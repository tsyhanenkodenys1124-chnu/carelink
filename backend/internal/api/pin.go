// Режим помічника на пристрої підопічного.
//
// Сюди потрапляє помічник, який стоїть поруч із людиною і не хоче діставати
// власний телефон: відмітити виконане, закрити сигнал, відповісти на питання.
//
// Поверхня API тут навмисно окрема й вузька, а не «пустити PIN-сеанс у панель
// помічника». Причини дві. Перша: пристрій лежить у чужій квартирі, і
// чотиризначний PIN не рівносильний паролю — з нього не можна міняти
// налаштування доступу, запрошувати помічників, підключати інші пристрої чи
// видаляти профіль. Друга: при спільних обробниках кожен новий маршрут
// панелі автоматично ставав би доступним за PIN, і про це легко забути.
// Тут доступне рівно те, що перелічено нижче, і завжди лише один підопічний.

package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"doglyad/backend/internal/security"
)

const pinKey contextKey = "сеанс PIN"

// pinPrincipal описує відкритий за PIN сеанс на пристрої підопічного.
type pinPrincipal struct {
	SessionID string
	WardID    string
	DeviceID  string
}

// pinOf повертає сеанс PIN з контексту запиту.
func pinOf(r *http.Request) pinPrincipal {
	value, _ := r.Context().Value(pinKey).(pinPrincipal)
	return value
}

// pinOnly вимагає дійсний сеанс PIN і продовжує його на час бездіяльності.
//
// Токен надходить заголовком, а не куками, тому підробити запит із чужої
// сторінки не вийде і окрема перевірка CSRF не потрібна.
func (s *Server) pinOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-PIN-Token")
		if token == "" {
			s.writeError(w, http.StatusUnauthorized, "Режим помічника не відкрито.")
			return
		}
		var principal pinPrincipal
		err := s.db.QueryRowContext(r.Context(), `
			SELECT ps.id,ps.ward_id,ps.device_id
			FROM pin_sessions ps
			JOIN devices d ON d.id=ps.device_id
			JOIN wards w ON w.id=ps.ward_id
			WHERE ps.token_hash=? AND ps.revoked_at IS NULL AND ps.expires_at>?
			  AND d.revoked_at IS NULL AND w.deleted_at IS NULL`,
			security.Hash(token), s.nowStamp()).
			Scan(&principal.SessionID, &principal.WardID, &principal.DeviceID)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "Режим помічника завершено. Введіть PIN-код ще раз.")
			return
		}
		// Сеанс живе короткими відрізками й продовжується, поки ним користуються:
		// планшет, залишений на столі, сам закриє доступ через кілька хвилин.
		if _, err := s.db.ExecContext(r.Context(),
			"UPDATE pin_sessions SET last_seen_at=?,expires_at=? WHERE id=?",
			s.nowStamp(), stamp(s.now().Add(pinSessionTTL)), principal.SessionID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося продовжити сеанс.", err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), pinKey, principal)))
	}
}

// pinOverview — усе, що режим показує одним запитом: профіль, справи дня
// та відкриті сигнали саме цього підопічного.
func (s *Server) pinOverview(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)

	var name, timezone string
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT address_name,timezone FROM wards WHERE id=?", session.WardID).Scan(&name, &timezone); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати профіль.", err)
		return
	}

	from := stamp(s.now().Add(-12 * time.Hour))
	to := stamp(s.now().Add(12 * time.Hour))
	occurrences, err := s.occurrencesBetween(r.Context(), session.WardID, from, to)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати справи.", err)
		return
	}
	signals, err := s.pinSignals(r.Context(), session.WardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати сигнали.", err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"ward":        map[string]string{"id": session.WardID, "name": name, "timezone": timezone},
		"occurrences": occurrences,
		"signals":     signals,
	})
}

// pinSignals збирає відкриті сигнали одного підопічного.
//
// Окремо від collect*Signals панелі помічника: там доступ визначають care_links
// конкретного облікового запису, а тут підопічний один і заданий сеансом.
func (s *Server) pinSignals(ctx context.Context, wardID string) ([]signal, error) {
	out := []signal{}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id,COALESCE(message,''),status,created_at FROM help_requests
		WHERE ward_id=? AND status='open' ORDER BY created_at DESC`, wardID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item := signal{Type: EventHelp, WardID: wardID, Title: "Потрібна допомога"}
		if err := rows.Scan(&item.ID, &item.Message, &item.Status, &item.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `
		SELECT ur.id,t.title,ur.status,ur.created_at,ur.occurrence_id
		FROM unsure_requests ur
		JOIN task_occurrences o ON o.id=ur.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		WHERE ur.ward_id=? AND ur.status='open' ORDER BY ur.created_at DESC`, wardID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item := signal{Type: EventUnsure, WardID: wardID, Title: "«Я не пам'ятаю»"}
		if err := rows.Scan(&item.ID, &item.Message, &item.Status, &item.CreatedAt, &item.OccurrenceID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `
		SELECT e.id,t.title,e.status,e.opened_at,e.occurrence_id
		FROM escalations e
		JOIN task_occurrences o ON o.id=e.occurrence_id
		JOIN tasks t ON t.id=o.task_id
		WHERE e.ward_id=? AND e.status<>'closed' ORDER BY e.opened_at DESC`, wardID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item := signal{Type: EventEscalation, WardID: wardID, Title: "Критичну справу не виконано"}
		if err := rows.Scan(&item.ID, &item.Message, &item.Status, &item.CreatedAt, &item.OccurrenceID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	return out, nil
}

// pinMark відмічає виконання за підопічного.
func (s *Server) pinMark(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	occurrenceID := r.PathValue("id")
	if !s.pinOwnsOccurrence(w, r, occurrenceID) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if in.Status != "done" && in.Status != "missed" {
		s.writeError(w, http.StatusBadRequest, "Статус може бути «done» або «missed».")
		return
	}
	completedAt := ""
	if in.Status == "done" {
		completedAt = s.nowStamp()
	}
	// marked_by_helper_id лишається порожнім: обліковий запис тут невідомий,
	// і вигадувати його не можна — у звіті стояло б чуже ім'я.
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE task_occurrences SET status=?,completed_at=?,undo_until=? WHERE id=?`,
		in.Status, nullIfEmpty(completedAt), nullIfEmpty(stamp(s.now().Add(s.cfg.UndoWindow))), occurrenceID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти відмітку.", err)
		return
	}
	if in.Status == "done" {
		s.closeEscalationFor(r.Context(), occurrenceID, "")
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "позначено "+in.Status, "виконання", occurrenceID)
	s.publish(r.Context(), session.WardID, EventOccurrence, "Відмітка з пристрою підопічного", false,
		map[string]any{"occurrenceID": occurrenceID, "status": in.Status})
	w.WriteHeader(http.StatusNoContent)
}

// pinUndo скасовує відмітку, поки діє вікно скасування.
func (s *Server) pinUndo(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	occurrenceID := r.PathValue("id")
	if !s.pinOwnsOccurrence(w, r, occurrenceID) {
		return
	}
	if err := s.undoOccurrence(r.Context(), occurrenceID); err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "скасовано відмітку", "виконання", occurrenceID)
	s.publish(r.Context(), session.WardID, EventOccurrence, "Відмітку скасовано з пристрою", false,
		map[string]any{"occurrenceID": occurrenceID})
	w.WriteHeader(http.StatusNoContent)
}

// pinCloseHelp закриває запит про допомогу: помічник уже поруч.
func (s *Server) pinCloseHelp(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	requestID := r.PathValue("id")
	result, err := s.db.ExecContext(r.Context(),
		"UPDATE help_requests SET status='closed',closed_at=? WHERE id=? AND ward_id=? AND status='open'",
		s.nowStamp(), requestID, session.WardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося закрити запит.", err)
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		s.writeError(w, http.StatusNotFound, "Відкритого запиту не знайдено.")
		return
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "закрито", "запит допомоги", requestID)
	s.publish(r.Context(), session.WardID, EventSignalDone, "Запит про допомогу закрито на пристрої", false,
		map[string]any{"requestID": requestID})
	w.WriteHeader(http.StatusNoContent)
}

// pinAnswerUnsure відповідає на питання «чи я вже приймав».
func (s *Server) pinAnswerUnsure(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	requestID := r.PathValue("id")
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
	var occurrenceID string
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT occurrence_id FROM unsure_requests WHERE id=? AND ward_id=? AND status='open'",
		requestID, session.WardID).Scan(&occurrenceID); err != nil {
		s.writeError(w, http.StatusNotFound, "Відкритого питання не знайдено.")
		return
	}
	if in.MarkAsDone {
		if _, err := s.db.ExecContext(r.Context(),
			"UPDATE task_occurrences SET status='done',completed_at=? WHERE id=? AND status='planned'",
			s.nowStamp(), occurrenceID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося відмітити справу.", err)
			return
		}
		s.closeEscalationFor(r.Context(), occurrenceID, "")
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE unsure_requests SET status='answered',answer=?,answered_at=? WHERE id=?",
		answer, s.nowStamp(), requestID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти відповідь.", err)
		return
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "відповідь", "питання підопічного", requestID)
	s.publish(r.Context(), session.WardID, EventSignalDone, "Відповідь дано на пристрої", false,
		map[string]any{"requestID": requestID})
	w.WriteHeader(http.StatusNoContent)
}

// pinCloseEscalation закриває ескалацію: помічник на місці й розібрався.
func (s *Server) pinCloseEscalation(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	escalationID := r.PathValue("id")
	var in struct {
		Comment string `json:"comment"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	result, err := s.db.ExecContext(r.Context(), `
		UPDATE escalations SET status='closed',comment=?,closed_at=?
		WHERE id=? AND ward_id=? AND status<>'closed'`,
		strings.TrimSpace(in.Comment), s.nowStamp(), escalationID, session.WardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося закрити ескалацію.", err)
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		s.writeError(w, http.StatusNotFound, "Відкритої ескалації не знайдено.")
		return
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "закрито", "ескалація", escalationID)
	s.publish(r.Context(), session.WardID, EventSignalDone, "Ескалацію закрито на пристрої", false,
		map[string]any{"escalationID": escalationID})
	w.WriteHeader(http.StatusNoContent)
}

// pinLogout закриває режим помічника негайно, не чекаючи кінця сеансу.
func (s *Server) pinLogout(w http.ResponseWriter, r *http.Request) {
	session := pinOf(r)
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE pin_sessions SET revoked_at=? WHERE id=?", s.nowStamp(), session.SessionID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося закрити режим помічника.", err)
		return
	}
	s.auditAs(r.Context(), session.WardID, "", actorPIN, "вихід з режиму PIN", "пристрій", session.DeviceID)
	w.WriteHeader(http.StatusNoContent)
}

// pinOwnsOccurrence перевіряє, що виконання належить підопічному цього сеансу.
func (s *Server) pinOwnsOccurrence(w http.ResponseWriter, r *http.Request, occurrenceID string) bool {
	var wardID string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT ward_id FROM task_occurrences WHERE id=?", occurrenceID).Scan(&wardID)
	if err != nil || wardID != pinOf(r).WardID {
		s.writeError(w, http.StatusNotFound, "Справу не знайдено.")
		return false
	}
	return true
}
