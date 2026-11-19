package api

import (
	"net/http"
	"strings"
)

// listEscalations повертає ескалації підопічного.
func (s *Server) listEscalations(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permEscalations) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT e.id,e.occurrence_id,t.title,e.status,e.opened_at,COALESCE(e.closed_at,''),
		       COALESCE(h.display_name,''),COALESCE(e.comment,'')
		FROM escalations e
		JOIN task_occurrences o ON o.id=e.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		LEFT JOIN helpers h ON h.id=e.taken_by
		WHERE e.ward_id=? ORDER BY e.opened_at DESC LIMIT 100`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати ескалації.", err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, occurrenceID, title, status, openedAt, closedAt, takenBy, comment string
		if err := rows.Scan(&id, &occurrenceID, &title, &status, &openedAt, &closedAt, &takenBy, &comment); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати ескалації.", err)
			return
		}
		out = append(out, map[string]string{
			"id": id, "occurrenceID": occurrenceID, "title": title, "status": status,
			"openedAt": openedAt, "closedAt": closedAt, "takenBy": takenBy, "comment": comment,
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати ескалації.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// takeEscalation позначає, що помічник узяв ескалацію на себе.
func (s *Server) takeEscalation(w http.ResponseWriter, r *http.Request) {
	escalationID := r.PathValue("id")
	wardID, err := s.wardOfEscalation(r, escalationID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Ескалацію не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permEscalations) {
		return
	}
	helper := helperOf(r)
	result, err := s.db.ExecContext(r.Context(),
		"UPDATE escalations SET taken_by=?,status='taken',taken_at=? WHERE id=? AND status='open'",
		helper.ID, s.nowStamp(), escalationID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося взяти ескалацію.", err)
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		s.writeError(w, http.StatusConflict, "Ескалацію вже взяв інший помічник.")
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "взято", "ескалація", escalationID)
	s.publish(r.Context(), wardID, EventEscalation, helper.Name+" узяв ескалацію", false, map[string]any{
		"escalationID": escalationID,
		"takenBy":      helper.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// closeEscalation закриває ескалацію з коментарем помічника.
func (s *Server) closeEscalation(w http.ResponseWriter, r *http.Request) {
	escalationID := r.PathValue("id")
	wardID, err := s.wardOfEscalation(r, escalationID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Ескалацію не знайдено.")
		return
	}
	if !s.requireWard(w, r, wardID, permEscalations) {
		return
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	helper := helperOf(r)
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE escalations SET status='closed',comment=?,closed_at=?,taken_by=COALESCE(taken_by,?) WHERE id=?",
		strings.TrimSpace(in.Comment), s.nowStamp(), helper.ID, escalationID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося закрити ескалацію.", err)
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "закрито", "ескалація", escalationID)
	s.publish(r.Context(), wardID, EventSignalDone, "Ескалацію закрито", false, map[string]any{"escalationID": escalationID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wardOfEscalation(r *http.Request, escalationID string) (string, error) {
	var wardID string
	err := s.db.QueryRowContext(r.Context(), "SELECT ward_id FROM escalations WHERE id=?", escalationID).Scan(&wardID)
	return wardID, err
}
