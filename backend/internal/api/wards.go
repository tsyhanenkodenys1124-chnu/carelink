package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"doglyad/backend/internal/security"
)

// listWards повертає підопічних, доступних помічникові, з лічильниками відкритих сигналів.
func (s *Server) listWards(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT w.id,w.address_name,w.timezone,COALESCE(w.photo_path,''),COALESCE(w.emergency_phone,''),
		       cl.is_primary,cl.can_edit_tasks,cl.can_edit_settings,cl.can_escalations,
		       (SELECT COUNT(*) FROM help_requests hr WHERE hr.ward_id=w.id AND hr.status='open'),
		       (SELECT COUNT(*) FROM unsure_requests ur WHERE ur.ward_id=w.id AND ur.status='open'),
		       (SELECT COUNT(*) FROM escalations e WHERE e.ward_id=w.id AND e.status<>'closed')
		FROM wards w JOIN care_links cl ON cl.ward_id=w.id
		WHERE cl.helper_id=? AND cl.revoked_at IS NULL AND w.deleted_at IS NULL
		ORDER BY w.address_name`, helperOf(r).ID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати список підопічних.", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, timezone, photo, phone string
		var primary, tasks, settings, escalations int
		var openHelp, openUnsure, openEscalations int
		if err := rows.Scan(&id, &name, &timezone, &photo, &phone,
			&primary, &tasks, &settings, &escalations,
			&openHelp, &openUnsure, &openEscalations); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати підопічних.", err)
			return
		}
		out = append(out, map[string]any{
			"id":             id,
			"addressName":    name,
			"timezone":       timezone,
			"photo":          photo,
			"emergencyPhone": phone,
			"rights":         Rights{true, tasks == 1, settings == 1, escalations == 1, primary == 1},
			"openSignals": map[string]int{
				"help":        openHelp,
				"unsure":      openUnsure,
				"escalations": openEscalations,
				"total":       openHelp + openUnsure + openEscalations,
			},
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати підопічних.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// createWard створює профіль підопічного та робить автора головним помічником.
func (s *Server) createWard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AddressName    string `json:"addressName"`
		Timezone       string `json:"timezone"`
		EmergencyPhone string `json:"emergencyPhone"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.AddressName)
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Вкажіть, як звертатися до підопічного.")
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

	helperID := helperOf(r).ID
	wardID := security.ID()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити профіль.", err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO wards(id,address_name,timezone,emergency_phone,created_at) VALUES(?,?,?,?,?)`,
		wardID, name, timezone, strings.TrimSpace(in.EmergencyPhone), s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити профіль.", err)
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO care_links(id,ward_id,helper_id,is_primary,can_view,can_edit_tasks,can_edit_settings,can_escalations,created_at)
		VALUES(?,?,?,1,1,1,1,1,?)`, security.ID(), wardID, helperID, s.nowStamp()); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося призначити головного помічника.", err)
		return
	}
	if _, err := tx.Exec(`INSERT INTO accessibility_settings(ward_id,updated_at,updated_by) VALUES(?,?,?)`,
		wardID, s.nowStamp(), helperID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити налаштування доступності.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося створити профіль.", err)
		return
	}
	s.audit(r.Context(), wardID, helperID, "створено", "підопічний", wardID)
	s.writeJSON(w, http.StatusCreated, map[string]string{"id": wardID})
}

// getWard повертає повний профіль підопічного.
func (s *Server) getWard(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	var name, timezone, phone, photo string
	var wakeLock int
	var hasPIN int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT address_name,timezone,COALESCE(emergency_phone,''),COALESCE(photo_path,''),wake_lock_enabled,
		       CASE WHEN pin_hash IS NULL THEN 0 ELSE 1 END
		FROM wards WHERE id=? AND deleted_at IS NULL`, wardID).
		Scan(&name, &timezone, &phone, &photo, &wakeLock, &hasPIN)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "Підопічного не знайдено.")
		return
	}
	rights, err := s.rightsFor(r.Context(), helperOf(r).ID, wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати права.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"id":              wardID,
		"addressName":     name,
		"timezone":        timezone,
		"emergencyPhone":  phone,
		"photo":           photo,
		"wakeLockEnabled": wakeLock == 1,
		"pinConfigured":   hasPIN == 1,
		"rights":          rights,
	})
}

// updateWard змінює основні дані профілю.
func (s *Server) updateWard(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	var in struct {
		AddressName     string `json:"addressName"`
		Timezone        string `json:"timezone"`
		EmergencyPhone  string `json:"emergencyPhone"`
		WakeLockEnabled bool   `json:"wakeLockEnabled"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.AddressName)
	if name == "" {
		s.writeError(w, http.StatusBadRequest, "Вкажіть форму звертання.")
		return
	}
	timezone := strings.TrimSpace(in.Timezone)
	if _, err := time.LoadLocation(timezone); err != nil {
		s.writeError(w, http.StatusBadRequest, "Невідомий часовий пояс.")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE wards SET address_name=?,timezone=?,emergency_phone=?,wake_lock_enabled=? WHERE id=?`,
		name, timezone, strings.TrimSpace(in.EmergencyPhone), boolInt(in.WakeLockEnabled), wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти профіль.", err)
		return
	}
	// Часовий пояс впливає на розклад: оновлюємо майбутні виконання.
	if _, err := s.db.ExecContext(r.Context(), "UPDATE tasks SET timezone=? WHERE ward_id=? AND deleted_at IS NULL", timezone, wardID); err != nil {
		s.log.Warn("Не вдалося оновити часовий пояс завдань", "помилка", err)
	}
	s.rescheduleWard(r.Context(), wardID)
	s.audit(r.Context(), wardID, helperOf(r).ID, "змінено", "підопічний", wardID)
	w.WriteHeader(http.StatusNoContent)
}

// deleteWard позначає профіль видаленим і відключає пристрої.
func (s *Server) deleteWard(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	now := s.nowStamp()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося видалити профіль.", err)
		return
	}
	defer tx.Rollback()
	for _, query := range []string{
		"UPDATE wards SET deleted_at=? WHERE id=?",
		"UPDATE devices SET revoked_at=? WHERE ward_id=? AND revoked_at IS NULL",
		"UPDATE tasks SET deleted_at=? WHERE ward_id=? AND deleted_at IS NULL",
	} {
		if _, err := tx.Exec(query, now, wardID); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося видалити профіль.", err)
			return
		}
	}
	if _, err := tx.Exec("DELETE FROM task_occurrences WHERE ward_id=? AND status='planned'", wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прибрати майбутні виконання.", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося видалити профіль.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "видалено", "підопічний", wardID)
	w.WriteHeader(http.StatusNoContent)
}

// needsAnswers містить відповіді профілю функціональних потреб.
type needsAnswers struct {
	VisionText   string `json:"visionText"`
	VisionColors string `json:"visionColors"`
	Hearing      string `json:"hearing"`
	Motor        string `json:"motor"`
	Dyslexia     string `json:"dyslexia"`
	Memory       string `json:"memory"`
	PrefersAudio string `json:"prefersAudio"`
}

func (n needsAnswers) values() []string {
	return []string{n.VisionText, n.VisionColors, n.Hearing, n.Motor, n.Dyslexia, n.Memory, n.PrefersAudio}
}

// getNeeds повертає профіль функціональних потреб.
func (s *Server) getNeeds(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	var answers needsAnswers
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COALESCE(vision_text,''),COALESCE(vision_colors,''),COALESCE(hearing,''),COALESCE(motor,''),
		       COALESCE(dyslexia,''),COALESCE(memory,''),COALESCE(prefers_audio,'')
		FROM needs_profiles WHERE ward_id=?`, wardID).
		Scan(&answers.VisionText, &answers.VisionColors, &answers.Hearing, &answers.Motor,
			&answers.Dyslexia, &answers.Memory, &answers.PrefersAudio)
	if isNoRows(err) {
		s.writeJSON(w, http.StatusOK, needsAnswers{})
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати профіль потреб.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, answers)
}

// putNeeds зберігає відповіді та застосовує типові налаштування доступності.
func (s *Server) putNeeds(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	var in needsAnswers
	if !s.readJSON(w, r, &in) {
		return
	}
	for _, value := range in.values() {
		switch value {
		case "", "yes", "partly", "no", "skip":
		default:
			s.writeError(w, http.StatusBadRequest, "Відповідь може бути: так, частково, ні або пропустити.")
			return
		}
	}
	helperID := helperOf(r).ID
	if _, err := s.db.ExecContext(r.Context(), `
		INSERT INTO needs_profiles(ward_id,vision_text,vision_colors,hearing,motor,dyslexia,memory,prefers_audio,updated_at,updated_by)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(ward_id) DO UPDATE SET
			vision_text=excluded.vision_text,vision_colors=excluded.vision_colors,hearing=excluded.hearing,
			motor=excluded.motor,dyslexia=excluded.dyslexia,memory=excluded.memory,prefers_audio=excluded.prefers_audio,
			updated_at=excluded.updated_at,updated_by=excluded.updated_by`,
		wardID, in.VisionText, in.VisionColors, in.Hearing, in.Motor, in.Dyslexia, in.Memory, in.PrefersAudio,
		s.nowStamp(), helperID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти профіль потреб.", err)
		return
	}
	changes, err := s.applyNeedDefaults(r.Context(), wardID, helperID, in)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося застосувати налаштування.", err)
		return
	}
	settings, err := s.readAccessibility(r.Context(), wardID)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати налаштування.", err)
		return
	}
	s.audit(r.Context(), wardID, helperID, "змінено", "потреби", wardID)
	// Повертаємо не лише результат, а й причину кожної зміни: помічник має бачити,
	// що саме застосунок зробив з його відповідей.
	s.writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "changes": changes})
}

// accessibilitySettings описує налаштування екрана підопічного.
type accessibilitySettings struct {
	Scale          int     `json:"scale"`
	Palette        string  `json:"palette"`
	Magnifier      bool    `json:"magnifier"`
	MagnifierScale int     `json:"magnifierScale"`
	LargeControls  bool    `json:"largeControls"`
	DyslexicFont   bool    `json:"dyslexicFont"`
	LetterSpacing  string  `json:"letterSpacing"`
	WordSpacing    string  `json:"wordSpacing"`
	LineHeight     string  `json:"lineHeight"`
	DementiaMode   bool    `json:"dementiaMode"`
	SpeechEnabled  bool    `json:"speechEnabled"`
	SpeechRate     float64 `json:"speechRate"`
}

// getAccessibility повертає налаштування екрана.
func (s *Server) getAccessibility(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	settings, err := s.readAccessibility(r.Context(), wardID)
	if err != nil {
		s.fail(w, http.StatusNotFound, "Налаштування не знайдено.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, settings)
}

func (s *Server) readAccessibility(ctx context.Context, wardID string) (accessibilitySettings, error) {
	var out accessibilitySettings
	var magnifier, large, dyslexic, dementia, speech int
	err := s.db.QueryRowContext(ctx, `
		SELECT scale,palette,magnifier,magnifier_scale,large_controls,dyslexic_font,
		       letter_spacing,word_spacing,line_height,dementia_mode,speech_enabled,speech_rate
		FROM accessibility_settings WHERE ward_id=?`, wardID).
		Scan(&out.Scale, &out.Palette, &magnifier, &out.MagnifierScale, &large, &dyslexic,
			&out.LetterSpacing, &out.WordSpacing, &out.LineHeight, &dementia, &speech, &out.SpeechRate)
	if err != nil {
		return out, err
	}
	out.Magnifier = magnifier == 1
	out.LargeControls = large == 1
	out.DyslexicFont = dyslexic == 1
	out.DementiaMode = dementia == 1
	out.SpeechEnabled = speech == 1
	return out, nil
}

// putAccessibility зберігає налаштування екрана підопічного.
func (s *Server) putAccessibility(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permSettings) {
		return
	}
	var in accessibilitySettings
	if !s.readJSON(w, r, &in) {
		return
	}
	if err := validateAccessibility(&in); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.saveAccessibility(r.Context(), wardID, helperOf(r).ID, in); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти налаштування.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "змінено", "доступність", wardID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveAccessibility(ctx context.Context, wardID, helperID string, in accessibilitySettings) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE accessibility_settings SET scale=?,palette=?,magnifier=?,magnifier_scale=?,large_controls=?,
			dyslexic_font=?,letter_spacing=?,word_spacing=?,line_height=?,dementia_mode=?,speech_enabled=?,
			speech_rate=?,updated_at=?,updated_by=? WHERE ward_id=?`,
		in.Scale, in.Palette, boolInt(in.Magnifier), in.MagnifierScale, boolInt(in.LargeControls),
		boolInt(in.DyslexicFont), in.LetterSpacing, in.WordSpacing, in.LineHeight,
		boolInt(in.DementiaMode), boolInt(in.SpeechEnabled), in.SpeechRate,
		s.nowStamp(), helperID, wardID)
	return err
}

func validateAccessibility(in *accessibilitySettings) error {
	switch in.Scale {
	case 100, 125, 150, 200:
	default:
		return errInvalid("Масштаб може бути 100, 125, 150 або 200 відсотків.")
	}
	switch in.Palette {
	case "standard", "high-contrast", "color-safe":
	default:
		return errInvalid("Палітра може бути звичайною, контрастною або безпечною для кольорів.")
	}
	if in.MagnifierScale < 2 || in.MagnifierScale > 4 {
		in.MagnifierScale = 2
	}
	if in.SpeechRate < 0.5 || in.SpeechRate > 1.5 {
		in.SpeechRate = 1
	}
	if in.LineHeight == "" {
		in.LineHeight = "1.5"
	}
	if in.LetterSpacing == "" {
		in.LetterSpacing = "0"
	}
	if in.WordSpacing == "" {
		in.WordSpacing = "0"
	}
	return nil
}

type validationError string

func (v validationError) Error() string { return string(v) }

func errInvalid(message string) error { return validationError(message) }

// putPIN встановлює PIN режиму помічника на пристрої підопічного.
func (s *Server) putPIN(w http.ResponseWriter, r *http.Request) {
	wardID := r.PathValue("id")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	var in struct {
		PIN string `json:"pin"`
	}
	if !s.readJSON(w, r, &in) {
		return
	}
	if len(in.PIN) < 4 || len(in.PIN) > 6 {
		s.writeError(w, http.StatusBadRequest, "PIN-код має містити від 4 до 6 цифр.")
		return
	}
	for _, digit := range in.PIN {
		if digit < '0' || digit > '9' {
			s.writeError(w, http.StatusBadRequest, "PIN-код складається лише з цифр.")
			return
		}
	}
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE wards SET pin_hash=?,pin_failed_attempts=0,pin_locked_until=NULL WHERE id=?",
		security.HashPassword(in.PIN), wardID); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося зберегти PIN-код.", err)
		return
	}
	s.audit(r.Context(), wardID, helperOf(r).ID, "змінено", "PIN", wardID)
	w.WriteHeader(http.StatusNoContent)
}
