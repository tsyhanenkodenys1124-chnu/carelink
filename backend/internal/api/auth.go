package api

import (
	"context"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

const (
	sessionCookie     = "doglyad_session"
	minPasswordLength = 10
	loginAttemptLimit = 5
	loginLockDuration = 15 * time.Minute
	emailTokenTTL     = 24 * time.Hour
	resetTokenTTL     = time.Hour
)

// Види одноразових кодів у таблиці email_tokens.
const (
	tokenKindConfirm = "confirm"
	tokenKindReset   = "reset"
)

// register створює обліковий запис помічника та надсилає лист підтвердження.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	email := normalizeEmail(in.Email)
	if !validEmail(email) {
		s.writeError(w, http.StatusBadRequest, "Вкажіть коректну електронну адресу.")
		return
	}
	if len([]rune(in.Password)) < minPasswordLength {
		s.writeError(w, http.StatusBadRequest, "Пароль має містити щонайменше 10 символів.")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Вкажіть ваше ім'я.")
		return
	}
	timezone := strings.TrimSpace(in.Timezone)
	if timezone == "" {
		timezone = "Europe/Kyiv"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		s.writeError(w, http.StatusBadRequest, "Невідомий часовий пояс.")
		return
	}

	helperID := security.ID()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO helpers(id,email,password_hash,display_name,timezone,created_at)
		VALUES(?,?,?,?,?,?)`,
		helperID, email, security.HashPassword(in.Password), name, timezone, s.nowStamp())
	if err != nil {
		// Єдине обмеження на адресу — найімовірніша причина.
		s.writeError(w, http.StatusConflict, "Ця електронна адреса вже використовується.")
		return
	}
	s.sendConfirmation(r.Context(), helperID, email, name)
	if err := s.startSession(r.Context(), w, helperID, r.UserAgent()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити сеанс.", err)
		return
	}
	s.audit(r.Context(), "", helperID, "створено", "обліковий запис", helperID)
	s.writeJSON(w, http.StatusCreated, map[string]string{
		"id":      helperID,
		"message": "Обліковий запис створено. Ми надіслали лист для підтвердження адреси.",
	})
}

// login перевіряє пароль із захистом від перебору.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	email := normalizeEmail(in.Email)
	address := clientIP(r)
	if s.throttle(r.Context(), "login-email", email) || s.throttle(r.Context(), "login-ip", address) {
		s.writeError(w, http.StatusTooManyRequests, "Забагато спроб входу. Спробуйте за 15 хвилин.")
		return
	}

	var helperID, hash string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT id,password_hash FROM helpers WHERE email=? AND deleted_at IS NULL", email).Scan(&helperID, &hash)
	if err != nil || !security.CheckPassword(hash, in.Password) {
		s.registerFailure(r.Context(), "login-email", email, loginAttemptLimit, loginLockDuration)
		s.registerFailure(r.Context(), "login-ip", address, loginAttemptLimit*4, loginLockDuration)
		s.writeError(w, http.StatusUnauthorized, "Неправильна адреса або пароль.")
		return
	}
	s.clearFailures(r.Context(), "login-email", email)
	s.clearFailures(r.Context(), "login-ip", address)
	if err := s.startSession(r.Context(), w, helperID, r.UserAgent()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити сеанс.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Вхід виконано."})
}

// logout завершує поточний сеанс.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.db.ExecContext(r.Context(),
			"UPDATE sessions SET revoked_at=? WHERE token_hash=?", s.nowStamp(), security.Hash(cookie.Value)); err != nil {
			s.log.Warn("Не вдалося завершити сеанс", "помилка", err)
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// requestPasswordReset надсилає лист із одноразовим кодом відновлення.
//
// Відповідь однакова для наявної та відсутньої адреси, щоб не розкривати базу користувачів.
func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	email := normalizeEmail(in.Email)
	answer := map[string]string{"message": "Якщо адреса зареєстрована, ми надіслали інструкції."}

	var helperID, name string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT id,display_name FROM helpers WHERE email=? AND deleted_at IS NULL", email).Scan(&helperID, &name)
	if err != nil {
		s.writeJSON(w, http.StatusAccepted, answer)
		return
	}
	token, err := s.issueEmailToken(r.Context(), helperID, tokenKindReset, resetTokenTTL)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити код відновлення.", err)
		return
	}
	s.enqueue(r.Context(), helperID, "", notify.ChannelEmail, notify.KindResetMail, notify.Message{
		Kind:  notify.KindResetMail,
		Title: "Відновлення пароля в застосунку «Догляд»",
		Body: name + ", ви попросили відновити пароль. Посилання діє одну годину.\n" +
			"Якщо це були не ви, просто проігноруйте лист — пароль залишиться незмінним.",
		URL: s.cfg.PublicURL + "/reset?token=" + token,
	})
	s.writeJSON(w, http.StatusAccepted, answer)
}

// confirmPasswordReset встановлює новий пароль за одноразовим кодом.
func (s *Server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if len([]rune(in.Password)) < minPasswordLength {
		s.writeError(w, http.StatusBadRequest, "Пароль має містити щонайменше 10 символів.")
		return
	}
	helperID, ok := s.consumeEmailToken(r.Context(), in.Token, tokenKindReset)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Посилання недійсне або вже використане.")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE helpers SET password_hash=? WHERE id=?", security.HashPassword(in.Password), helperID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти пароль.", err)
		return
	}
	// Зміна пароля завершує всі відкриті сеанси.
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE sessions SET revoked_at=? WHERE helper_id=? AND revoked_at IS NULL", s.nowStamp(), helperID); err != nil {
		s.log.Warn("Не вдалося завершити сеанси після зміни пароля", "помилка", err)
	}
	s.audit(r.Context(), "", helperID, "змінено", "пароль", helperID)
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Пароль змінено. Увійдіть з новим паролем."})
}

// confirmEmail підтверджує адресу за кодом із листа.
func (s *Server) confirmEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	helperID, ok := s.consumeEmailToken(r.Context(), in.Token, tokenKindConfirm)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "Посилання недійсне або вже використане.")
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE helpers SET email_confirmed_at=? WHERE id=? AND email_confirmed_at IS NULL", s.nowStamp(), helperID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підтвердити адресу.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Адресу підтверджено."})
}

// resendConfirmation повторно надсилає лист підтвердження.
func (s *Server) resendConfirmation(w http.ResponseWriter, r *http.Request) {
	helper := helperOf(r)
	if helper.Confirmed {
		s.writeJSON(w, http.StatusOK, map[string]string{"message": "Адресу вже підтверджено."})
		return
	}
	s.sendConfirmation(r.Context(), helper.ID, helper.Email, helper.Name)
	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "Лист надіслано ще раз."})
}

func (s *Server) sendConfirmation(ctx context.Context, helperID, email, name string) {
	token, err := s.issueEmailToken(ctx, helperID, tokenKindConfirm, emailTokenTTL)
	if err != nil {
		s.logError("Не вдалося створити код підтвердження", err)
		return
	}
	s.enqueue(ctx, helperID, "", notify.ChannelEmail, notify.KindConfirmMail, notify.Message{
		Kind:  notify.KindConfirmMail,
		Title: "Підтвердьте адресу в застосунку «Догляд»",
		Body:  name + ", підтвердьте адресу " + email + ", щоб отримувати нагадування та сигнали про допомогу.",
		URL:   s.cfg.PublicURL + "/confirm?token=" + token,
	})
}

// issueEmailToken створює одноразовий код і зберігає лише його хеш.
func (s *Server) issueEmailToken(ctx context.Context, helperID, kind string, ttl time.Duration) (string, error) {
	token := security.Token()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO email_tokens(id,helper_id,kind,token_hash,expires_at,created_at)
		VALUES(?,?,?,?,?,?)`,
		security.ID(), helperID, kind, security.Hash(token), stamp(s.now().Add(ttl)), s.nowStamp())
	if err != nil {
		return "", err
	}
	return token, nil
}

// consumeEmailToken перевіряє код і одразу позначає його використаним.
func (s *Server) consumeEmailToken(ctx context.Context, token, kind string) (string, bool) {
	if token == "" {
		return "", false
	}
	var tokenID, helperID, expires string
	err := s.db.QueryRowContext(ctx, `
		SELECT id,helper_id,expires_at FROM email_tokens
		WHERE token_hash=? AND kind=? AND used_at IS NULL`,
		security.Hash(token), kind).Scan(&tokenID, &helperID, &expires)
	if err != nil || expires < s.nowStamp() {
		return "", false
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE email_tokens SET used_at=? WHERE id=? AND used_at IS NULL", s.nowStamp(), tokenID)
	if err != nil {
		s.logError("Не вдалося позначити код використаним", err)
		return "", false
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return "", false
	}
	return helperID, true
}

// startSession створює сеанс і встановлює cookie.
func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, helperID, userAgent string) error {
	token, csrf := security.Token(), security.Token()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(id,helper_id,token_hash,csrf_token,expires_at,created_at,last_seen_at,user_agent)
		VALUES(?,?,?,?,?,?,?,?)`,
		security.ID(), helperID, security.Hash(token), csrf,
		stamp(s.now().Add(s.cfg.SessionTTL)), s.nowStamp(), s.nowStamp(), userAgent)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validEmail(value string) bool {
	if value == "" || len(value) > 254 {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}
