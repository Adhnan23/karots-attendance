// Package schedule decides which tasks fall on a given day.
package schedule

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const DayFmt = "2006-01-02"

// Repeat kinds and the fields each one uses.
const (
	Daily    = "daily"    // every day
	Weekly   = "weekly"   // Weekdays: digits 0(Sun)..6(Sat)
	Interval = "interval" // every Every days counting from Date
	Monthly  = "monthly"  // on day Mday of the month; -1 = last day (also short months)
	Nth      = "nth"      // the Mday-th (1..4, -1 = last) weekday Weekdays (one digit) of the month
	Once     = "once"     // only on Date
	Movable  = "movable"  // from Date for Every days, until ticked (the "until ticked" part lives in the web layer)
)

type Kind struct{ Value, Label string }

var Kinds = []Kind{
	{Daily, "Every day"}, {Weekly, "Weekly on chosen days"}, {Interval, "Every N days"},
	{Monthly, "Monthly on a date"}, {Nth, "Monthly on a weekday (e.g. 1st Monday)"}, {Once, "Once — a special day"},
	{Movable, "Movable — moves to the next day until done"},
}

var ShortDays = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

type Task struct {
	ID                 int64
	Title, Notes       string
	UserID             int64  // 0 = all workers
	Who                string // display name of the worker or "All workers"
	Kind               string
	Date, Weekdays     string
	Every, Mday        int
	StartDate, EndDate string // optional active range, YYYY-MM-DD
	At, Until          string // optional "HH:MM" window
	Important, Active  bool
}

// On reports whether the task falls on day d (only the date part of d is used).
func (t Task) On(d time.Time) bool {
	day := d.Format(DayFmt)
	if t.StartDate != "" && day < t.StartDate || t.EndDate != "" && day > t.EndDate {
		return false
	}
	wd := strconv.Itoa(int(d.Weekday()))
	switch t.Kind {
	case Daily:
		return true
	case Weekly:
		return strings.Contains(t.Weekdays, wd)
	case Interval:
		start, err := time.Parse(DayFmt, t.Date)
		if err != nil || t.Every < 1 {
			return false
		}
		n := int(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC).Sub(start).Hours() / 24)
		return n >= 0 && n%t.Every == 0
	case Monthly:
		m, last := t.Mday, lastDay(d)
		if m == -1 || m > last {
			m = last
		}
		return d.Day() == m
	case Nth:
		if t.Weekdays != wd {
			return false
		}
		if t.Mday == -1 {
			return d.Day()+7 > lastDay(d)
		}
		return (d.Day()-1)/7+1 == t.Mday
	case Once:
		return day == t.Date
	case Movable:
		return day >= t.Date && day <= t.LastDay()
	}
	return false
}

// LastDay is the final day a movable task can show (YYYY-MM-DD).
func (t Task) LastDay() string {
	d, err := time.Parse(DayFmt, t.Date)
	if err != nil {
		return ""
	}
	return d.AddDate(0, 0, t.Every-1).Format(DayFmt)
}

func lastDay(d time.Time) int { return time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// ForDay filters tasks to those on d, sorted: timed ones by time, then untimed; important first within.
func ForDay(tasks []Task, d time.Time) []Task {
	var out []Task
	for _, t := range tasks {
		if t.Active && t.On(d) {
			out = append(out, t)
		}
	}
	timeKey := func(t Task) string { return cmp.Or(t.At, "~") } // "~" sorts after any time
	slices.SortStableFunc(out, func(x, y Task) int {
		return cmp.Or(cmp.Compare(timeKey(x), timeKey(y)), cmp.Compare(b2i(y.Important), b2i(x.Important)))
	})
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Time is the display form of the time window.
func (t Task) Time() string {
	if t.Until != "" {
		return t.At + "–" + t.Until
	}
	return t.At
}

// Schedule describes the repeat rule in words.
func (t Task) Schedule() string {
	var s string
	switch t.Kind {
	case Daily:
		s = "Every day"
	case Weekly:
		var names []string
		for _, c := range t.Weekdays {
			names = append(names, ShortDays[c-'0'])
		}
		s = "Every " + strings.Join(names, ", ")
	case Interval:
		s = fmt.Sprintf("Every %d days from %s", t.Every, PrettyDay(t.Date))
	case Monthly:
		s = "Monthly on the " + Ordinal(t.Mday)
		if t.Mday == -1 {
			s = "Monthly on the last day"
		}
	case Nth:
		d, _ := strconv.Atoi(t.Weekdays)
		s = fmt.Sprintf("%s %s of every month", NthName(t.Mday), time.Weekday(d))
	case Once:
		return "Once on " + PrettyDay(t.Date)
	case Movable:
		return "From " + PrettyDay(t.Date) + ", moves on until done · expires after " + PrettyDay(t.LastDay())
	}
	if t.StartDate != "" {
		s += " · from " + PrettyDay(t.StartDate)
	}
	if t.EndDate != "" {
		s += " · until " + PrettyDay(t.EndDate)
	}
	return s
}

func PrettyDay(day string) string {
	d, err := time.Parse(DayFmt, day)
	if err != nil {
		return day
	}
	return d.Format("Mon 02 Jan 2006")
}

func Ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func NthName(n int) string {
	return map[int]string{1: "First", 2: "Second", 3: "Third", 4: "Fourth", -1: "Last"}[n]
}

// Validate checks and normalises a task built from user input.
func (t *Task) Validate() error {
	t.Title, t.Notes = strings.TrimSpace(t.Title), strings.TrimSpace(t.Notes)
	if t.Title == "" {
		return errors.New("task title is required")
	}
	if len(t.Title) > 200 || len(t.Notes) > 1000 {
		return errors.New("title or notes too long")
	}
	for _, d := range []string{t.Date, t.StartDate, t.EndDate} {
		if _, err := time.Parse(DayFmt, d); d != "" && err != nil {
			return errors.New("invalid date " + d)
		}
	}
	for _, c := range []string{t.At, t.Until} {
		if _, err := time.Parse("15:04", c); c != "" && err != nil {
			return errors.New("invalid time " + c)
		}
	}
	if t.Until != "" && (t.At == "" || t.Until <= t.At) {
		return errors.New("end time must be after start time")
	}
	if t.StartDate != "" && t.EndDate != "" && t.EndDate < t.StartDate {
		return errors.New("'active until' is before 'active from'")
	}
	keep := func(date, wd string, every, mday int) { t.Date, t.Weekdays, t.Every, t.Mday = date, wd, every, mday }
	switch t.Kind {
	case Daily:
		keep("", "", 0, 0)
	case Weekly:
		var wd strings.Builder
		for i := range 7 {
			if strings.ContainsRune(t.Weekdays, rune('0'+i)) {
				wd.WriteByte(byte('0' + i))
			}
		}
		if wd.Len() == 0 {
			return errors.New("pick at least one weekday")
		}
		keep("", wd.String(), 0, 0)
	case Interval:
		if t.Every < 2 || t.Every > 365 || t.Date == "" {
			return errors.New("every N days needs N between 2 and 365 and a start date")
		}
		keep(t.Date, "", t.Every, 0)
	case Monthly:
		if t.Mday != -1 && (t.Mday < 1 || t.Mday > 31) {
			return errors.New("pick a day of the month")
		}
		keep("", "", 0, t.Mday)
	case Nth:
		if NthName(t.Mday) == "" || len(t.Weekdays) != 1 || t.Weekdays[0] < '0' || t.Weekdays[0] > '6' {
			return errors.New("pick which week and weekday")
		}
		keep("", t.Weekdays, 0, t.Mday)
	case Once:
		if t.Date == "" {
			return errors.New("pick the date")
		}
		keep(t.Date, "", 0, 0)
		t.StartDate, t.EndDate = "", ""
	case Movable:
		if t.Date == "" || t.Every < 1 || t.Every > 365 {
			return errors.New("movable needs a first day and 1 to 365 days to keep it")
		}
		keep(t.Date, "", t.Every, 0)
		t.StartDate, t.EndDate = "", ""
	default:
		return errors.New("pick how the task repeats")
	}
	return nil
}
