package api

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/skip2/go-qrcode"

	"doglyad/backend/internal/security"
)

// pairingCodeAttempts обмежує кількість спроб підібрати вільний код.
const pairingCodeAttempts = 10

// listDevices показує пристрої, підключені до профілю підопічного.
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id,label,COALESCE(last_active_at,''),created_at,COALESCE(revoked_at,'')
		FROM devices WHERE ward_id=? ORDER BY created_at DESC`, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати пристрої.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, label, lastActive, createdAt, revokedAt string
		if err := rows.Scan(&id, &label, &lastActive, &createdAt, &revokedAt); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати пристрої.", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "label": label, "lastActiveAt": lastActive,
			"createdAt": createdAt, "revokedAt": revokedAt, "active": revokedAt == "",
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати пристрої.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// createPairing створює одноразовий код підключення та QR-код до нього.
func (s *Server) createPairing(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	helper := helperOf(r)
	expiresAt := stamp(s.now().Add(s.cfg.PairingTTL))

	// Код випадковий, тому можливий збіг із чинним кодом іншого профілю: пробуємо ще раз.
	var code string
	var lastErr error
	for attempt := 0; attempt < pairingCodeAttempts; attempt++ {
		candidate := security.NumericCode(6)
		_, err := s.db.ExecContext(r.Context(), `
			INSERT INTO pairing_codes(id,ward_id,code,expires_at,created_by,created_at)
			VALUES(?,?,?,?,?,?)`,
			security.ID(), wardID, candidate, expiresAt, helper.ID, s.nowStamp())
		if err == nil {
			code = candidate
			break
		}
		lastErr = err
	}
	if code == "" {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити код підключення.", lastErr)
		return
	}

	pairingURL := s.cfg.PublicURL + "/pair?code=" + code
	png, err := qrcode.Encode(pairingURL, qrcode.Medium, 512)
	if err != nil {
		s.log.Warn("Не вдалося створити QR-код", "помилка", err)
	}
	response := map[string]string{
		"code":       code,
		"expiresAt":  expiresAt,
		"pairingURL": pairingURL,
	}
	if len(png) > 0 {
		response["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	s.audit(r.Context(), wardID, helper.ID, "створено", "код підключення", wardID)
	s.writeJSON(w, http.StatusCreated, response)
}

// revokeDevice відключає пристрій підопічного.
func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	wardID, deviceID := r.PathValue("id"), r.PathValue("deviceID")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE devices SET revoked_at=? WHERE id=? AND ward_id=? AND revoked_at IS NULL",
		s.nowStamp(), deviceID, wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося відключити пристрій.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "відключено", "пристрій", deviceID)
	w.WriteHeader(http.StatusNoContent)
}

// pairDevice обмінює одноразовий код на постійний токен пристрою.
func (s *Server) pairDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code  string `json:"code"`
		Label string `json:"label"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	code := strings.TrimSpace(in.Code)
	if len(code) != 6 {
		s.writeError(w, http.StatusBadRequest, "Введіть шестизначний код.")
		return
	}
	address := clientIP(r)
	if s.throttle(r.Context(), "pairing-ip", address) {
		s.writeError(w, http.StatusTooManyRequests, "Забагато спроб. Спробуйте за 15 хвилин.")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підключити пристрій.", err)
		return
	}
	defer tx.Rollback()

	var pairingID, wardID, expiresAt string
	err = tx.QueryRow(
		"SELECT id,ward_id,expires_at FROM pairing_codes WHERE code=? AND used_at IS NULL", code).
		Scan(&pairingID, &wardID, &expiresAt)
	if err != nil || expiresAt < s.nowStamp() {
		s.registerFailure(r.Context(), "pairing-ip", address, 10, loginLockDuration)
		s.writeError(w, http.StatusBadRequest, "Код недійсний або вже завершився.")
		return
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = "Пристрій підопічного"
	}
	deviceToken := security.Token()
	if _, err := tx.Exec(
		"INSERT INTO devices(id,ward_id,token_hash,label,created_at,last_active_at) VALUES(?,?,?,?,?,?)",
		security.ID(), wardID, security.Hash(deviceToken), label, s.nowStamp(), s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підключити пристрій.", err)
		return
	}
	if _, err := tx.Exec("UPDATE pairing_codes SET used_at=? WHERE id=?", s.nowStamp(), pairingID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося завершити підключення.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підключити пристрій.", err)
		return
	}
	s.clearFailures(r.Context(), "pairing-ip", address)
	s.audit(r.Context(), wardID, "", "підключено", "пристрій", label)
	s.writeJSON(w, http.StatusCreated, map[string]string{"deviceToken": deviceToken, "wardID": wardID})
}
