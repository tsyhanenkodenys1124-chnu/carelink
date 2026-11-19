package api

import (
	"context"
	"net/http"
	"strings"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

// signal — один відкритий сигнал від підопічного для панелі помічника.
type signal struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	WardID       string `json:"wardId"`
	WardName     string `json:"wardName"`
	Title        string `json:"title"`
	Message      string `json:"message"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	OccurrenceID string `json:"occurrenceID,omitempty"`
	TakenBy      string `json:"takenBy,omitempty"`
	Acknowledged bool   `json:"acknowledged"`
}

// listSignals збирає всі відкриті сигнали помічника: допомогу, «Я не пам'ятаю» та ескалації.
//
// Панель помічника показує їх постійно, тому запит не прив'язаний до одного підопічного.
func (s *Server) listSignals(w http.ResponseWriter, r *http.Request) {
	helper := helperOf(r)
	wardFilter := r.URL.Query().Get("wardId")
	if wardFilter != "" && !s.requireWard(w, r, wardFilter, permView) {
		return
	}
	out := []signal{}

	help, err := s.collectHelpSignals(r.Context(), helper.ID, wardFilter)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати запити допомоги.", err)
		return
	}
	out = append(out, help...)

	unsure, err := s.collectUnsureSignals(r.Context(), helper.ID, wardFilter)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати питання підопічного.", err)
		return
	}
	out = append(out, unsure...)

	escalations, err := s.collectEscalationSignals(r.Context(), helper.ID, wardFilter)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати ескалації.", err)
		return
	}
	out = append(out, escalations...)

	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) collectHelpSignals(ctx context.Context, helperID, wardFilter string) ([]signal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT hr.id,hr.ward_id,w.address_name,COALESCE(hr.message,''),hr.status,hr.created_at,
		       (SELECT COUNT(*) FROM help_acknowledgements ha WHERE ha.request_id=hr.id AND ha.helper_id=?)
		FROM help_requests hr
		JOIN wards w ON w.id=hr.ward_id
		JOIN care_links cl ON cl.ward_id=hr.ward_id AND cl.helper_id=? AND cl.revoked_at IS NULL
		WHERE hr.status='open' AND (?='' OR hr.ward_id=?)
		ORDER BY hr.created_at DESC`, helperID, helperID, wardFilter, wardFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []signal{}
	for rows.Next() {
		var item signal
		var acknowledged int
		if err := rows.Scan(&item.ID, &item.WardID, &item.WardName, &item.Message,
			&item.Status, &item.CreatedAt, &acknowledged); err != nil {
			return nil, err
		}
		item.Type = EventHelp
		item.Title = "Потрібна допомога"
		item.Acknowledged = acknowledged > 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) collectUnsureSignals(ctx context.Context, helperID, wardFilter string) ([]signal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ur.id,ur.ward_id,w.address_name,t.title,ur.status,ur.created_at,ur.occurrence_id
		FROM unsure_requests ur
		JOIN wards w ON w.id=ur.ward_id
		JOIN task_occurrences o ON o.id=ur.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		JOIN care_links cl ON cl.ward_id=ur.ward_id AND cl.helper_id=? AND cl.revoked_at IS NULL
		WHERE ur.status='open' AND (?='' OR ur.ward_id=?)
		ORDER BY ur.created_at DESC`, helperID, wardFilter, wardFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []signal{}
	for rows.Next() {
		var item signal
		var taskTitle string
		if err := rows.Scan(&item.ID, &item.WardID, &item.WardName, &taskTitle,
			&item.Status, &item.CreatedAt, &item.OccurrenceID); err != nil {
			return nil, err
		}
		item.Type = EventUnsure
		item.Title = "«Я не пам'ятаю»"
		item.Message = taskTitle
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) collectEscalationSignals(ctx context.Context, helperID, wardFilter string) ([]signal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id,e.ward_id,w.address_name,t.title,e.status,e.opened_at,e.occurrence_id,COALESCE(h.display_name,'')
		FROM escalations e
		JOIN wards w ON w.id=e.ward_id
		JOIN task_occurrences o ON o.id=e.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		JOIN care_links cl ON cl.ward_id=e.ward_id AND cl.helper_id=? AND cl.revoked_at IS NULL
		LEFT JOIN helpers h ON h.id=e.taken_by
		WHERE e.status<>'closed' AND (?='' OR e.ward_id=?)
		ORDER BY e.opened_at DESC`, helperID, wardFilter, wardFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []signal{}
	for rows.Next() {
		var item signal
		var taskTitle string
		if err := rows.Scan(&item.ID, &item.WardID, &item.WardName, &taskTitle,
			&item.Status, &item.CreatedAt, &item.OccurrenceID, &item.TakenBy); err != nil {
			return nil, err
		}
		item.Type = EventEscalation
		item.Title = "Критичну справу не виконано"
		item.Message = taskTitle
		out = append(out, item)
	}
	return out, rows.Err()
}

// listHelp повертає історію запитів допомоги підопічного.
func (s *Server) listHelp(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT hr.id,COALESCE(hr.message,''),hr.status,hr.created_at,COALESCE(hr.closed_at,''),
		       COALESCE(h.display_name,''),
		       (SELECT COUNT(*) FROM help_acknowledgements ha WHERE ha.request_id=hr.id)
		FROM help_requests hr LEFT JOIN helpers h ON h.id=hr.closed_by
		WHERE hr.ward_id=? ORDER BY hr.created_at DESC LIMIT 100`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати запити.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, message, status, createdAt, closedAt, closedBy string
		var acks int
		if err := rows.Scan(&id, &message, &status, &createdAt, &closedAt, &closedBy, &acks); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати запити.", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "message": message, "status": status, "createdAt": createdAt,
			"closedAt": closedAt, "closedBy": closedBy, "acknowledgements": acks,
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати запити.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// ackHelp позначає, що помічник узяв запит у роботу.
func (s *Server) ackHelp(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	var wardID string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT ward_id FROM help_requests WHERE id=? AND status='open'", requestID).Scan(&wardID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Відкритого запиту не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	helper := helperOf(r)
	if _, err := s.db.ExecContext(r.Context(),
		"INSERT OR IGNORE INTO help_acknowledgements(id,request_id,helper_id,created_at) VALUES(?,?,?,?)",
		security.ID(), requestID, helper.ID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося позначити запит.", err)
		return
	}
	s.publish(r.Context(), wardID, EventHelp, helper.Name+" бере запит у роботу", false, map[string]any{
		"requestID": requestID,
		"takenBy":   helper.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// closeHelp закриває запит про допомогу.
func (s *Server) closeHelp(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	var wardID string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT ward_id FROM help_requests WHERE id=?", requestID).Scan(&wardID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Запит не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	helper := helperOf(r)
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE help_requests SET status='closed',closed_at=?,closed_by=? WHERE id=? AND status='open'",
		s.nowStamp(), helper.ID, requestID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося закрити запит.", err)
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "закрито", "запит допомоги", requestID)
	s.publish(r.Context(), wardID, EventSignalDone, "Запит про допомогу закрито", false, map[string]any{"requestID": requestID})
	w.WriteHeader(http.StatusNoContent)
}

// listUnsure повертає питання «Я не пам'ятаю» підопічного.
func (s *Server) listUnsure(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT u.id,u.occurrence_id,t.title,u.status,COALESCE(u.answer,''),u.created_at,COALESCE(u.answered_at,'')
		FROM unsure_requests u
		JOIN task_occurrences o ON o.id=u.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		WHERE u.ward_id=? ORDER BY u.created_at DESC LIMIT 100`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати питання.", err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, occurrenceID, title, status, answer, createdAt, answeredAt string
		if err := rows.Scan(&id, &occurrenceID, &title, &status, &answer, &createdAt, &answeredAt); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати питання.", err)
			return
		}
		out = append(out, map[string]string{
			"id": id, "occurrenceID": occurrenceID, "title": title, "status": status,
			"answer": answer, "createdAt": createdAt, "answeredAt": answeredAt,
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати питання.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// answerUnsure дає підопічному коротку відповідь і за потреби відмічає справу виконаною.
func (s *Server) answerUnsure(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("id")
	var wardID, occurrenceID string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT ward_id,occurrence_id FROM unsure_requests WHERE id=? AND status='open'", requestID).
		Scan(&wardID, &occurrenceID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Відкритого питання не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permTasks) {
		return
	}
	var in struct {
		Answer     string `json:"answer"`
		MarkAsDone bool   `json:"markAsDone"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	answer := strings.TrimSpace(in.Answer)
	if answer == "" {
		s.writeError(w, http.StatusBadRequest, "Напишіть коротку відповідь для підопічного.")
		return
	}
	helper := helperOf(r)
	if in.MarkAsDone {
		if _, err := s.db.ExecContext(r.Context(), `
			UPDATE task_occurrences SET status='done',completed_at=?,marked_by_helper_id=?
			WHERE id=? AND status='planned'`, s.nowStamp(), helper.ID, occurrenceID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося відмітити справу.", err)
			return
		}
		s.closeEscalationFor(r.Context(), occurrenceID, helper.ID)
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE unsure_requests SET status='answered',answer=?,answered_by=?,answered_at=? WHERE id=?",
		answer, helper.ID, s.nowStamp(), requestID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти відповідь.", err)
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "відповідь", "питання підопічного", requestID)
	s.publish(r.Context(), wardID, EventSignalDone, "Помічник відповів підопічному", false, map[string]any{
		"requestID": requestID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// repeatEscalation повторює сигнал, якщо ескалацію досі ніхто не взяв.
func (s *Server) repeatEscalation(ctx context.Context, wardID, occurrenceID, title string) {
	name := s.wardName(ctx, wardID)
	s.notifyWard(ctx, wardID, notify.Message{
		Kind:     notify.KindEscalation,
		Title:    "Досі ніхто не відгукнувся: " + title,
		Body:     "Профіль: " + name + ". Критичну справу не виконано, і ескалацію ніхто не взяв.",
		URL:      s.cfg.PublicURL + "/?ward=" + wardID + "&tab=signals",
		WardName: name,
		Urgent:   true,
	}, false)
	s.publish(ctx, wardID, EventEscalation, "Досі ніхто не відгукнувся: "+title, true, map[string]any{
		"occurrenceID": occurrenceID,
		"repeated":     true,
	})
}

// escalate створює ескалацію та сповіщає помічників.
func (s *Server) escalate(ctx context.Context, wardID, occurrenceID, title string) {
	name := s.wardName(ctx, wardID)
	s.notifyWard(ctx, wardID, notify.Message{
		Kind:     notify.KindEscalation,
		Title:    "Критичну справу не виконано: " + title,
		Body:     "Профіль: " + name + ". Справу не відмічено вчасно. Перевірте, будь ласка.",
		URL:      s.cfg.PublicURL + "/?ward=" + wardID + "&tab=signals",
		WardName: name,
		Urgent:   true,
	}, false)
	s.publish(ctx, wardID, EventEscalation, "Критичну справу не виконано: "+title, true, map[string]any{
		"occurrenceID": occurrenceID,
	})
}
