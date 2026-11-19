package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// maxBodyBytes обмежує розмір тіла запиту JSON.
const maxBodyBytes = 1 << 20

// writeJSON надсилає відповідь у форматі JSON.
func (s *Server) writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logError("Не вдалося надіслати відповідь", err)
	}
}

// writeError надсилає зрозумілу користувачеві помилку українською.
func (s *Server) writeError(w http.ResponseWriter, code int, message string) {
	s.writeJSON(w, code, map[string]string{"error": message})
}

// fail записує технічну причину в журнал і повертає користувачеві загальний текст.
func (s *Server) fail(w http.ResponseWriter, code int, message string, err error) {
	if err != nil {
		s.logError(message, err)
	}
	s.writeError(w, code, message)
}

// decode читає тіло запиту у структуру та відхиляє невідомі поля.
func decode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

// readJSON читає тіло та у разі помилки одразу відповідає клієнту.
func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decode(r, dst); err != nil {
		s.writeError(w, http.StatusBadRequest, "Не вдалося прочитати дані запиту.")
		return false
	}
	return true
}

// now повертає поточний час. Тести підміняють годинник.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UTC()
}

// stamp форматує час так, як його зберігає база.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// nowStamp повертає поточний час у форматі бази.
func (s *Server) nowStamp() string { return stamp(s.now()) }

// parseStamp читає час із бази.
func parseStamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// boolInt переводить прапорець у ціле для SQLite.
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// nullIfEmpty повертає NULL для порожнього рядка.
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// isNoRows повідомляє, що запит не знайшов рядків.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// clientIP визначає адресу клієнта з урахуванням зворотного проксі.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if first, _, found := strings.Cut(forwarded, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}

// logError пише помилку в журнал, але скасований контекст лишає на рівні INFO.
// Контекст скасовують у двох штатних випадках: сервер зупиняють і клієнт
// від'єднується. Якби такі рядки лишалися помилками, журнал кричав би «вовк»
// після кожного перезапуску, і справжню поломку в ньому було б не знайти.
func (s *Server) logError(msg string, err error, args ...any) {
	fields := append(append([]any{}, args...), "помилка", err)
	if errors.Is(err, context.Canceled) {
		s.log.Info(msg+": роботу перервано", fields...)
		return
	}
	s.log.Error(msg, fields...)
}
