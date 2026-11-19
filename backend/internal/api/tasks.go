package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"doglyad/backend/internal/recurrence"
	"doglyad/backend/internal/security"
)

// Горизонт планування: на скільки наперед створюються виконання.
const (
	scheduleHorizon = 15 * 24 * time.Hour
	scheduleBackfil = 24 * time.Hour
	maxTitleWords   = 5
	maxSteps        = 10
)

// taskInput — дані завдання, які надсилає інтерфейс помічника.
type taskInput struct {
	Title                   string   `json:"title"`
	Category                string   `json:"category"`
	Description             string   `json:"description"`
	Critical                bool     `json:"critical"`
	LocalStartDate          string   `json:"localStartDate"`
	LocalTime               string   `json:"localTime"`
	RecurrenceKind          string   `json:"recurrenceKind"`
	RecurrenceDays          []int    `json:"recurrenceDays"`
	RecurrenceInterval      int      `json:"recurrenceInterval"`
	CompletionWindowMinutes int      `json:"completionWindowMinutes"`
	EscalationWindowMinutes int      `json:"escalationWindowMinutes"`
	WarningMinutes          int      `json:"warningMinutes"`
	ReminderRepeats         int      `json:"reminderRepeats"`
	ReminderIntervalMinutes int      `json:"reminderIntervalMinutes"`
	Steps                   []string `json:"steps"`
}

// applyDefaults підставляє значення, які інтерфейс може не надіслати.
func (in *taskInput) applyDefaults() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if in.RecurrenceKind == "" {
		in.RecurrenceKind = recurrence.Once
	}
	if in.RecurrenceInterval < 1 {
		in.RecurrenceInterval = 1
	}
	if in.CompletionWindowMinutes <= 0 {
		in.CompletionWindowMinutes = 120
	}
	if in.EscalationWindowMinutes <= 0 {
		in.EscalationWindowMinutes = 30
	}
	if in.ReminderIntervalMinutes <= 0 {
		in.ReminderIntervalMinutes = 5
	}
	cleaned := make([]string, 0, len(in.Steps))
	for _, step := range in.Steps {
		if trimmed := strings.TrimSpace(step); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	in.Steps = cleaned
}

// validate перевіряє дані завдання та повертає зрозумілу помилку.
func (in taskInput) validate() error {
	if in.Title == "" {
		return errInvalid("Вкажіть коротку назву завдання.")
	}
	if len(strings.Fields(in.Title)) > maxTitleWords {
		return errInvalid("Назва має вміщатися у п'ять слів — так її легше прочитати.")
	}
	switch in.Category {
	case "medicine", "procedure", "visit", "food", "other":
	default:
		return errInvalid("Оберіть категорію: ліки, процедура, візит, їжа або інше.")
	}
	if _, err := time.Parse("2006-01-02", in.LocalStartDate); err != nil {
		return errInvalid("Вкажіть дату початку у форматі 2026-01-31.")
	}
	if _, err := time.Parse("15:04", in.LocalTime); err != nil {
		return errInvalid("Вкажіть час у форматі 09:00.")
	}
	if !recurrence.Valid(in.RecurrenceKind, in.RecurrenceDays, in.RecurrenceInterval) {
		return errInvalid("Перевірте повторення: для щотижневого оберіть хоча б один день.")
	}
	if in.CompletionWindowMinutes < 5 || in.CompletionWindowMinutes > 1440 {
		return errInvalid("Вікно виконання може бути від 5 хвилин до доби.")
	}
	if in.EscalationWindowMinutes < 5 || in.EscalationWindowMinutes > 720 {
		return errInvalid("Вікно ескалації може бути від 5 хвилин до 12 годин.")
	}
	if in.WarningMinutes < 0 || in.WarningMinutes > 120 {
		return errInvalid("Попередження можна надіслати не раніше ніж за дві години.")
	}
	if in.ReminderRepeats < 0 || in.ReminderRepeats > 3 {
		return errInvalid("Повторних нагадувань може бути не більше трьох.")
	}
	if in.ReminderIntervalMinutes < 1 || in.ReminderIntervalMinutes > 60 {
		return errInvalid("Інтервал нагадувань може бути від 1 до 60 хвилин.")
	}
	if len(in.Steps) > maxSteps {
		return errInvalid("Кроків може бути не більше десяти.")
	}
	return nil
}

// createTask створює завдання та одразу планує найближчі виконання.
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	var in taskInput
	if !s.readJSON(w, r, &in) {
		return
	}
	in.applyDefaults()
	if err := in.validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var timezone string
	if err := s.db.QueryRowContext(r.Context(), "SELECT timezone FROM wards WHERE id=?", wardID).Scan(&timezone); err != nil {
		s.fail(w, http.StatusNotFound, "Підопічного не знайдено.", err)
		return
	}
	days, err := json.Marshal(in.RecurrenceDays)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "Не вдалося прочитати дні повторення.", err)
		return
	}

	taskID := security.ID()
	helperID := helperOf(r).ID
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити завдання.", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO tasks(id,ward_id,title,category,description,critical,local_start_date,local_time,timezone,
			recurrence_kind,recurrence_days,recurrence_interval,completion_window_minutes,escalation_window_minutes,
			warning_minutes,reminder_repeats,reminder_interval_minutes,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		taskID, wardID, in.Title, in.Category, in.Description, boolInt(in.Critical),
		in.LocalStartDate, in.LocalTime, timezone, in.RecurrenceKind, string(days), in.RecurrenceInterval,
		in.CompletionWindowMinutes, in.EscalationWindowMinutes, in.WarningMinutes,
		in.ReminderRepeats, in.ReminderIntervalMinutes, helperID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити завдання.", err)
		return
	}
	for position, step := range in.Steps {
		if _, err := tx.Exec("INSERT INTO task_steps(id,task_id,position,text) VALUES(?,?,?,?)",
			security.ID(), taskID, position, step); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти кроки завдання.", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити завдання.", err)
		return
	}
	if err := s.materializeTask(r.Context(), taskID); err != nil {
		s.logError("Не вдалося запланувати виконання", err, "завдання", taskID)
	}
	s.audit(r.Context(), wardID, helperID, "створено", "завдання", taskID)
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": taskID})
}

// getTask повертає одне завдання з кроками.
func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	wardID, err := s.wardOfTask(r.Context(), taskID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Завдання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	task, err := s.readTask(r.Context(), taskID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати завдання.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, task)
}

// listTasks повертає всі завдання підопічного.
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id FROM tasks WHERE ward_id=? AND deleted_at IS NULL ORDER BY local_time,title`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати завдання.", err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати завдання.", err)
			return
		}
		ids = append(ids, taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати завдання.", err)
		return
	}
	rows.Close()

	out := make([]map[string]any, 0, len(ids))
	for _, taskID := range ids {
		task, err := s.readTask(r.Context(), taskID)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати завдання.", err)
			return
		}
		out = append(out, task)
	}
	s.writeJSON(w, http.StatusOK, out)
}

// readTask збирає повні дані завдання разом із кроками.
func (s *Server) readTask(ctx context.Context, taskID string) (map[string]any, error) {
	var title, category, description, startDate, clock, timezone, kind, daysRaw, photo string
	var critical, interval, completion, escalation, warning, repeats, reminderInterval int
	err := s.db.QueryRowContext(ctx, `
		SELECT title,category,description,critical,local_start_date,local_time,timezone,recurrence_kind,
		       recurrence_days,recurrence_interval,completion_window_minutes,escalation_window_minutes,
		       warning_minutes,reminder_repeats,reminder_interval_minutes,COALESCE(photo_media_id,'')
		FROM tasks WHERE id=? AND deleted_at IS NULL`, taskID).
		Scan(&title, &category, &description, &critical, &startDate, &clock, &timezone, &kind,
			&daysRaw, &interval, &completion, &escalation, &warning, &repeats, &reminderInterval, &photo)
	if err != nil {
		return nil, err
	}
	var weekdays []int
	if err := json.Unmarshal([]byte(daysRaw), &weekdays); err != nil {
		weekdays = []int{}
	}
	if weekdays == nil {
		weekdays = []int{}
	}
	steps, err := s.readSteps(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                      taskID,
		"title":                   title,
		"category":                category,
		"description":             description,
		"critical":                critical == 1,
		"localStartDate":          startDate,
		"localTime":               clock,
		"timezone":                timezone,
		"recurrenceKind":          kind,
		"recurrenceDays":          weekdays,
		"recurrenceInterval":      interval,
		"completionWindowMinutes": completion,
		"escalationWindowMinutes": escalation,
		"warningMinutes":          warning,
		"reminderRepeats":         repeats,
		"reminderIntervalMinutes": reminderInterval,
		"photo":                   photo,
		"steps":                   steps,
	}, nil
}

func (s *Server) readSteps(ctx context.Context, taskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT text FROM task_steps WHERE task_id=? ORDER BY position", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []string{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		steps = append(steps, text)
	}
	return steps, rows.Err()
}

// updateTask змінює завдання. Зміни діють лише для майбутніх виконань.
func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	wardID, err := s.wardOfTask(r.Context(), taskID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Завдання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	var in taskInput
	if !s.readJSON(w, r, &in) {
		return
	}
	in.applyDefaults()
	if err := in.validate(); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	days, err := json.Marshal(in.RecurrenceDays)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "Не вдалося прочитати дні повторення.", err)
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти завдання.", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		UPDATE tasks SET title=?,category=?,description=?,critical=?,local_start_date=?,local_time=?,
			recurrence_kind=?,recurrence_days=?,recurrence_interval=?,completion_window_minutes=?,
			escalation_window_minutes=?,warning_minutes=?,reminder_repeats=?,reminder_interval_minutes=?
		WHERE id=? AND deleted_at IS NULL`,
		in.Title, in.Category, in.Description, boolInt(in.Critical), in.LocalStartDate, in.LocalTime,
		in.RecurrenceKind, string(days), in.RecurrenceInterval, in.CompletionWindowMinutes,
		in.EscalationWindowMinutes, in.WarningMinutes, in.ReminderRepeats, in.ReminderIntervalMinutes,
		taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти завдання.", err)
		return
	}
	if _, err := tx.Exec("DELETE FROM task_steps WHERE task_id=?", taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося оновити кроки.", err)
		return
	}
	for position, step := range in.Steps {
		if _, err := tx.Exec("INSERT INTO task_steps(id,task_id,position,text) VALUES(?,?,?,?)",
			security.ID(), taskID, position, step); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося оновити кроки.", err)
			return
		}
	}
	// Уже виконані відмітки лишаються недоторканими: переплановуємо лише майбутнє.
	if _, err := tx.Exec(
		"DELETE FROM task_occurrences WHERE task_id=? AND status='planned' AND scheduled_at>?",
		taskID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося перепланувати виконання.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти завдання.", err)
		return
	}
	if err := s.materializeTask(r.Context(), taskID); err != nil {
		s.logError("Не вдалося перепланувати завдання", err, "завдання", taskID)
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "змінено", "завдання", taskID)
	w.WriteHeader(http.StatusNoContent)
}

// deleteTask прибирає завдання та його майбутні виконання.
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	wardID, err := s.wardOfTask(r.Context(), taskID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Завдання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося видалити завдання.", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE tasks SET deleted_at=? WHERE id=?", s.nowStamp(), taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося видалити завдання.", err)
		return
	}
	if _, err := tx.Exec(
		"DELETE FROM reminder_jobs WHERE status='created' AND occurrence_id IN (SELECT id FROM task_occurrences WHERE task_id=? AND status='planned')",
		taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прибрати нагадування.", err)
		return
	}
	if _, err := tx.Exec("DELETE FROM task_occurrences WHERE task_id=? AND status='planned'", taskID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прибрати виконання.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося видалити завдання.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "видалено", "завдання", taskID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wardOfTask(ctx context.Context, taskID string) (string, error) {
	var wardID string
	err := s.db.QueryRowContext(ctx, "SELECT ward_id FROM tasks WHERE id=? AND deleted_at IS NULL", taskID).Scan(&wardID)
	return wardID, err
}

// materializeTask створює виконання та нагадування на горизонті планування.
func (s *Server) materializeTask(ctx context.Context, taskID string) error {
	var wardID, startDate, clock, timezone, kind, daysRaw string
	var interval, window, warning, repeats, reminderInterval int
	err := s.db.QueryRowContext(ctx, `
		SELECT ward_id,local_start_date,local_time,timezone,recurrence_kind,recurrence_days,recurrence_interval,
		       completion_window_minutes,warning_minutes,reminder_repeats,reminder_interval_minutes
		FROM tasks WHERE id=? AND deleted_at IS NULL`, taskID).
		Scan(&wardID, &startDate, &clock, &timezone, &kind, &daysRaw, &interval,
			&window, &warning, &repeats, &reminderInterval)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	var weekdays []int
	if err := json.Unmarshal([]byte(daysRaw), &weekdays); err != nil {
		weekdays = nil
	}
	rule := recurrence.Rule{
		StartDate: startDate,
		TimeOfDay: clock,
		Timezone:  timezone,
		Kind:      kind,
		Weekdays:  weekdays,
		Interval:  interval,
	}
	moments, err := rule.Occurrences(s.now().Add(-scheduleBackfil), s.now().Add(scheduleHorizon))
	if err != nil {
		return err
	}
	for _, moment := range moments {
		occurrenceID := occurrenceIdentifier(taskID, moment)
		deadline := moment.Add(time.Duration(window) * time.Minute)
		if _, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO task_occurrences(id,task_id,ward_id,scheduled_at,completion_deadline,created_at)
			VALUES(?,?,?,?,?,?)`,
			occurrenceID, taskID, wardID, stamp(moment), stamp(deadline), s.nowStamp()); err != nil {
			return err
		}
		for attempt := 0; attempt <= repeats; attempt++ {
			due := moment.Add(time.Duration(attempt*reminderInterval) * time.Minute)
			if attempt == 0 {
				due = moment.Add(-time.Duration(warning) * time.Minute)
			}
			if _, err := s.db.ExecContext(ctx, `
				INSERT OR IGNORE INTO reminder_jobs(id,occurrence_id,kind,due_at,idempotency_key,created_at)
				VALUES(?,?,?,?,?,?)`,
				security.ID(), occurrenceID, "нагадування", stamp(due),
				occurrenceID+"-"+strconv.Itoa(attempt), s.nowStamp()); err != nil {
				return err
			}
		}
	}
	return nil
}

// rescheduleWard переплановує всі завдання підопічного, наприклад після зміни часового поясу.
func (s *Server) rescheduleWard(ctx context.Context, wardID string) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM tasks WHERE ward_id=? AND deleted_at IS NULL", wardID)
	if err != nil {
		s.logError("Не вдалося перелічити завдання для перепланування", err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати завдання", err)
			return
		}
		ids = append(ids, taskID)
	}
	rows.Close()
	for _, taskID := range ids {
		if _, err := s.db.ExecContext(ctx,
			"DELETE FROM task_occurrences WHERE task_id=? AND status='planned' AND scheduled_at>?",
			taskID, s.nowStamp()); err != nil {
			s.logError("Не вдалося прибрати заплановані виконання", err)
			continue
		}
		if err := s.materializeTask(ctx, taskID); err != nil {
			s.logError("Не вдалося перепланувати завдання", err, "завдання", taskID)
		}
	}
}

// occurrenceIdentifier робить ідентифікатор виконання передбачуваним,
// тому повторне планування не створює дублікатів.
func occurrenceIdentifier(taskID string, moment time.Time) string {
	return security.Hash(taskID + "|" + stamp(moment))[:32]
}
