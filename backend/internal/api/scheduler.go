package api

import (
	"context"
	"time"

	"doglyad/backend/internal/security"
)

// Періоди роботи планувальника.
const (
	schedulerTick   = 30 * time.Second
	maintenanceTick = time.Hour
	// escalationRepeatAfter — скільки чекати на реакцію, перш ніж повторити сигнал.
	escalationRepeatAfter = 15 * time.Minute
)

// runScheduler веде розклад: створює виконання, надсилає нагадування,
// відкриває ескалації та закриває прострочені справи.
func (s *Server) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	maintenance := time.NewTicker(maintenanceTick)
	defer maintenance.Stop()

	s.runCycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCycle(ctx)
		case <-maintenance.C:
			s.makeSuggestions(ctx)
			s.cleanup(ctx)
		}
	}
}

func (s *Server) runCycle(ctx context.Context) {
	s.materializeAll(ctx)
	s.dispatchReminders(ctx)
	s.openEscalations(ctx)
	s.repeatEscalations(ctx)
	s.closeExpired(ctx)
}

// materializeAll планує виконання для всіх активних завдань.
func (s *Server) materializeAll(ctx context.Context) {
	ids, err := s.collectStrings(ctx, `
		SELECT t.id FROM tasks t JOIN wards w ON w.id=t.ward_id
		WHERE t.deleted_at IS NULL AND w.deleted_at IS NULL`)
	if err != nil {
		s.logError("Не вдалося перелічити завдання планувальника", err)
		return
	}
	for _, taskID := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.materializeTask(ctx, taskID); err != nil {
			s.logError("Не вдалося запланувати виконання", err, "завдання", taskID)
		}
	}
}

// reminderJob — одне нагадування, яке настав час надіслати.
type reminderJob struct {
	id           string
	occurrenceID string
}

// dispatchReminders надсилає нагадування, час яких настав.
func (s *Server) dispatchReminders(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,occurrence_id FROM reminder_jobs
		WHERE status='created' AND due_at<=? AND (locked_until IS NULL OR locked_until<?)
		ORDER BY due_at LIMIT 100`, s.nowStamp(), s.nowStamp())
	if err != nil {
		s.logError("Не вдалося прочитати нагадування", err)
		return
	}
	jobs := []reminderJob{}
	for rows.Next() {
		var job reminderJob
		if err := rows.Scan(&job.id, &job.occurrenceID); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати нагадування", err)
			return
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити нагадування", err)
		return
	}
	rows.Close()

	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.dispatchReminder(ctx, job)
	}
}

func (s *Server) dispatchReminder(ctx context.Context, job reminderJob) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE reminder_jobs SET status='sending',locked_until=?,attempt=attempt+1
		WHERE id=? AND status='created'`, stamp(s.now().Add(time.Minute)), job.id)
	if err != nil {
		s.logError("Не вдалося зайняти нагадування", err)
		return
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return
	}

	var wardID, status, title, scheduledAt string
	var critical int
	err = s.db.QueryRowContext(ctx, `
		SELECT o.ward_id,o.status,t.title,o.scheduled_at,t.critical
		FROM task_occurrences o JOIN tasks t ON t.id=o.task_id WHERE o.id=?`, job.occurrenceID).
		Scan(&wardID, &status, &title, &scheduledAt, &critical)
	if err != nil {
		s.finishReminder(ctx, job.id, "виконання не знайдено")
		return
	}
	// Якщо справу вже виконано, нагадування не потрібне.
	if status == "planned" {
		s.remindHelpers(ctx, wardID, job.occurrenceID, title, scheduledAt, critical == 1)
	}
	s.finishReminder(ctx, job.id, "")
}

func (s *Server) finishReminder(ctx context.Context, jobID, failure string) {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE reminder_jobs SET status='sent',sent_at=?,locked_until=NULL,last_error=? WHERE id=?",
		s.nowStamp(), nullIfEmpty(failure), jobID); err != nil {
		s.logError("Не вдалося завершити нагадування", err)
	}
}

// escalationCandidate — критична справа, яку не виконали вчасно.
type escalationCandidate struct {
	occurrenceID string
	wardID       string
	title        string
}

// openEscalations відкриває ескалації для критичних справ поза вікном.
func (s *Server) openEscalations(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id,o.ward_id,t.title FROM task_occurrences o
		JOIN tasks t ON t.id=o.task_id
		WHERE t.critical=1 AND o.status='planned' AND t.deleted_at IS NULL
		  AND datetime(o.scheduled_at,'+' || t.escalation_window_minutes || ' minutes')<=datetime(?)
		  AND NOT EXISTS (SELECT 1 FROM escalations e WHERE e.occurrence_id=o.id)
		LIMIT 100`, s.nowStamp())
	if err != nil {
		s.logError("Не вдалося прочитати ескалації", err)
		return
	}
	candidates := []escalationCandidate{}
	for rows.Next() {
		var candidate escalationCandidate
		if err := rows.Scan(&candidate.occurrenceID, &candidate.wardID, &candidate.title); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати ескалацію", err)
			return
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити ескалації", err)
		return
	}
	rows.Close()

	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return
		}
		result, err := s.db.ExecContext(ctx,
			"INSERT OR IGNORE INTO escalations(id,occurrence_id,ward_id,opened_at) VALUES(?,?,?,?)",
			security.ID(), candidate.occurrenceID, candidate.wardID, s.nowStamp())
		if err != nil {
			s.logError("Не вдалося створити ескалацію", err)
			continue
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			continue
		}
		s.escalate(ctx, candidate.wardID, candidate.occurrenceID, candidate.title)
	}
}

// repeatEscalations нагадує ще раз, якщо ескалацію ніхто не взяв.
//
// Критична справа без реакції — найгірший сценарій застосунку, тому
// повідомлення повторюється один раз через чверть години.
func (s *Server) repeatEscalations(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id,e.ward_id,e.occurrence_id,t.title FROM escalations e
		JOIN task_occurrences o ON o.id=e.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		WHERE e.status='open' AND e.repeated_at IS NULL AND e.opened_at<=?
		LIMIT 50`, stamp(s.now().Add(-escalationRepeatAfter)))
	if err != nil {
		s.logError("Не вдалося прочитати ескалації для повтору", err)
		return
	}
	type pending struct{ id, wardID, occurrenceID, title string }
	all := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.wardID, &item.occurrenceID, &item.title); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати ескалацію", err)
			return
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити ескалації", err)
		return
	}
	rows.Close()

	for _, item := range all {
		if ctx.Err() != nil {
			return
		}
		result, err := s.db.ExecContext(ctx,
			"UPDATE escalations SET repeated_at=? WHERE id=? AND status='open' AND repeated_at IS NULL",
			s.nowStamp(), item.id)
		if err != nil {
			s.logError("Не вдалося позначити повтор ескалації", err)
			continue
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			continue
		}
		s.repeatEscalation(ctx, item.wardID, item.occurrenceID, item.title)
	}
}

// closeExpired закриває справи, час виконання яких минув.
func (s *Server) closeExpired(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE task_occurrences SET status='missed',system_closed_at=?
		WHERE status='planned' AND completion_deadline<?`, s.nowStamp(), s.nowStamp()); err != nil {
		s.logError("Не вдалося закрити прострочені справи", err)
	}
}

// cleanup прибирає застарілі записи раз на годину.
func (s *Server) cleanup(ctx context.Context) {
	statements := []struct {
		query string
		args  []any
	}{
		{"DELETE FROM pairing_codes WHERE expires_at<? OR used_at IS NOT NULL", []any{s.nowStamp()}},
		{"DELETE FROM invitations WHERE expires_at<? AND accepted_at IS NULL", []any{s.nowStamp()}},
		{"DELETE FROM email_tokens WHERE expires_at<? OR used_at IS NOT NULL", []any{s.nowStamp()}},
		{"DELETE FROM login_attempts WHERE updated_at<?", []any{stamp(s.now().Add(-24 * time.Hour))}},
		{"DELETE FROM adaptation_signals WHERE date<?", []any{s.now().AddDate(0, 0, -90).Format("2006-01-02")}},
		{"UPDATE pin_sessions SET revoked_at=? WHERE expires_at<? AND revoked_at IS NULL", []any{s.nowStamp(), s.nowStamp()}},
		{"UPDATE sessions SET revoked_at=? WHERE expires_at<? AND revoked_at IS NULL", []any{s.nowStamp(), s.nowStamp()}},
		{"DELETE FROM notifications WHERE status IN ('sent','failed','skipped') AND created_at<?",
			[]any{stamp(s.now().AddDate(0, 0, -30))}},
		{"DELETE FROM reminder_jobs WHERE status='sent' AND created_at<?", []any{stamp(s.now().AddDate(0, 0, -30))}},
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			s.log.Warn("Не вдалося виконати очищення", "запит", statement.query, "помилка", err)
		}
	}
}

// makeSuggestions шукає закономірності та пропонує зміни помічникові.
//
// Пропозиція ніколи не застосовується сама: рішення завжди за людиною.
func (s *Server) makeSuggestions(ctx context.Context) {
	s.suggestFromSignals(ctx)
	s.suggestFromMisses(ctx)
	s.suggestFromEscalations(ctx)
}

// suggestFromSignals читає знеособлені сигнали зручності екрана.
func (s *Server) suggestFromSignals(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ward_id,
		       SUM(viewport_zoom_sessions+magnifier_uses),SUM(quick_undos),SUM(speech_uses),SUM(unsure_uses)
		FROM adaptation_signals WHERE date>=? GROUP BY ward_id`,
		s.now().AddDate(0, 0, -7).Format("2006-01-02"))
	if err != nil {
		s.logError("Не вдалося прочитати сигнали адаптації", err)
		return
	}
	type wardSignals struct {
		wardID                      string
		zoom, undos, speech, unsure int
	}
	all := []wardSignals{}
	for rows.Next() {
		var item wardSignals
		if err := rows.Scan(&item.wardID, &item.zoom, &item.undos, &item.speech, &item.unsure); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати сигнал", err)
			return
		}
		all = append(all, item)
	}
	rows.Close()

	yes := true
	for _, item := range all {
		if item.zoom >= 3 {
			s.createSuggestion(ctx, item.wardID, "", "R4",
				"Екран збільшували щонайменше у трьох сеансах за тиждень.", proposal{Scale: "next"})
		}
		if item.undos >= 5 {
			s.createSuggestion(ctx, item.wardID, "", "R5",
				"За тиждень підопічний скасував щонайменше п'ять дій — імовірно, важко влучати в кнопки.",
				proposal{LargeControls: &yes})
		}
		if item.speech >= 10 {
			s.createSuggestion(ctx, item.wardID, "", "R6",
				"Озвучення вмикали щонайменше десять разів за тиждень.", proposal{SpeechEnabled: &yes})
		}
		if item.unsure >= 3 {
			s.createSuggestion(ctx, item.wardID, "", "R7",
				"Кнопку «Я не пам'ятаю» натискали щонайменше тричі за тиждень.", proposal{DementiaMode: &yes})
		}
	}
}

// suggestFromMisses пропонує перенести час справи, яку регулярно пропускають.
func (s *Server) suggestFromMisses(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.ward_id,o.task_id,t.title,COUNT(*) FROM task_occurrences o
		JOIN tasks t ON t.id=o.task_id
		WHERE o.status='missed' AND o.scheduled_at>=? AND t.deleted_at IS NULL
		GROUP BY o.task_id HAVING COUNT(*)>=3`, stamp(s.now().AddDate(0, 0, -14)))
	if err != nil {
		s.logError("Не вдалося прочитати пропущені справи", err)
		return
	}
	type missedTask struct{ wardID, taskID, title string }
	all := []missedTask{}
	for rows.Next() {
		var item missedTask
		var count int
		if err := rows.Scan(&item.wardID, &item.taskID, &item.title, &count); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати пропущену справу", err)
			return
		}
		all = append(all, item)
	}
	rows.Close()

	for _, item := range all {
		s.createSuggestion(ctx, item.wardID, item.taskID, "R1",
			"Справу «"+item.title+"» пропустили щонайменше тричі за два тижні. Можливо, час незручний.",
			proposal{ShiftMinutes: 30})
	}
}

// suggestFromEscalations пропонує додати нагадування для критичних справ.
func (s *Server) suggestFromEscalations(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.ward_id,o.task_id,t.title,COUNT(*) FROM escalations e
		JOIN task_occurrences o ON o.id=e.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		WHERE e.opened_at>=? AND t.deleted_at IS NULL AND t.reminder_repeats<3
		GROUP BY o.task_id HAVING COUNT(*)>=2`, stamp(s.now().AddDate(0, 0, -14)))
	if err != nil {
		s.logError("Не вдалося прочитати ескалації для рекомендацій", err)
		return
	}
	type escalatedTask struct{ wardID, taskID, title string }
	all := []escalatedTask{}
	for rows.Next() {
		var item escalatedTask
		var count int
		if err := rows.Scan(&item.wardID, &item.taskID, &item.title, &count); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати ескалацію", err)
			return
		}
		all = append(all, item)
	}
	rows.Close()

	for _, item := range all {
		s.createSuggestion(ctx, item.wardID, item.taskID, "R2",
			"Критичну справу «"+item.title+"» довелося ескалювати двічі за два тижні.",
			proposal{ExtraReminder: true})
	}
}

// collectStrings зчитує один стовпець у зріз, щоб не тримати курсор відкритим.
func (s *Server) collectStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// Materialize створює виконання для всіх активних завдань.
// Потрібен командам, які готують дані поза HTTP-сервером.
func (s *Server) Materialize(ctx context.Context) { s.materializeAll(ctx) }
