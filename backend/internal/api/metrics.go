package api

import (
	"context"
	"sort"
)

// auditBlockedDose — дія в журналі, якою позначається спрацювання захисту
// від повторного приймання ліків.
const auditBlockedDose = "заблоковано повторне приймання"

// auditBlockedEarly — дія в журналі, якою позначається спрацювання захисту
// від завчасної відмітки: завтрашні ліки не можна позначити прийнятими сьогодні.
const auditBlockedEarly = "заблоковано завчасну відмітку"

// researchMetrics — показники, за якими перевіряється, чи досягнуто мети застосунку.
//
// Кожен відповідає одному твердженню: сигнал доходить швидко, людина
// справляється з однієї спроби, подвійне приймання не трапляється,
// автопідбір налаштувань влучає.
type researchMetrics struct {
	// Час від появи сигналу до першої реакції помічника, у секундах.
	MedianHelpResponseSeconds       int `json:"medianHelpResponseSeconds"`
	MedianEscalationResponseSeconds int `json:"medianEscalationResponseSeconds"`
	// Скільки сигналів лишилися без реакції у межах проміжку.
	UnansweredSignals int `json:"unansweredSignals"`
	// Скільки разів застосунок не дав прийняти ліки вдруге.
	BlockedDoubleDoses int `json:"blockedDoubleDoses"`
	// Скільки разів застосунок не дав відмітити справу раніше за її час.
	BlockedEarlyMarks int `json:"blockedEarlyMarks"`
	// Виконання з однієї спроби проти тих, де відмітку довелося виправляти.
	CompletedFirstTry       int `json:"completedFirstTry"`
	CompletedWithCorrection int `json:"completedWithCorrection"`
	// Рішення помічника щодо автоматичних порад.
	SuggestionsAccepted int `json:"suggestionsAccepted"`
	SuggestionsDeclined int `json:"suggestionsDeclined"`
	SuggestionsPending  int `json:"suggestionsPending"`
}

// buildMetrics рахує показники дослідження за проміжок часу.
func (s *Server) buildMetrics(ctx context.Context, wardID, from, to string) (researchMetrics, error) {
	var metrics researchMetrics

	helpDurations, helpUnanswered, err := s.helpResponseTimes(ctx, wardID, from, to)
	if err != nil {
		return metrics, err
	}
	escalationDurations, escalationUnanswered, err := s.escalationResponseTimes(ctx, wardID, from, to)
	if err != nil {
		return metrics, err
	}
	metrics.MedianHelpResponseSeconds = median(helpDurations)
	metrics.MedianEscalationResponseSeconds = median(escalationDurations)
	metrics.UnansweredSignals = helpUnanswered + escalationUnanswered

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_log
		WHERE ward_id=? AND action=? AND created_at BETWEEN ? AND ?`,
		wardID, auditBlockedDose, from, to).Scan(&metrics.BlockedDoubleDoses); err != nil {
		return metrics, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_log
		WHERE ward_id=? AND action=? AND created_at BETWEEN ? AND ?`,
		wardID, auditBlockedEarly, from, to).Scan(&metrics.BlockedEarlyMarks); err != nil {
		return metrics, err
	}

	// Виконання вважається зробленим з першого разу, якщо відмітку не скасовували
	// і підопічний не питав «чи я вже приймав».
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN o.correction_count=0 AND u.asked IS NULL THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN o.correction_count>0 OR u.asked IS NOT NULL THEN 1 ELSE 0 END),0)
		FROM task_occurrences o
		LEFT JOIN (SELECT occurrence_id,1 AS asked FROM unsure_requests GROUP BY occurrence_id) u
			ON u.occurrence_id=o.id
		WHERE o.ward_id=? AND o.status='done' AND o.scheduled_at BETWEEN ? AND ?`,
		wardID, from, to).Scan(&metrics.CompletedFirstTry, &metrics.CompletedWithCorrection); err != nil {
		return metrics, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status='accepted' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='declined' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status='created' THEN 1 ELSE 0 END),0)
		FROM adaptation_suggestions WHERE ward_id=? AND created_at BETWEEN ? AND ?`,
		wardID, from, to).Scan(&metrics.SuggestionsAccepted, &metrics.SuggestionsDeclined,
		&metrics.SuggestionsPending); err != nil {
		return metrics, err
	}
	return metrics, nil
}

// helpResponseTimes повертає тривалості реакції на запити допомоги
// та кількість запитів, на які ніхто не відгукнувся.
func (s *Server) helpResponseTimes(ctx context.Context, wardID, from, to string) ([]int, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT hr.created_at,
		       COALESCE((SELECT MIN(ha.created_at) FROM help_acknowledgements ha WHERE ha.request_id=hr.id),''),
		       COALESCE(hr.closed_at,'')
		FROM help_requests hr
		WHERE hr.ward_id=? AND hr.created_at BETWEEN ? AND ?`, wardID, from, to)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	durations := []int{}
	unanswered := 0
	for rows.Next() {
		var createdAt, acknowledgedAt, closedAt string
		if err := rows.Scan(&createdAt, &acknowledgedAt, &closedAt); err != nil {
			return nil, 0, err
		}
		// Реакцією вважаємо перше з двох: «беру на себе» або закриття запиту.
		reaction := earliest(acknowledgedAt, closedAt)
		if seconds, ok := secondsBetween(createdAt, reaction); ok {
			durations = append(durations, seconds)
		} else {
			unanswered++
		}
	}
	return durations, unanswered, rows.Err()
}

// escalationResponseTimes рахує те саме для ескалацій критичних справ.
func (s *Server) escalationResponseTimes(ctx context.Context, wardID, from, to string) ([]int, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT opened_at,COALESCE(taken_at,''),COALESCE(closed_at,'')
		FROM escalations WHERE ward_id=? AND opened_at BETWEEN ? AND ?`, wardID, from, to)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	durations := []int{}
	unanswered := 0
	for rows.Next() {
		var openedAt, takenAt, closedAt string
		if err := rows.Scan(&openedAt, &takenAt, &closedAt); err != nil {
			return nil, 0, err
		}
		reaction := earliest(takenAt, closedAt)
		if seconds, ok := secondsBetween(openedAt, reaction); ok {
			durations = append(durations, seconds)
		} else {
			unanswered++
		}
	}
	return durations, unanswered, rows.Err()
}

// earliest повертає ранішу з двох позначок часу, пропускаючи порожні.
func earliest(first, second string) string {
	switch {
	case first == "":
		return second
	case second == "":
		return first
	case first < second:
		return first
	default:
		return second
	}
}

// secondsBetween рахує різницю у секундах; друга позначка може бути порожньою.
func secondsBetween(start, end string) (int, bool) {
	startTime, okStart := parseStamp(start)
	endTime, okEnd := parseStamp(end)
	if !okStart || !okEnd || endTime.Before(startTime) {
		return 0, false
	}
	return int(endTime.Sub(startTime).Seconds()), true
}

// median повертає медіану, бо середнє псується одним забутим на ніч сигналом.
func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}
