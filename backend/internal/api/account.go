package api

import (
	"net/http"
	"strings"
	"time"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

// me повертає профіль помічника та токен CSRF для наступних запитів.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	helper := helperOf(r)
	var phone, confirmed string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT COALESCE(phone,''),COALESCE(email_confirmed_at,'') FROM helpers WHERE id=?",
		helper.ID).Scan(&phone, &confirmed)
	if err != nil {
		s.fail(w, http.StatusNotFound, "Обліковий запис не знайдено.", err)
		return
	}
	var telegramLinked int
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COUNT(*) FROM telegram_links WHERE helper_id=? AND chat_id IS NOT NULL AND revoked_at IS NULL",
		helper.ID).Scan(&telegramLinked); err != nil {
		s.log.Warn("Не вдалося перевірити Telegram", "помилка", err)
	}
	var pushCount int
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COUNT(*) FROM push_subscriptions WHERE helper_id=? AND revoked_at IS NULL",
		helper.ID).Scan(&pushCount); err != nil {
		s.log.Warn("Не вдалося перевірити підписки push", "помилка", err)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"id":             helper.ID,
		"email":          helper.Email,
		"name":           helper.Name,
		"timezone":       helper.Timezone,
		"phone":          phone,
		"emailConfirmed": confirmed != "",
		"telegramLinked": telegramLinked > 0,
		"pushDevices":    pushCount,
		"csrfToken":      helper.CSRF,
	})
}

// updateMe змінює ім'я, часовий пояс і телефон помічника.
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Phone    string `json:"phone"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Ім'я є обов'язковим.")
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
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE helpers SET display_name=?,timezone=?,phone=? WHERE id=?",
		name, timezone, strings.TrimSpace(in.Phone), helperOf(r).ID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти профіль.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listSessions показує відкриті сеанси помічника.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id,created_at,last_seen_at,COALESCE(user_agent,'') FROM sessions
		WHERE helper_id=? AND revoked_at IS NULL AND expires_at>? ORDER BY last_seen_at DESC`,
		helperOf(r).ID, s.nowStamp())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати сеанси.", err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, created, lastSeen, agent string
		if err := rows.Scan(&id, &created, &lastSeen, &agent); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати сеанси.", err)
			return
		}
		out = append(out, map[string]string{"id": id, "createdAt": created, "lastSeenAt": lastSeen, "userAgent": agent})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати сеанси.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// revokeSession завершує вибраний сеанс.
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE sessions SET revoked_at=? WHERE id=? AND helper_id=?",
		s.nowStamp(), r.PathValue("id"), helperOf(r).ID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося завершити сеанс.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listNotificationPrefs повертає налаштування каналів сповіщень.
func (s *Server) listNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	prefs := s.prefsFor(r.Context(), helperOf(r).ID, "")
	out := make([]channelPref, 0, len(prefs))
	for _, channel := range []string{notify.ChannelPush, notify.ChannelTelegram, notify.ChannelEmail} {
		out = append(out, prefs[channel])
	}
	s.writeJSON(w, http.StatusOK, out)
}

// putNotificationPrefs зберігає налаштування каналів і тихих годин.
func (s *Server) putNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	var in []channelPref
	if !s.readJSON(w, r, &in) {
		return
	}
	helperID := helperOf(r).ID
	for _, pref := range in {
		switch pref.Channel {
		case notify.ChannelPush, notify.ChannelTelegram, notify.ChannelEmail:
		default:
			s.writeError(w, http.StatusBadRequest, "Невідомий канал сповіщень.")
			return
		}
		if !validClock(pref.QuietStart) || !validClock(pref.QuietEnd) {
			s.writeError(w, http.StatusBadRequest, "Тихі години вказуються у форматі 22:00.")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), `
			INSERT INTO notification_prefs(id,helper_id,ward_id,channel,enabled,quiet_start,quiet_end)
			VALUES(?,?,NULL,?,?,?,?)
			ON CONFLICT(helper_id,ward_id,channel) DO UPDATE SET
				enabled=excluded.enabled,quiet_start=excluded.quiet_start,quiet_end=excluded.quiet_end`,
			security.ID(), helperID, pref.Channel, boolInt(pref.Enabled),
			nullIfEmpty(pref.QuietStart), nullIfEmpty(pref.QuietEnd)); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти налаштування.", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// pushPublicKey віддає відкритий ключ VAPID для підписки в браузері.
func (s *Server) pushPublicKey(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"publicKey": s.cfg.Push.PublicKey,
		"enabled":   s.cfg.Push.Enabled(),
	})
}

// subscribePush зберігає підписку браузера на push-сповіщення.
func (s *Server) subscribePush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256DH string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if in.Endpoint == "" || in.Keys.P256DH == "" || in.Keys.Auth == "" {
		s.writeError(w, http.StatusBadRequest, "Підписка неповна.")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO push_subscriptions(id,helper_id,endpoint,p256dh,auth,created_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET
			helper_id=excluded.helper_id,p256dh=excluded.p256dh,auth=excluded.auth,revoked_at=NULL`,
		security.ID(), helperOf(r).ID, in.Endpoint, in.Keys.P256DH, in.Keys.Auth, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти підписку.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unsubscribePush відкликає підписку браузера.
func (s *Server) unsubscribePush(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE push_subscriptions SET revoked_at=? WHERE endpoint=? AND helper_id=?",
		s.nowStamp(), in.Endpoint, helperOf(r).ID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося відкликати підписку.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createTelegramLink створює одноразове посилання для підключення бота.
func (s *Server) createTelegramLink(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Telegram.Enabled() || s.cfg.Telegram.BotName == "" {
		s.writeError(w, http.StatusServiceUnavailable, "Telegram-бот не налаштований на цьому сервері.")
		return
	}
	helperID := helperOf(r).ID
	code := security.Token()
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO telegram_links(id,helper_id,link_code_hash,expires_at,created_at) VALUES(?,?,?,?,?)`,
		security.ID(), helperID, security.Hash(code), stamp(s.now().Add(time.Hour)), s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити посилання.", err)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]string{
		"url":     "https://t.me/" + s.cfg.Telegram.BotName + "?start=" + code,
		"message": "Відкрийте посилання і натисніть «Почати». Воно діє одну годину.",
	})
}

// revokeTelegramLink від'єднує чат Telegram від облікового запису.
func (s *Server) revokeTelegramLink(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE telegram_links SET revoked_at=? WHERE helper_id=? AND revoked_at IS NULL",
		s.nowStamp(), helperOf(r).ID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося від'єднати Telegram.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validClock перевіряє час у форматі 15:04.
func validClock(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}
