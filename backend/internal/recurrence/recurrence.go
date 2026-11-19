// Пакет recurrence обчислює дати повторення завдань у локальному часовому поясі підопічного.
package recurrence

import (
	"errors"
	"time"
)

// Види повторення, які приймає застосунок.
const (
	Once     = "once"
	Daily    = "daily"
	Weekly   = "weekly"
	Interval = "interval"
)

// ErrUnknownKind повертається для невідомого виду повторення.
var ErrUnknownKind = errors.New("невідомий вид повторення")

// Rule описує розклад одного завдання.
type Rule struct {
	// StartDate у форматі 2006-01-02 за локальним часом підопічного.
	StartDate string
	// TimeOfDay у форматі 15:04 за локальним часом підопічного.
	TimeOfDay string
	// Timezone — назва зони IANA, наприклад Europe/Kyiv.
	Timezone string
	Kind     string
	// Weekdays містить дні тижня для Weekly: 0 — неділя, 6 — субота.
	Weekdays []int
	// Interval — крок у днях для Interval.
	Interval int
}

// Valid перевіряє, що вид повторення відомий і має потрібні параметри.
func Valid(kind string, weekdays []int, interval int) bool {
	switch kind {
	case Once, Daily:
		return true
	case Weekly:
		if len(weekdays) == 0 {
			return false
		}
		for _, day := range weekdays {
			if day < 0 || day > 6 {
				return false
			}
		}
		return true
	case Interval:
		return interval >= 1 && interval <= 60
	}
	return false
}

// Occurrences повертає моменти виконання у UTC на проміжку [from, to].
//
// Обчислення виконується за локальною датою, тому перехід на літній час
// не зсуває час прийому ліків: о 08:00 за Києвом завжди означає 08:00.
func (r Rule) Occurrences(from, to time.Time) ([]time.Time, error) {
	location, err := time.LoadLocation(r.Timezone)
	if err != nil {
		location = time.UTC
	}
	start, err := time.ParseInLocation("2006-01-02 15:04", r.StartDate+" "+r.TimeOfDay, location)
	if err != nil {
		return nil, err
	}
	if !Valid(r.Kind, r.Weekdays, r.Interval) {
		return nil, ErrUnknownKind
	}
	startDay := dayStart(start)
	weekdays := make(map[int]bool, len(r.Weekdays))
	for _, day := range r.Weekdays {
		weekdays[day] = true
	}

	out := make([]time.Time, 0, 32)
	cursor := dayStart(from.In(location))
	last := dayStart(to.In(location))
	for ; !cursor.After(last); cursor = cursor.AddDate(0, 0, 1) {
		if cursor.Before(startDay) {
			continue
		}
		if !r.matches(cursor, startDay, weekdays) {
			continue
		}
		moment := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), start.Hour(), start.Minute(), 0, 0, location)
		if moment.Before(from) || moment.After(to) {
			continue
		}
		out = append(out, moment.UTC())
	}
	return out, nil
}

func (r Rule) matches(day, startDay time.Time, weekdays map[int]bool) bool {
	switch r.Kind {
	case Once:
		return day.Equal(startDay)
	case Daily:
		return true
	case Weekly:
		return weekdays[int(day.Weekday())]
	case Interval:
		return int(day.Sub(startDay).Hours()/24)%r.Interval == 0
	}
	return false
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
