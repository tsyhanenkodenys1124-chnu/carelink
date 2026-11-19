package api

import (
	"net/http"
	"strings"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

// listLinks повертає помічників, підключених до підопічного.
func (s *Server) listLinks(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT cl.id,h.id,h.display_name,h.email,cl.is_primary,cl.can_edit_tasks,cl.can_edit_settings,
		       cl.can_escalations,cl.created_at
		FROM care_links cl JOIN helpers h ON h.id=cl.helper_id
		WHERE cl.ward_id=? AND cl.revoked_at IS NULL
		ORDER BY cl.is_primary DESC,h.display_name`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати помічників.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var linkID, helperID, name, email, createdAt string
		var primary, tasks, settings, escalations int
		if err := rows.Scan(&linkID, &helperID, &name, &email, &primary, &tasks, &settings, &escalations, &createdAt); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати помічників.", err)
			return
		}
		out = append(out, map[string]any{
			"id": linkID, "helperID": helperID, "name": name, "email": email, "createdAt": createdAt,
			"rights": Rights{true, tasks == 1, settings == 1, escalations == 1, primary == 1},
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати помічників.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// createInvitation створює одноразове запрошення та надсилає його поштою.
func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	var in struct {
		Email        string `json:"email"`
		EditTasks    bool   `json:"editTasks"`
		EditSettings bool   `json:"editSettings"`
		Escalations  bool   `json:"escalations"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	email := normalizeEmail(in.Email)
	if email != "" && !validEmail(email) {
		s.writeError(w, http.StatusBadRequest, "Вкажіть коректну електронну адресу або залиште поле порожнім.")
		return
	}
	helper := helperOf(r)
	token := security.Token()
	expiresAt := stamp(s.now().Add(s.cfg.InvitationTTL))
	invitationID := security.ID()
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO invitations(id,ward_id,email,token_hash,can_edit_tasks,can_edit_settings,can_escalations,
			expires_at,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		invitationID, wardID, nullIfEmpty(email), security.Hash(token),
		boolInt(in.EditTasks), boolInt(in.EditSettings), boolInt(in.Escalations),
		expiresAt, helper.ID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити запрошення.", err)
		return
	}

	link := s.cfg.PublicURL + "/invite?token=" + token
	name := s.wardName(r.Context(), wardID)
	if email != "" {
		// Якщо адресат уже має обліковий запис, лист піде саме йому.
		var inviteeID string
		if err := s.db.QueryRowContext(r.Context(),
			"SELECT id FROM helpers WHERE email=? AND deleted_at IS NULL", email).Scan(&inviteeID); err == nil {
			s.enqueue(r.Context(), inviteeID, wardID, notify.ChannelEmail, notify.KindInvitation, notify.Message{
				Kind:  notify.KindInvitation,
				Title: "Вас запрошено допомагати: " + name,
				Body:  helper.Name + " запрошує вас до профілю «" + name + "» у застосунку «Догляд». Посилання діє 72 години.",
				URL:   link,
			})
		}
	}
	s.audit(r.Context(), wardID, helper.ID, "створено", "запрошення", invitationID)
	s.writeJSON(w, http.StatusCreated, map[string]string{
		"id":        invitationID,
		"token":     token,
		"url":       link,
		"expiresAt": expiresAt,
	})
}

// listInvitations показує ще не використані запрошення.
func (s *Server) listInvitations(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id,COALESCE(email,''),expires_at,created_at,can_edit_tasks,can_edit_settings,can_escalations
		FROM invitations
		WHERE ward_id=? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>?
		ORDER BY created_at DESC`, wardID, s.nowStamp())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати запрошення.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, expiresAt, createdAt string
		var tasks, settings, escalations int
		if err := rows.Scan(&id, &email, &expiresAt, &createdAt, &tasks, &settings, &escalations); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати запрошення.", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "email": email, "expiresAt": expiresAt, "createdAt": createdAt,
			"rights": Rights{true, tasks == 1, settings == 1, escalations == 1, false},
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати запрошення.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// previewInvitation показує, до якого профілю веде посилання, ще до входу.
func (s *Server) previewInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var wardName, email, expiresAt string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT w.address_name,COALESCE(i.email,''),i.expires_at
		FROM invitations i JOIN wards w ON w.id=i.ward_id
		WHERE i.token_hash=? AND i.accepted_at IS NULL AND i.revoked_at IS NULL`,
		security.Hash(token)).Scan(&wardName, &email, &expiresAt)
	if err != nil || expiresAt < s.nowStamp() {
		s.writeError(w, http.StatusNotFound, "Запрошення недійсне або вже завершилося.")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"wardName":  wardName,
		"email":     email,
		"expiresAt": expiresAt,
	})
}

// acceptInvitation підключає помічника до профілю підопічного.
func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Token) == "" {
		s.writeError(w, http.StatusBadRequest, "Вкажіть код запрошення.")
		return
	}
	helper := helperOf(r)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прийняти запрошення.", err)
		return
	}
	defer tx.Rollback()

	var invitationID, wardID, email, expiresAt string
	var tasks, settings, escalations int
	err = tx.QueryRow(`
		SELECT id,ward_id,COALESCE(email,''),expires_at,can_edit_tasks,can_edit_settings,can_escalations
		FROM invitations WHERE token_hash=? AND accepted_at IS NULL AND revoked_at IS NULL`,
		security.Hash(in.Token)).Scan(&invitationID, &wardID, &email, &expiresAt, &tasks, &settings, &escalations)
	if err != nil || expiresAt < s.nowStamp() {
		s.writeError(w, http.StatusBadRequest, "Запрошення недійсне або вже завершилося.")
		return
	}
	// Іменне запрошення приймає лише той, кому воно адресоване.
	if email != "" && !strings.EqualFold(email, helper.Email) {
		s.writeError(w, http.StatusForbidden, "Це запрошення призначено для іншої адреси.")
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO care_links(id,ward_id,helper_id,can_view,can_edit_tasks,can_edit_settings,can_escalations,created_at)
		VALUES(?,?,?,1,?,?,?,?)
		ON CONFLICT(ward_id,helper_id) DO UPDATE SET revoked_at=NULL,can_view=1,
			can_edit_tasks=excluded.can_edit_tasks,can_edit_settings=excluded.can_edit_settings,
			can_escalations=excluded.can_escalations`,
		security.ID(), wardID, helper.ID, tasks, settings, escalations, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підключити помічника.", err)
		return
	}
	if _, err := tx.Exec("UPDATE invitations SET accepted_at=? WHERE id=?", s.nowStamp(), invitationID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося завершити запрошення.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прийняти запрошення.", err)
		return
	}
	s.audit(r.Context(), wardID, helper.ID, "прийнято", "запрошення", invitationID)
	s.writeJSON(w, http.StatusOK, map[string]string{"wardID": wardID})
}

// updateLink змінює права помічника або передає роль головного.
func (s *Server) updateLink(w http.ResponseWriter, r *http.Request) {
	wardID, linkID := r.PathValue("id"), r.PathValue("linkID")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	var in struct {
		EditTasks    bool `json:"editTasks"`
		EditSettings bool `json:"editSettings"`
		Escalations  bool `json:"escalations"`
		MakePrimary  bool `json:"makePrimary"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося оновити права.", err)
		return
	}
	defer tx.Rollback()
	if in.MakePrimary {
		// Головний помічник у профілі лише один.
		if _, err := tx.Exec("UPDATE care_links SET is_primary=0 WHERE ward_id=?", wardID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося передати роль.", err)
			return
		}
		if _, err := tx.Exec(
			"UPDATE care_links SET is_primary=1,can_edit_tasks=1,can_edit_settings=1,can_escalations=1 WHERE id=? AND ward_id=?",
			linkID, wardID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося передати роль.", err)
			return
		}
	} else {
		if _, err := tx.Exec(`
			UPDATE care_links SET can_edit_tasks=?,can_edit_settings=?,can_escalations=?
			WHERE id=? AND ward_id=? AND revoked_at IS NULL AND is_primary=0`,
			boolInt(in.EditTasks), boolInt(in.EditSettings), boolInt(in.Escalations), linkID, wardID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося оновити права.", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося оновити права.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "змінено права", "помічник", linkID)
	w.WriteHeader(http.StatusNoContent)
}

// revokeLink відключає помічника від профілю.
func (s *Server) revokeLink(w http.ResponseWriter, r *http.Request) {
	wardID, linkID := r.PathValue("id"), r.PathValue("linkID")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	var primary int
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT is_primary FROM care_links WHERE id=? AND ward_id=?", linkID, wardID).Scan(&primary); err != nil {
		s.writeError(w, http.StatusNotFound, "Помічника не знайдено.")
		return
	}
	if primary == 1 {
		s.writeError(w, http.StatusBadRequest, "Спершу передайте роль головного помічника іншій людині.")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE care_links SET revoked_at=? WHERE id=? AND ward_id=?", s.nowStamp(), linkID, wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося відкликати доступ.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "відкликано доступ", "помічник", linkID)
	w.WriteHeader(http.StatusNoContent)
}

// revokeInvitation скасовує ще не використане запрошення.
func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	wardID, invitationID := r.PathValue("id"), r.PathValue("invitationID")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE invitations SET revoked_at=? WHERE id=? AND ward_id=? AND accepted_at IS NULL",
		s.nowStamp(), invitationID, wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося скасувати запрошення.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "скасовано", "запрошення", invitationID)
	w.WriteHeader(http.StatusNoContent)
}
