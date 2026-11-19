package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"doglyad/backend/internal/security"
)

// proposal описує зміну, яку рекомендує застосунок.
// Порожні поля означають «не чіпати».
type proposal struct {
	// Scale приймає значення "next" — наступний крок збільшення екрана.
	Scale         string `json:"scale,omitempty"`
	LargeControls *bool  `json:"largeControls,omitempty"`
	SpeechEnabled *bool  `json:"speechEnabled,omitempty"`
	DementiaMode  *bool  `json:"dementiaMode,omitempty"`
	// ShiftMinutes зсуває час завдання, якщо його регулярно виконують пізніше.
	ShiftMinutes int `json:"shiftMinutes,omitempty"`
	// ExtraReminder додає ще одне повторне нагадування.
	ExtraReminder bool `json:"extraReminder,omitempty"`
}

// listSuggestions повертає рекомендації щодо налаштувань підопічного.
func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT a.id,COALESCE(a.task_id,''),COALESCE(t.title,''),a.rule_code,a.status,a.explanation,
		       a.proposal_json,a.created_at,COALESCE(a.decided_at,''),COALESCE(h.display_name,'')
		FROM adaptation_suggestions a
		LEFT JOIN tasks t ON t.id=a.task_id
		LEFT JOIN helpers h ON h.id=a.decided_by
		WHERE a.ward_id=? ORDER BY a.created_at DESC LIMIT 50`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати рекомендації.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, taskID, taskTitle, rule, status, explanation, proposalJSON, createdAt, decidedAt, decidedBy string
		if err := rows.Scan(&id, &taskID, &taskTitle, &rule, &status, &explanation,
			&proposalJSON, &createdAt, &decidedAt, &decidedBy); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати рекомендації.", err)
			return
		}
		var parsed proposal
		if err := json.Unmarshal([]byte(proposalJSON), &parsed); err != nil {
			s.log.Warn("Не вдалося прочитати пропозицію", "рекомендація", id, "помилка", err)
		}
		out = append(out, map[string]any{
			"id": id, "taskID": taskID, "taskTitle": taskTitle, "rule": rule, "status": status,
			"explanation": explanation, "proposal": parsed, "humanProposal": describeProposal(parsed),
			"createdAt": createdAt, "decidedAt": decidedAt, "decidedBy": decidedBy,
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати рекомендації.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// decideSuggestion приймає рішення помічника та, у разі згоди, застосовує зміну.
func (s *Server) decideSuggestion(w http.ResponseWriter, r *http.Request) {
	suggestionID := r.PathValue("id")
	var wardID, taskID, rule, proposalJSON, status string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT ward_id,COALESCE(task_id,''),rule_code,proposal_json,status
		FROM adaptation_suggestions WHERE id=?`, suggestionID).
		Scan(&wardID, &taskID, &rule, &proposalJSON, &status)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Рекомендацію не знайдено.")
		return
	}
	if status != "created" {
		s.writeError(w, http.StatusConflict, "Рішення за цією рекомендацією вже ухвалено.")
		return
	}
	// Зміни розкладу потребують права на завдання, зміни екрана — права на налаштування.
	permission := permSettings
	if taskID != "" {
		permission = permTasks
	}
	if !s.requireWard(w, r, wardID, permission) {
		return
	}
	var in struct {
		Decision string `json:"decision"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	switch in.Decision {
	case "accepted", "declined", "deferred":
	default:
		s.writeError(w, http.StatusBadRequest, "Оберіть: прийняти, відхилити або відкласти.")
		return
	}

	helper := helperOf(r)
	var before, after string
	if in.Decision == "accepted" {
		var parsed proposal
		if err := json.Unmarshal([]byte(proposalJSON), &parsed); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати пропозицію.", err)
			return
		}
		before, after, err = s.applyProposal(r.Context(), wardID, taskID, helper.ID, parsed)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося застосувати рекомендацію.", err)
			return
		}
	}
	suppressed := ""
	if in.Decision == "declined" {
		// Відхилену рекомендацію не повторюємо місяць.
		suppressed = stamp(s.now().Add(30 * 24 * time.Hour))
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE adaptation_suggestions SET status=?,decided_at=?,decided_by=?,suppressed_until=?,
			effect_before_json=?,effect_after_json=? WHERE id=?`,
		in.Decision, s.nowStamp(), helper.ID, nullIfEmpty(suppressed),
		nullIfEmpty(before), nullIfEmpty(after), suggestionID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти рішення.", err)
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "рішення "+in.Decision, "рекомендація", suggestionID)
	w.WriteHeader(http.StatusNoContent)
}

// applyProposal змінює налаштування або завдання та повертає стан до і після.
func (s *Server) applyProposal(ctx context.Context, wardID, taskID, helperID string, p proposal) (string, string, error) {
	if taskID != "" {
		return s.applyTaskProposal(ctx, taskID, p)
	}
	settings, err := s.readAccessibility(ctx, wardID)
	if err != nil {
		return "", "", err
	}
	before, err := json.Marshal(settings)
	if err != nil {
		return "", "", err
	}
	if p.Scale == "next" {
		settings.Scale = nextScale(settings.Scale)
	}
	if p.LargeControls != nil {
		settings.LargeControls = *p.LargeControls
	}
	if p.SpeechEnabled != nil {
		settings.SpeechEnabled = *p.SpeechEnabled
	}
	if p.DementiaMode != nil {
		settings.DementiaMode = *p.DementiaMode
	}
	if err := s.saveAccessibility(ctx, wardID, helperID, settings); err != nil {
		return "", "", err
	}
	after, err := json.Marshal(settings)
	if err != nil {
		return string(before), "", err
	}
	return string(before), string(after), nil
}

// applyTaskProposal зсуває час завдання або додає нагадування.
func (s *Server) applyTaskProposal(ctx context.Context, taskID string, p proposal) (string, string, error) {
	var clock string
	var repeats int
	if err := s.db.QueryRowContext(ctx,
		"SELECT local_time,reminder_repeats FROM tasks WHERE id=? AND deleted_at IS NULL", taskID).
		Scan(&clock, &repeats); err != nil {
		return "", "", err
	}
	before, err := json.Marshal(map[string]any{"localTime": clock, "reminderRepeats": repeats})
	if err != nil {
		return "", "", err
	}
	newClock := clock
	if p.ShiftMinutes != 0 {
		parsed, err := time.Parse("15:04", clock)
		if err != nil {
			return string(before), "", err
		}
		newClock = parsed.Add(time.Duration(p.ShiftMinutes) * time.Minute).Format("15:04")
	}
	newRepeats := repeats
	if p.ExtraReminder && repeats < 3 {
		newRepeats = repeats + 1
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE tasks SET local_time=?,reminder_repeats=? WHERE id=?", newClock, newRepeats, taskID); err != nil {
		return string(before), "", err
	}
	// Майбутні виконання переплановуємо за новим часом.
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM task_occurrences WHERE task_id=? AND status='planned' AND scheduled_at>?",
		taskID, s.nowStamp()); err != nil {
		return string(before), "", err
	}
	if err := s.materializeTask(ctx, taskID); err != nil {
		return string(before), "", err
	}
	after, err := json.Marshal(map[string]any{"localTime": newClock, "reminderRepeats": newRepeats})
	if err != nil {
		return string(before), "", err
	}
	return string(before), string(after), nil
}

// describeProposal перекладає пропозицію людською мовою для картки рекомендації.
func describeProposal(p proposal) string {
	switch {
	case p.Scale == "next":
		return "Збільшити шрифт на екрані підопічного на один крок."
	case p.LargeControls != nil && *p.LargeControls:
		return "Зробити кнопки більшими, щоб у них легше було влучати."
	case p.SpeechEnabled != nil && *p.SpeechEnabled:
		return "Увімкнути озвучення тексту на екрані."
	case p.DementiaMode != nil && *p.DementiaMode:
		return "Увімкнути режим підказок: показувати дату, день тижня та підтвердження приймання."
	case p.ShiftMinutes != 0:
		return "Перенести час справи на " + minutesText(p.ShiftMinutes) + "."
	case p.ExtraReminder:
		return "Додати ще одне повторне нагадування."
	}
	return "Змін немає."
}

func minutesText(minutes int) string {
	if minutes < 0 {
		return strconv.Itoa(-minutes) + " хвилин раніше"
	}
	return strconv.Itoa(minutes) + " хвилин пізніше"
}

func nextScale(current int) int {
	switch current {
	case 100:
		return 125
	case 125:
		return 150
	default:
		return 200
	}
}

// createSuggestion додає рекомендацію, якщо таку саму не відхиляли нещодавно.
func (s *Server) createSuggestion(ctx context.Context, wardID, taskID, rule, explanation string, p proposal) {
	payload, err := json.Marshal(p)
	if err != nil {
		s.logError("Не вдалося серіалізувати пропозицію", err)
		return
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM adaptation_suggestions
		WHERE ward_id=? AND rule_code=? AND (
			status='created'
			OR (suppressed_until IS NOT NULL AND suppressed_until>?)
			OR created_at>?
		)`, wardID, rule, s.nowStamp(), stamp(s.now().Add(-7*24*time.Hour))).Scan(&existing); err != nil {
		s.log.Warn("Не вдалося перевірити рекомендації", "помилка", err)
		return
	}
	if existing > 0 {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO adaptation_suggestions(id,ward_id,task_id,rule_code,explanation,proposal_json,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		security.ID(), wardID, nullIfEmpty(taskID), rule, explanation, string(payload), s.nowStamp()); err != nil {
		s.logError("Не вдалося створити рекомендацію", err)
	}
}
