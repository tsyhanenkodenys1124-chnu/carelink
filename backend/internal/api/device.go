package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"doglyad/backend/internal/notify"
	"doglyad/backend/internal/security"
)

const (
	pinAttemptLimit = 5
	pinLockDuration = 15 * time.Minute
	pinSessionTTL   = 5 * time.Minute
)

// deviceToday повертає екран підопічного: поточні справи та налаштування показу.
func (s *Server) deviceToday(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	// Пристрій може бути офлайн годинами, тож дозаповнюємо розклад перед показом.
	s.materializeWard(r.Context(), device.WardID)

	var name, timezone, phone, photo string
	var wakeLock int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT address_name,timezone,COALESCE(emergency_phone,''),COALESCE(photo_path,''),wake_lock_enabled
		FROM wards WHERE id=? AND deleted_at IS NULL`, device.WardID).
		Scan(&name, &timezone, &phone, &photo, &wakeLock)
	if err != nil {
		s.fail(w, http.StatusNotFound, "Профіль підопічного недоступний.", err)
		return
	}
	settings, err := s.readAccessibility(r.Context(), device.WardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати налаштування.", err)
		return
	}

	// Показуємо найближчі справи: те, що ще можна виконати, і щойно виконане.
	from := stamp(s.now().Add(-6 * time.Hour))
	to := stamp(s.now().Add(24 * time.Hour))
	items, err := s.occurrencesBetween(r.Context(), device.WardID, from, to)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати справи.", err)
		return
	}

	type deviceOccurrence struct {
		occurrenceRow
		Steps []string `json:"steps"`
		Photo string   `json:"photo"`
	}
	enriched := make([]deviceOccurrence, 0, len(items))
	for _, item := range items {
		steps, err := s.readSteps(r.Context(), item.TaskID)
		if err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати кроки.", err)
			return
		}
		var photoID string
		if err := s.db.QueryRowContext(r.Context(),
			"SELECT COALESCE(photo_media_id,'') FROM tasks WHERE id=?", item.TaskID).Scan(&photoID); err != nil {
			s.log.Warn("Не вдалося прочитати фото завдання", "помилка", err)
		}
		enriched = append(enriched, deviceOccurrence{occurrenceRow: item, Steps: steps, Photo: photoID})
	}

	// Відповідь помічника на «Я не пам'ятаю» показуємо одразу на екрані.
	var answer string
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(answer,'') FROM unsure_requests
		WHERE ward_id=? AND status='answered' AND answered_at>?
		ORDER BY answered_at DESC LIMIT 1`,
		device.WardID, stamp(s.now().Add(-2*time.Hour))).Scan(&answer); err != nil && !isNoRows(err) {
		s.log.Warn("Не вдалося прочитати відповідь помічника", "помилка", err)
	}
	var helpOpen int
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COUNT(*) FROM help_requests WHERE ward_id=? AND status='open'", device.WardID).Scan(&helpOpen); err != nil {
		s.log.Warn("Не вдалося прочитати запити допомоги", "помилка", err)
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"ward": map[string]any{
			"name":     name,
			"timezone": timezone,
			"phone":    phone,
			"photo":    photo,
			"wakeLock": wakeLock == 1,
		},
		"settings":        settings,
		"occurrences":     enriched,
		"helperAnswer":    answer,
		"helpRequestOpen": helpOpen > 0,
		"serverTime":      s.nowStamp(),
	})
}

// deviceMark приймає відмітку виконання з пристрою підопічного.
func (s *Server) deviceMark(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		OccurrenceID   string `json:"occurrenceID"`
		IdempotencyKey string `json:"idempotencyKey"`
		DeviceTime     string `json:"deviceTime"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if in.OccurrenceID == "" || in.IdempotencyKey == "" {
		s.writeError(w, http.StatusBadRequest, "Бракує даних відмітки.")
		return
	}
	// Ключ ідемпотентності захищає від подвійного запису після відновлення зв'язку.
	var existingStatus string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT status FROM task_occurrences WHERE idempotency_key=?", in.IdempotencyKey).Scan(&existingStatus)
	if err == nil {
		s.writeJSON(w, http.StatusOK, map[string]string{"message": "Готово. Відмітку вже збережено."})
		return
	}

	deviceTime, err := time.Parse(time.RFC3339, in.DeviceTime)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Не вдалося прочитати час пристрою.")
		return
	}
	// Годинник пристрою може відставати, але не на добу вперед і не на два дні назад.
	if deviceTime.After(s.now().Add(5*time.Minute)) || deviceTime.Before(s.now().Add(-48*time.Hour)) {
		s.writeError(w, http.StatusBadRequest, "Час на пристрої надто відрізняється від справжнього.")
		return
	}

	var wardID, status, category, title, scheduledAt string
	var warningMinutes int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT o.ward_id,o.status,t.category,t.title,o.scheduled_at,t.warning_minutes
		FROM task_occurrences o
		JOIN tasks t ON t.id=o.task_id WHERE o.id=?`, in.OccurrenceID).
		Scan(&wardID, &status, &category, &title, &scheduledAt, &warningMinutes)
	if err != nil || wardID != device.WardID {
		s.writeError(w, http.StatusNotFound, "Справу не знайдено.")
		return
	}
	// Справу видно заздалегідь, але відмітити її можна лише з початку вікна.
	// Інакше ввечері можна позначити завтрашні ліки прийнятими: вранці їх ніхто
	// не вип'є, ескалація не спрацює, а помічник бачитиме «виконано».
	if open := openFrom(scheduledAt, warningMinutes); status == "planned" && s.nowStamp() < open {
		s.audit(r.Context(), wardID, "", auditBlockedEarly, "виконання", in.OccurrenceID)
		clock := localClock(scheduledAt, s.wardLocation(r.Context(), wardID))
		s.writeError(w, http.StatusConflict, "Ще рано. Ця справа — о "+clock+". Поки нічого робити не треба.")
		return
	}
	// Захист від повторного приймання ліків. Спрацювання записуємо:
	// це один із показників, за якими оцінюється користь застосунку.
	if status == "done" && category == "medicine" {
		s.audit(r.Context(), wardID, "", auditBlockedDose, "виконання", in.OccurrenceID)
		s.writeError(w, http.StatusConflict, "Ці ліки вже позначено як прийняті. Більше приймати не треба.")
		return
	}

	undoUntil := stamp(s.now().Add(s.cfg.UndoWindow))
	result, err := s.db.ExecContext(r.Context(), `
		UPDATE task_occurrences SET status='done',completed_at=?,device_time=?,device_id=?,
			idempotency_key=?,undo_until=? WHERE id=? AND status='planned'`,
		s.nowStamp(), stamp(deviceTime), device.ID, in.IdempotencyKey, undoUntil, in.OccurrenceID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти відмітку.", err)
		return
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		s.writeJSON(w, http.StatusOK, map[string]string{"message": "Цю справу вже відмічено."})
		return
	}
	s.closeEscalationFor(r.Context(), in.OccurrenceID, "")
	s.audit(r.Context(), wardID, "", "виконано", "виконання", in.OccurrenceID)
	s.publish(r.Context(), wardID, EventOccurrence, title+": виконано", false, map[string]any{
		"occurrenceID": in.OccurrenceID,
		"status":       "done",
	})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"message":   "Готово. Дякуємо.",
		"undoUntil": undoUntil,
	})
}

// deviceUndo дозволяє підопічному одразу скасувати помилкову відмітку.
func (s *Server) deviceUndo(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		OccurrenceID string `json:"occurrenceID"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	wardID, err := s.wardOfOccurrence(r.Context(), in.OccurrenceID)
	if err != nil || wardID != device.WardID {
		s.writeError(w, http.StatusNotFound, "Справу не знайдено.")
		return
	}
	if err := s.undoOccurrence(r.Context(), in.OccurrenceID); err != nil {
		s.writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r.Context(), wardID, "", "скасовано відмітку", "виконання", in.OccurrenceID)
	s.publish(r.Context(), wardID, EventOccurrence, "Відмітку скасовано на пристрої", false, map[string]any{
		"occurrenceID": in.OccurrenceID,
	})
	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Скасовано. Справа знову активна."})
}

// deviceHelp створює сигнал «Потрібна допомога» і показує його всім помічникам.
func (s *Server) deviceHelp(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		Message string `json:"message"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	var openID string
	err := s.db.QueryRowContext(r.Context(),
		"SELECT id FROM help_requests WHERE ward_id=? AND status='open' ORDER BY created_at DESC LIMIT 1",
		device.WardID).Scan(&openID)
	if err == nil && openID != "" {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"id":      openID,
			"message": "Ми вже повідомили помічників. Зачекайте, будь ласка.",
		})
		return
	}

	requestID := security.ID()
	if _, err := s.db.ExecContext(r.Context(),
		"INSERT INTO help_requests(id,ward_id,message,created_at) VALUES(?,?,?,?)",
		requestID, device.WardID, strings.TrimSpace(in.Message), s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося надіслати запит.", err)
		return
	}
	name := s.wardName(r.Context(), device.WardID)
	s.notifyWard(r.Context(), device.WardID, notify.Message{
		Kind:     notify.KindHelp,
		Title:    "Потрібна допомога: " + name,
		Body:     helpBody(in.Message),
		URL:      s.cfg.PublicURL + "/?ward=" + device.WardID + "&tab=signals",
		WardName: name,
		Urgent:   true,
	}, false)
	s.publish(r.Context(), device.WardID, EventHelp, "Потрібна допомога: "+name, true, map[string]any{
		"requestID": requestID,
		"message":   strings.TrimSpace(in.Message),
	})
	s.audit(r.Context(), device.WardID, "", "створено", "запит допомоги", requestID)
	s.writeJSON(w, http.StatusCreated, map[string]string{
		"id":      requestID,
		"message": "Ми повідомили помічників. Вони скоро відгукнуться.",
	})
}

func helpBody(message string) string {
	if trimmed := strings.TrimSpace(message); trimmed != "" {
		return trimmed
	}
	return "Натиснуто кнопку «Потрібна допомога»."
}

// deviceUnsure обробляє питання «Я не пам'ятаю, чи приймав ліки».
func (s *Server) deviceUnsure(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		OccurrenceID string `json:"occurrenceID"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	var wardID, status, completedAt, title string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT o.ward_id,o.status,COALESCE(o.completed_at,''),t.title
		FROM task_occurrences o JOIN tasks t ON t.id=o.task_id WHERE o.id=?`, in.OccurrenceID).
		Scan(&wardID, &status, &completedAt, &title)
	if err != nil || wardID != device.WardID {
		s.writeError(w, http.StatusNotFound, "Справу не знайдено.")
		return
	}
	// Якщо ліки вже прийнято, відповідь дає сам застосунок — без очікування помічника.
	if status == "done" {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"message": "Ви вже прийняли о " + localClock(completedAt, s.wardLocation(r.Context(), wardID)) +
				". Більше приймати не треба.",
		})
		return
	}

	requestID := security.ID()
	if _, err := s.db.ExecContext(r.Context(),
		"INSERT INTO unsure_requests(id,occurrence_id,ward_id,created_at) VALUES(?,?,?,?)",
		requestID, in.OccurrenceID, device.WardID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося надіслати запит.", err)
		return
	}
	name := s.wardName(r.Context(), device.WardID)
	s.notifyWard(r.Context(), device.WardID, notify.Message{
		Kind:     notify.KindUnsure,
		Title:    name + ": «Я не пам'ятаю»",
		Body:     "Питання про справу «" + title + "». Підкажіть, чи вже виконано.",
		URL:      s.cfg.PublicURL + "/?ward=" + device.WardID + "&tab=signals",
		WardName: name,
		Urgent:   true,
	}, false)
	s.publish(r.Context(), device.WardID, EventUnsure, name+": «Я не пам'ятаю» — "+title, true, map[string]any{
		"requestID":    requestID,
		"occurrenceID": in.OccurrenceID,
	})
	s.writeJSON(w, http.StatusCreated, map[string]string{
		"message": "Ми запитали помічника. Зачекайте, будь ласка.",
	})
}

// deviceSignals зберігає знеособлені сигнали зручності екрана.
func (s *Server) deviceSignals(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		ViewportZoomSessions int `json:"viewportZoomSessions"`
		MagnifierUses        int `json:"magnifierUses"`
		QuickUndos           int `json:"quickUndos"`
		SpeechUses           int `json:"speechUses"`
		UnsureUses           int `json:"unsureUses"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	// Добу рахуємо за годинником підопічного й лише на сервері: за UTC усе до
	// третьої ночі в Києві падало б у вчорашній день, а дату з пристрою можна
	// підмінити й зіпсувати дані дослідження.
	date := s.now().In(s.wardLocation(r.Context(), device.WardID)).Format("2006-01-02")
	// Значення накопичуються за добу, тому додаємо, а не перезаписуємо.
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO adaptation_signals(id,ward_id,date,viewport_zoom_sessions,magnifier_uses,quick_undos,speech_uses,unsure_uses,created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(ward_id,date) DO UPDATE SET
			viewport_zoom_sessions=viewport_zoom_sessions+excluded.viewport_zoom_sessions,
			magnifier_uses=magnifier_uses+excluded.magnifier_uses,
			quick_undos=quick_undos+excluded.quick_undos,
			speech_uses=speech_uses+excluded.speech_uses,
			unsure_uses=unsure_uses+excluded.unsure_uses`,
		security.ID(), device.WardID, date, in.ViewportZoomSessions, in.MagnifierUses,
		in.QuickUndos, in.SpeechUses, in.UnsureUses, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти сигнал.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// devicePINLogin відкриває режим помічника на пристрої підопічного.
func (s *Server) devicePINLogin(w http.ResponseWriter, r *http.Request) {
	device := deviceOf(r)
	var in struct {
		PIN string `json:"pin"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	var hash, lockedUntil string
	var attempts int
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT COALESCE(pin_hash,''),pin_failed_attempts,COALESCE(pin_locked_until,'') FROM wards WHERE id=?",
		device.WardID).Scan(&hash, &attempts, &lockedUntil); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося перевірити PIN-код.", err)
		return
	}
	if lockedUntil != "" && lockedUntil > s.nowStamp() {
		s.writeError(w, http.StatusTooManyRequests, "Вхід тимчасово заблоковано. Спробуйте за 15 хвилин.")
		return
	}
	if hash == "" {
		s.writeError(w, http.StatusBadRequest, "PIN-код ще не встановлено.")
		return
	}
	if !security.CheckPassword(hash, in.PIN) {
		attempts++
		if attempts >= pinAttemptLimit {
			if _, err := s.db.ExecContext(r.Context(),
				"UPDATE wards SET pin_failed_attempts=0,pin_locked_until=? WHERE id=?",
				stamp(s.now().Add(pinLockDuration)), device.WardID); err != nil {
				s.logError("Не вдалося заблокувати PIN", err)
			}
			name := s.wardName(r.Context(), device.WardID)
			s.notifyWard(r.Context(), device.WardID, notify.Message{
				Kind:     notify.KindPIN,
				Title:    "П'ять невдалих спроб PIN-коду",
				Body:     "Хтось намагався відкрити режим помічника на пристрої: " + name + ".",
				URL:      s.cfg.PublicURL + "/?ward=" + device.WardID,
				WardName: name,
				Urgent:   true,
			}, true)
		} else if _, err := s.db.ExecContext(r.Context(),
			"UPDATE wards SET pin_failed_attempts=? WHERE id=?", attempts, device.WardID); err != nil {
			s.logError("Не вдалося зберегти лічильник PIN", err)
		}
		s.writeError(w, http.StatusUnauthorized, "Неправильний PIN-код.")
		return
	}

	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE wards SET pin_failed_attempts=0,pin_locked_until=NULL WHERE id=?", device.WardID); err != nil {
		s.log.Warn("Не вдалося скинути лічильник PIN", "помилка", err)
	}
	token := security.Token()
	expiresAt := stamp(s.now().Add(pinSessionTTL))
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO pin_sessions(id,ward_id,device_id,token_hash,expires_at,last_seen_at,created_at)
		VALUES(?,?,?,?,?,?,?)`,
		security.ID(), device.WardID, device.ID, security.Hash(token), expiresAt, s.nowStamp(), s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося відкрити режим помічника.", err)
		return
	}
	s.auditAs(r.Context(), device.WardID, "", actorPIN, "вхід за PIN", "пристрій", device.ID)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"pinToken":  token,
		"expiresAt": expiresAt,
		"wardName":  s.wardName(r.Context(), device.WardID),
	})
}

// materializeWard дозаповнює розклад підопічного перед показом екрана.
func (s *Server) materializeWard(ctx context.Context, wardID string) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM tasks WHERE ward_id=? AND deleted_at IS NULL", wardID)
	if err != nil {
		s.logError("Не вдалося перелічити завдання підопічного", err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			s.logError("Не вдалося прочитати завдання", err)
			return
		}
		ids = append(ids, taskID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logError("Не вдалося перелічити завдання підопічного", err)
		return
	}
	rows.Close()
	for _, taskID := range ids {
		if err := s.materializeTask(ctx, taskID); err != nil {
			s.logError("Не вдалося запланувати виконання", err, "завдання", taskID)
		}
	}
}
