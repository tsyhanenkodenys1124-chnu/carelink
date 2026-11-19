package api

import (
	"context"
	"net/http"
	"runtime/debug"
	"time"

	"doglyad/backend/internal/security"
)

type contextKey string

const (
	helperKey contextKey = "помічник"
	deviceKey contextKey = "пристрій"
)

// helperPrincipal описує помічника, який виконує запит.
type helperPrincipal struct {
	ID        string
	Name      string
	Email     string
	Timezone  string
	CSRF      string
	Confirmed bool
}

// devicePrincipal описує підключений пристрій підопічного.
type devicePrincipal struct {
	ID     string
	WardID string
}

// cors дозволяє звертатися до API з адреси Vite під час розробки.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin == s.cfg.WebOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Device-Token, X-PIN-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recovery не дає панікам зупинити сервер.
func (s *Server) recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if reason := recover(); reason != nil {
				s.log.Error("Паніка під час обробки запиту",
					"шлях", r.URL.Path, "причина", reason, "стек", string(debug.Stack()))
				s.writeError(w, http.StatusInternalServerError, "Сталася внутрішня помилка.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// helperOnly вимагає дійсну сесію помічника та перевіряє токен CSRF для змін.
func (s *Server) helperOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "Увійдіть до облікового запису.")
			return
		}
		var principal helperPrincipal
		var confirmedAt, sessionID string
		err = s.db.QueryRowContext(r.Context(), `
			SELECT s.id,h.id,h.display_name,h.email,h.timezone,s.csrf_token,COALESCE(h.email_confirmed_at,'')
			FROM sessions s JOIN helpers h ON h.id=s.helper_id
			WHERE s.token_hash=? AND s.expires_at>? AND s.revoked_at IS NULL AND h.deleted_at IS NULL`,
			security.Hash(cookie.Value), s.nowStamp()).
			Scan(&sessionID, &principal.ID, &principal.Name, &principal.Email, &principal.Timezone, &principal.CSRF, &confirmedAt)
		if err != nil {
			s.clearSessionCookie(w)
			s.writeError(w, http.StatusUnauthorized, "Сеанс завершено. Увійдіть знову.")
			return
		}
		principal.Confirmed = confirmedAt != ""

		if changesState(r.Method) && r.Header.Get("X-CSRF-Token") != principal.CSRF {
			s.writeError(w, http.StatusForbidden, "Перевірка безпеки не пройдена. Оновіть сторінку.")
			return
		}
		// Відмітка активності потрібна для списку сеансів; помилка тут не має зривати запит.
		if _, err := s.db.ExecContext(r.Context(), "UPDATE sessions SET last_seen_at=? WHERE id=?", s.nowStamp(), sessionID); err != nil {
			s.log.Warn("Не вдалося оновити активність сеансу", "помилка", err)
		}
		next(w, r.WithContext(context.WithValue(r.Context(), helperKey, principal)))
	}
}

// deviceOnly вимагає токен пристрою підопічного.
func (s *Server) deviceOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Device-Token")
		if token == "" {
			s.writeError(w, http.StatusUnauthorized, "Пристрій не підключено.")
			return
		}
		var principal devicePrincipal
		err := s.db.QueryRowContext(r.Context(),
			"SELECT id,ward_id FROM devices WHERE token_hash=? AND revoked_at IS NULL",
			security.Hash(token)).Scan(&principal.ID, &principal.WardID)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "Цей пристрій відключено.")
			return
		}
		if _, err := s.db.ExecContext(r.Context(), "UPDATE devices SET last_active_at=? WHERE id=?", s.nowStamp(), principal.ID); err != nil {
			s.log.Warn("Не вдалося оновити активність пристрою", "помилка", err)
		}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceKey, principal)))
	}
}

func changesState(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// helperOf повертає помічника з контексту запиту.
func helperOf(r *http.Request) helperPrincipal {
	value, _ := r.Context().Value(helperKey).(helperPrincipal)
	return value
}

// deviceOf повертає пристрій з контексту запиту.
func deviceOf(r *http.Request) devicePrincipal {
	value, _ := r.Context().Value(deviceKey).(devicePrincipal)
	return value
}

// throttle перевіряє, чи не заблоковано спроби для цього суб'єкта.
func (s *Server) throttle(ctx context.Context, scope, subject string) bool {
	var lockedUntil string
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(locked_until,'') FROM login_attempts WHERE scope=? AND subject=?", scope, subject).Scan(&lockedUntil)
	if err != nil || lockedUntil == "" {
		return false
	}
	return lockedUntil > s.nowStamp()
}

// registerFailure збільшує лічильник невдалих спроб і блокує після п'ятої.
func (s *Server) registerFailure(ctx context.Context, scope, subject string, limit int, lock time.Duration) {
	var failures int
	err := s.db.QueryRowContext(ctx,
		"SELECT failures FROM login_attempts WHERE scope=? AND subject=?", scope, subject).Scan(&failures)
	if err != nil && !isNoRows(err) {
		s.log.Warn("Не вдалося прочитати лічильник спроб", "помилка", err)
		return
	}
	failures++
	lockedUntil := ""
	if failures >= limit {
		lockedUntil = stamp(s.now().Add(lock))
		failures = 0
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO login_attempts(id,scope,subject,failures,locked_until,updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(scope,subject) DO UPDATE SET failures=excluded.failures,locked_until=excluded.locked_until,updated_at=excluded.updated_at`,
		security.ID(), scope, subject, failures, nullIfEmpty(lockedUntil), s.nowStamp()); err != nil {
		s.log.Warn("Не вдалося зберегти лічильник спроб", "помилка", err)
	}
}

// clearFailures скидає лічильник після успішного входу.
func (s *Server) clearFailures(ctx context.Context, scope, subject string) {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM login_attempts WHERE scope=? AND subject=?", scope, subject); err != nil {
		s.log.Warn("Не вдалося очистити лічильник спроб", "помилка", err)
	}
}
