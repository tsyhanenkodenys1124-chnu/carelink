package api

import (
	"context"
	"net/http"
	"time"

	"doglyad/backend/internal/security"
)

// Права доступу помічника до даних підопічного.
const (
	permView        = "view"
	permTasks       = "tasks"
	permSettings    = "settings"
	permEscalations = "escalations"
	permPrimary     = "primary"
)

// Rights описує, що саме помічник може робити з профілем підопічного.
type Rights struct {
	View         bool `json:"view"`
	EditTasks    bool `json:"editTasks"`
	EditSettings bool `json:"editSettings"`
	Escalations  bool `json:"escalations"`
	Primary      bool `json:"primary"`
}

// allows перевіряє конкретне право з урахуванням ролі головного помічника.
func (r Rights) allows(permission string) bool {
	switch permission {
	case permView:
		return r.View
	case permTasks:
		return r.EditTasks || r.Primary
	case permSettings:
		return r.EditSettings || r.Primary
	case permEscalations:
		return r.Escalations || r.Primary
	case permPrimary:
		return r.Primary
	}
	return false
}

// rightsFor читає права помічника щодо підопічного.
func (s *Server) rightsFor(ctx context.Context, helperID, wardID string) (Rights, error) {
	var view, tasks, settings, escalations, primary int
	err := s.db.QueryRowContext(ctx, `
		SELECT can_view,can_edit_tasks,can_edit_settings,can_escalations,is_primary
		FROM care_links WHERE helper_id=? AND ward_id=? AND revoked_at IS NULL`,
		helperID, wardID).Scan(&view, &tasks, &settings, &escalations, &primary)
	if err != nil {
		return Rights{}, err
	}
	return Rights{view == 1, tasks == 1, settings == 1, escalations == 1, primary == 1}, nil
}

// requireWard перевіряє доступ і сам відповідає клієнту у разі відмови.
func (s *Server) requireWard(w http.ResponseWriter, r *http.Request, wardID, permission string) bool {
	if wardID == "" {
		s.writeError(w, http.StatusBadRequest, "Не вказано підопічного.")
		return false
	}
	rights, err := s.rightsFor(r.Context(), helperOf(r).ID, wardID)
	if err != nil || !rights.View {
		s.writeError(w, http.StatusForbidden, "У вас немає доступу до даних цього підопічного.")
		return false
	}
	if !rights.allows(permission) {
		s.writeError(w, http.StatusForbidden, "У вас немає потрібного права для цієї дії.")
		return false
	}
	return true
}

// wardIDsFor повертає всіх підопічних, до яких помічник має доступ.
func (s *Server) wardIDsFor(ctx context.Context, helperID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT ward_id FROM care_links WHERE helper_id=? AND revoked_at IS NULL", helperID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var wardID string
		if err := rows.Scan(&wardID); err != nil {
			return nil, err
		}
		out = append(out, wardID)
	}
	return out, rows.Err()
}

// helperIDsFor повертає помічників, які мають доступ до підопічного зараз.
func (s *Server) helperIDsFor(ctx context.Context, wardID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT helper_id FROM care_links WHERE ward_id=? AND revoked_at IS NULL", wardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var helperID string
		if err := rows.Scan(&helperID); err != nil {
			return nil, err
		}
		out = append(out, helperID)
	}
	return out, rows.Err()
}

// wardOfOccurrence знаходить підопічного за виконанням завдання.
func (s *Server) wardOfOccurrence(ctx context.Context, occurrenceID string) (string, error) {
	var wardID string
	err := s.db.QueryRowContext(ctx, "SELECT ward_id FROM task_occurrences WHERE id=?", occurrenceID).Scan(&wardID)
	return wardID, err
}

// audit записує дію до журналу підопічного.
func (s *Server) audit(ctx context.Context, wardID, helperID, action, entity, entityID string) {
	s.auditAs(ctx, wardID, helperID, "", action, entity, entityID)
}

// actorPIN — підпис діяча для дій, виконаних у режимі PIN на пристрої підопічного.
// Обліковий запис там не вказують, але це й не «Система»: біля планшета стояла людина.
const actorPIN = "Пристрій підопічного (PIN)"

// auditAs записує дію, явно називаючи діяча там, де облікового запису немає.
func (s *Server) auditAs(ctx context.Context, wardID, helperID, actor, action, entity, entityID string) {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log(id,ward_id,helper_id,actor,action,entity_type,entity_id,created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		security.ID(), nullIfEmpty(wardID), nullIfEmpty(helperID), nullIfEmpty(actor),
		action, entity, nullIfEmpty(entityID), s.nowStamp()); err != nil {
		s.log.Warn("Не вдалося записати подію до журналу", "дія", action, "помилка", err)
	}
}

// wardName повертає форму звертання до підопічного для текстів сповіщень.
func (s *Server) wardName(ctx context.Context, wardID string) string {
	var name string
	if err := s.db.QueryRowContext(ctx, "SELECT address_name FROM wards WHERE id=?", wardID).Scan(&name); err != nil {
		return "підопічний"
	}
	return name
}

// wardLocation повертає часовий пояс підопічного. Усі тексти про час —
// і на екрані, і в сповіщеннях — показують саме його місцевий час: помічник
// може сидіти в іншому поясі, але діє за годинником підопічного.
func (s *Server) wardLocation(ctx context.Context, wardID string) *time.Location {
	var timezone string
	if err := s.db.QueryRowContext(ctx, "SELECT timezone FROM wards WHERE id=?", wardID).Scan(&timezone); err != nil {
		s.log.Warn("Не вдалося прочитати часовий пояс підопічного", "підопічний", wardID, "помилка", err)
		return time.UTC
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		s.log.Warn("Невідомий часовий пояс підопічного", "пояс", timezone, "помилка", err)
		return time.UTC
	}
	return location
}
