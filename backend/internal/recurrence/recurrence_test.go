package recurrence

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("час %q: %v", value, err)
	}
	return parsed
}

func TestDailyKeepsLocalTimeAcrossDaylightSaving(t *testing.T) {
	// Перехід на зимовий час у Києві відбувається в ніч на 26 жовтня 2025 року.
	rule := Rule{
		StartDate: "2025-10-24",
		TimeOfDay: "08:00",
		Timezone:  "Europe/Kyiv",
		Kind:      Daily,
	}
	moments, err := rule.Occurrences(mustTime(t, "2025-10-24T00:00:00Z"), mustTime(t, "2025-10-28T23:00:00Z"))
	if err != nil {
		t.Fatalf("розклад: %v", err)
	}
	if len(moments) != 5 {
		t.Fatalf("очікувалося п'ять виконань, отримано %d", len(moments))
	}
	location, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("часовий пояс: %v", err)
	}
	for _, moment := range moments {
		local := moment.In(location)
		if local.Hour() != 8 || local.Minute() != 0 {
			t.Fatalf("після переведення годинника час зсунувся: %s", local.Format(time.RFC3339))
		}
	}
}

func TestWeeklyPicksOnlySelectedDays(t *testing.T) {
	rule := Rule{
		StartDate: "2026-01-05", // понеділок
		TimeOfDay: "11:00",
		Timezone:  "Europe/Kyiv",
		Kind:      Weekly,
		Weekdays:  []int{1, 4}, // понеділок і четвер
	}
	moments, err := rule.Occurrences(mustTime(t, "2026-01-05T00:00:00Z"), mustTime(t, "2026-01-11T23:00:00Z"))
	if err != nil {
		t.Fatalf("розклад: %v", err)
	}
	if len(moments) != 2 {
		t.Fatalf("очікувалося два виконання, отримано %d", len(moments))
	}
	location, _ := time.LoadLocation("Europe/Kyiv")
	if got := moments[0].In(location).Weekday(); got != time.Monday {
		t.Fatalf("перше виконання не в понеділок: %s", got)
	}
	if got := moments[1].In(location).Weekday(); got != time.Thursday {
		t.Fatalf("друге виконання не в четвер: %s", got)
	}
}

func TestIntervalCountsFromStartDate(t *testing.T) {
	rule := Rule{
		StartDate: "2026-01-01",
		TimeOfDay: "09:00",
		Timezone:  "Europe/Kyiv",
		Kind:      Interval,
		Interval:  3,
	}
	moments, err := rule.Occurrences(mustTime(t, "2026-01-01T00:00:00Z"), mustTime(t, "2026-01-10T23:00:00Z"))
	if err != nil {
		t.Fatalf("розклад: %v", err)
	}
	if len(moments) != 4 {
		t.Fatalf("очікувалося чотири виконання (1, 4, 7, 10 січня), отримано %d", len(moments))
	}
}

func TestOnceHappensExactlyOnce(t *testing.T) {
	rule := Rule{
		StartDate: "2026-03-15",
		TimeOfDay: "14:30",
		Timezone:  "Europe/Kyiv",
		Kind:      Once,
	}
	moments, err := rule.Occurrences(mustTime(t, "2026-03-01T00:00:00Z"), mustTime(t, "2026-03-31T23:00:00Z"))
	if err != nil {
		t.Fatalf("розклад: %v", err)
	}
	if len(moments) != 1 {
		t.Fatalf("очікувалося одне виконання, отримано %d", len(moments))
	}
}

func TestOccurrencesNeverStartBeforeStartDate(t *testing.T) {
	rule := Rule{
		StartDate: "2026-02-10",
		TimeOfDay: "07:00",
		Timezone:  "Europe/Kyiv",
		Kind:      Daily,
	}
	moments, err := rule.Occurrences(mustTime(t, "2026-02-01T00:00:00Z"), mustTime(t, "2026-02-12T23:00:00Z"))
	if err != nil {
		t.Fatalf("розклад: %v", err)
	}
	if len(moments) != 3 {
		t.Fatalf("очікувалося три виконання, отримано %d", len(moments))
	}
}

func TestValidRejectsWeeklyWithoutDays(t *testing.T) {
	if Valid(Weekly, nil, 1) {
		t.Fatal("щотижневе повторення без днів має бути відхилене")
	}
	if Valid(Interval, nil, 0) {
		t.Fatal("інтервал менший за день має бути відхилений")
	}
	if Valid("невідомо", nil, 1) {
		t.Fatal("невідомий вид повторення має бути відхилений")
	}
	if !Valid(Weekly, []int{0, 6}, 1) {
		t.Fatal("вихідні дні мають бути дозволені")
	}
}
