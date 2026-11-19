package api

import (
	"context"
	"net/http"
	"time"

	"doglyad/backend/internal/notify"
)

// occurrenceRow — одне виконання завдання у відповіді API.
type occurrenceRow struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskID"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	Description string `json:"description"`
	ScheduledAt string `json:"scheduledAt"`
	// OpenFrom — мить, з якої справу вже можна відмітити. Завдання з'являється
	// на екрані заздалегідь, щоб підопічний устиг підготуватися, але відмітити
	// завтрашні ліки сьогодні ввечері не можна: вони лишилися б непринятими,
	// а помічник бачив би «виконано».
	OpenFrom    string `json:"openFrom"`
	Deadline    string `json:"deadline"`
	Status      string `json:"status"`
	CompletedAt string `json:"completedAt"`
	Critical    bool   `json:"critical"`
	MarkedBy    string `json:"markedBy"`
	UndoUntil   string `json:"undoUntil"`
}

// listOccurrences повертає виконання підопічного за проміжок часу.
func (s *Server) listOccurrences(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	from := r.URL.Query().Get("from")
	if from == "" {
		from = stamp(s.now().Add(-12 * time.Hour))
	}
	to := r.URL.Query().Get("to")
	if to == "" {
		to = stamp(s.now().Add(scheduleHorizon))
	}
	items, err := s.occurrencesBetween(r.Context(), wardID, from, to)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати виконання.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) occurrencesBetween(ctx context.Context, wardID, from, to string) ([]occurrenceRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id,o.task_id,t.title,t.category,t.description,o.scheduled_at,o.completion_deadline,o.status,
		       COALESCE(o.completed_at,''),t.critical,COALESCE(h.display_name,''),COALESCE(o.undo_until,''),
		       t.warning_minutes
		FROM task_occurrences o
		JOIN tasks t ON t.id=o.task_id
		LEFT JOIN helpers h ON h.id=o.marked_by_helper_id
		WHERE o.ward_id=? AND o.scheduled_at>=? AND o.scheduled_at<=?
		ORDER BY o.scheduled_at`, wardID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []occurrenceRow{}
	for rows.Next() {
		var item occurrenceRow
		var critical, warningMinutes int
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Title, &item.Category, &item.Description,
			&item.ScheduledAt, &item.Deadline, &item.Status, &item.CompletedAt, &critical,
			&item.MarkedBy, &item.UndoUntil, &warningMinutes); err != nil {
			return nil, err
		}
		item.Critical = critical == 1
		item.OpenFrom = openFrom(item.ScheduledAt, warningMinutes)
		out = append(out, item)
	}
	return out, rows.Err()
}

// markByHelper дозволяє помічникові відмітити виконання за підопічного.
func (s *Server) markByHelper(w http.ResponseWriter, r *http.Request) {
	occurrenceID := r.PathValue("id")
	wardID, err := s.wardOfOccurrence(r.Context(), occurrenceID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Виконання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	switch in.Status {
	case "done", "missed", "planned":
	default:
		s.writeError(w, http.StatusBadRequest, "Статус може бути: виконано, пропущено або заплановано.")
		return
	}
	completedAt := any(nil)
	if in.Status == "done" {
		completedAt = s.nowStamp()
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE task_occurrences SET status=?,completed_at=?,marked_by_helper_id=?,undo_until=? WHERE id=?`,
		in.Status, completedAt, helperOf(r).ID, stamp(s.now().Add(s.cfg.UndoWindow)), occurrenceID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти відмітку.", err)
		return
	}
	// Відмітка закриває відкриту ескалацію за цим виконанням.
	s.closeEscalationFor(r.Context(), occurrenceID, helperOf(r).ID)
	s.audit(r.Context(), wardID, helperOf(r).ID, "позначено "+in.Status, "виконання", occurrenceID)
	s.publish(r.Context(), wardID, EventOccurrence, "Відмітку оновив помічник", false, map[string]any{
		"occurrenceID": occurrenceID,
		"status":       in.Status,
	})
	w.WriteHeader(http.StatusNoContent)
}

// undoByHelper скасовує помилкову відмітку протягом дозволеного вікна.
func (s *Server) undoByHelper(w http.ResponseWriter, r *http.Request) {
	occurrenceID := r.PathValue("id")
	wardID, err := s.wardOfOccurrence(r.Context(), occurrenceID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Виконання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	if err := s.undoOccurrence(r.Context(), occurrenceID); err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "скасовано відмітку", "виконання", occurrenceID)
	s.publish(r.Context(), wardID, EventOccurrence, "Відмітку скасовано", false, map[string]any{"occurrenceID": occurrenceID})
	w.WriteHeader(http.StatusNoContent)
}

// undoOccurrence повертає виконання у стан «заплановано», якщо вікно скасування ще діє.
func (s *Server) undoOccurrence(ctx context.Context, occurrenceID string) error {
	var status, undoUntil string
	err := s.db.QueryRowContext(ctx,
		"SELECT status,COALESCE(undo_until,'') FROM task_occurrences WHERE id=?", occurrenceID).Scan(&status, &undoUntil)
	if err != nil {
		return errInvalid("Виконання не знайдено.")
	}
	if status == "planned" {
		return errInvalid("Цю справу ще не відмічено.")
	}
	if undoUntil == "" || undoUntil < s.nowStamp() {
		return errInvalid("Час на скасування минув. Попросіть помічника змінити відмітку.")
	}
	// Лічильник виправлень показує, скільки справ вдалося відмітити з першого разу.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE task_occurrences SET status='planned',completed_at=NULL,device_time=NULL,
			marked_by_helper_id=NULL,device_id=NULL,idempotency_key=NULL,undo_until=NULL,
			correction_count=correction_count+1
		WHERE id=?`, occurrenceID); err != nil {
		return errInvalid("Не вдалося скасувати відмітку.")
	}
	return nil
}

// closeEscalationFor закриває ескалацію, коли справу нарешті виконано.
func (s *Server) closeEscalationFor(ctx context.Context, occurrenceID, helperID string) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE escalations SET status='closed',closed_at=?,taken_by=COALESCE(taken_by,?)
		WHERE occurrence_id=? AND status<>'closed'`, s.nowStamp(), nullIfEmpty(helperID), occurrenceID)
	if err != nil {
		s.log.Warn("Не вдалося закрити ескалацію", "помилка", err)
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected > 0 {
		wardID, err := s.wardOfOccurrence(ctx, occurrenceID)
		if err == nil {
			s.publish(ctx, wardID, EventSignalDone, "Ескалацію закрито", false, map[string]any{"occurrenceID": occurrenceID})
		}
	}
}

// remindHelpers надсилає нагадування про заплановану справу.
func (s *Server) remindHelpers(ctx context.Context, wardID, occurrenceID, title, scheduledAt string, critical bool) {
	name := s.wardName(ctx, wardID)
	clock := localClock(scheduledAt, s.wardLocation(ctx, wardID))
	s.notifyWard(ctx, wardID, notify.Message{
		Kind:     notify.KindReminder,
		Title:    "Нагадування: " + title,
		Body:     "Профіль: " + name + ". Заплановано на " + clock + ".",
		URL:      s.cfg.PublicURL + "/?ward=" + wardID,
		WardName: name,
		Urgent:   critical,
	}, false)
	s.publish(ctx, wardID, EventOccurrence, "Нагадування: "+title, critical, map[string]any{
		"occurrenceID": occurrenceID,
	})
}

// localClock показує лише час, щоб текст сповіщення був коротким.
// Пояс обов'язковий: без нього 08:00 за Києвом перетворюється на 05:00 і
// помічник отримує нагадування з чужим часом.
func localClock(value string, location *time.Location) string {
	parsed, ok := parseStamp(value)
	if !ok {
		return value
	}
	return parsed.In(location).Format("15:04")
}

// openFrom рахує мить, з якої справу вже можна відмітити: попередження про
// справу приходить за warning_minutes до часу, і з цієї ж миті кнопка працює.
func openFrom(scheduledAt string, warningMinutes int) string {
	parsed, ok := parseStamp(scheduledAt)
	if !ok {
		return scheduledAt
	}
	if warningMinutes < 0 {
		warningMinutes = 0
	}
	return stamp(parsed.Add(-time.Duration(warningMinutes) * time.Minute))
}
