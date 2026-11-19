package api

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
)

// reportSummary — зведення виконання за проміжок часу.
type reportSummary struct {
	From          string         `json:"from"`
	To            string         `json:"to"`
	Total         int            `json:"total"`
	Done          int            `json:"done"`
	Missed        int            `json:"missed"`
	Planned       int            `json:"planned"`
	TimelyPercent int            `json:"timelyPercent"`
	Escalations   int            `json:"escalations"`
	HelpRequests  int            `json:"helpRequests"`
	ByCategory    map[string]int `json:"byCategory"`
	// Metrics — показники, за якими перевіряється досягнення мети застосунку.
	Metrics researchMetrics `json:"metrics"`
}

// report повертає зведення для вкладки звіту.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	from, to := s.reportRange(r)
	summary, err := s.buildReport(r.Context(), wardID, from, to)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося побудувати звіт.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, summary)
}

func (s *Server) reportRange(r *http.Request) (string, string) {
	from := r.URL.Query().Get("from")
	if from == "" {
		from = stamp(s.now().AddDate(0, 0, -7))
	}
	to := r.URL.Query().Get("to")
	if to == "" {
		to = s.nowStamp()
	}
	return from, to
}

func (s *Server) buildReport(ctx context.Context, wardID, from, to string) (reportSummary, error) {
	summary := reportSummary{From: from, To: to, ByCategory: map[string]int{}}
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status='done' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN status='missed' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN status='planned' THEN 1 ELSE 0 END),0)
		FROM task_occurrences WHERE ward_id=? AND scheduled_at BETWEEN ? AND ?`,
		wardID, from, to).Scan(&summary.Total, &summary.Done, &summary.Missed, &summary.Planned)
	if err != nil {
		return summary, err
	}
	// Вчасно — це виконано до кінця дозволеного вікна.
	var timely int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM task_occurrences
		WHERE ward_id=? AND scheduled_at BETWEEN ? AND ? AND status='done'
		  AND completed_at IS NOT NULL AND completed_at<=completion_deadline`,
		wardID, from, to).Scan(&timely); err != nil {
		return summary, err
	}
	summary.TimelyPercent = percent(timely, summary.Total)

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM escalations WHERE ward_id=? AND opened_at BETWEEN ? AND ?`,
		wardID, from, to).Scan(&summary.Escalations); err != nil {
		return summary, err
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM help_requests WHERE ward_id=? AND created_at BETWEEN ? AND ?`,
		wardID, from, to).Scan(&summary.HelpRequests); err != nil {
		return summary, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT t.category,COUNT(*) FROM task_occurrences o JOIN tasks t ON t.id=o.task_id
		WHERE o.ward_id=? AND o.scheduled_at BETWEEN ? AND ? AND o.status='done'
		GROUP BY t.category`, wardID, from, to)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			return summary, err
		}
		summary.ByCategory[category] = count
	}
	if err := rows.Err(); err != nil {
		return summary, err
	}

	metrics, err := s.buildMetrics(ctx, wardID, from, to)
	if err != nil {
		return summary, err
	}
	summary.Metrics = metrics
	return summary, nil
}

// exportReport віддає виконання за проміжок у форматі CSV.
func (s *Server) exportReport(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permView) {
		return
	}
	from, to := s.reportRange(r)
	items, err := s.occurrencesBetween(r.Context(), wardID, from, to)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося підготувати експорт.", err)
		return
	}
	location := s.wardLocation(r.Context(), wardID)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="doglyad-report.csv"`)
	// BOM потрібен, щоб Excel відкрив кирилицю без спотворень.
	if _, err := w.Write([]byte("\xEF\xBB\xBF")); err != nil {
		return
	}
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	header := []string{"Дата", "Час", "Справа", "Категорія", "Критична", "Статус", "Виконано о", "Хто відмітив"}
	if err := writer.Write(header); err != nil {
		s.logError("Не вдалося записати заголовок CSV", err)
		return
	}
	for _, item := range items {
		scheduled, _ := parseStamp(item.ScheduledAt)
		local := scheduled.In(location)
		completed := ""
		if parsed, ok := parseStamp(item.CompletedAt); ok {
			completed = parsed.In(location).Format("15:04")
		}
		record := []string{
			local.Format("2006-01-02"),
			local.Format("15:04"),
			item.Title,
			categoryName(item.Category),
			yesNo(item.Critical),
			statusName(item.Status),
			completed,
			item.MarkedBy,
		}
		if err := writer.Write(record); err != nil {
			s.logError("Не вдалося записати рядок CSV", err)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		s.logError("Не вдалося завершити експорт CSV", err)
	}
}

// auditJournal повертає журнал дій за профілем підопічного.
func (s *Server) auditJournal(w http.ResponseWriter, r *http.Request) {
	wardID := r.URL.Query().Get("wardId")
	if !s.requireWard(w, r, wardID, permPrimary) {
		return
	}
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT a.id,a.action,a.entity_type,COALESCE(a.entity_id,''),COALESCE(h.display_name,a.actor,'Система'),a.created_at
		FROM audit_log a LEFT JOIN helpers h ON h.id=a.helper_id
		WHERE a.ward_id=? ORDER BY a.created_at DESC LIMIT ?`, wardID, limit)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося отримати журнал.", err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, action, entity, entityID, actor, createdAt string
		if err := rows.Scan(&id, &action, &entity, &entityID, &actor, &createdAt); err != nil {
			s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати журнал.", err)
			return
		}
		out = append(out, map[string]string{
			"id": id, "action": action, "entity": entity, "entityID": entityID,
			"actor": actor, "createdAt": createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		s.fail(w, http.StatusInternalServerError, "Не вдалося прочитати журнал.", err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

func percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}

func categoryName(category string) string {
	switch category {
	case "medicine":
		return "Ліки"
	case "procedure":
		return "Процедура"
	case "visit":
		return "Візит"
	case "food":
		return "Їжа"
	}
	return "Інше"
}

func statusName(status string) string {
	switch status {
	case "done":
		return "Виконано"
	case "missed":
		return "Пропущено"
	}
	return "Заплановано"
}

func yesNo(value bool) string {
	if value {
		return "так"
	}
	return "ні"
}
