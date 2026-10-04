package schedule

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	d, _ := time.ParseInLocation(DayFmt, s, time.Local)
	return d
}

func TestOn(t *testing.T) {
	cases := []struct {
		task Task
		yes  []string
		no   []string
	}{
		{Task{Kind: Daily}, []string{"2026-10-05", "2026-12-31"}, nil},
		{Task{Kind: Daily, StartDate: "2026-10-10", EndDate: "2026-10-12"}, []string{"2026-10-10", "2026-10-12"}, []string{"2026-10-09", "2026-10-13"}},
		{Task{Kind: Weekly, Weekdays: "15"}, []string{"2026-10-05", "2026-10-09"}, []string{"2026-10-06", "2026-10-04"}}, // Mon, Fri
		{Task{Kind: Interval, Every: 3, Date: "2026-10-05"}, []string{"2026-10-05", "2026-10-08", "2026-11-01"}, []string{"2026-10-02", "2026-10-06", "2026-10-07"}},
		{Task{Kind: Monthly, Mday: 15}, []string{"2026-10-15", "2026-02-15"}, []string{"2026-10-16"}},
		{Task{Kind: Monthly, Mday: 31}, []string{"2026-10-31", "2026-02-28", "2026-04-30"}, []string{"2026-04-29"}},
		{Task{Kind: Monthly, Mday: -1}, []string{"2028-02-29", "2026-10-31"}, []string{"2028-02-28"}},
		{Task{Kind: Nth, Mday: 1, Weekdays: "1"}, []string{"2026-10-05", "2026-11-02"}, []string{"2026-10-12", "2026-10-06"}}, // 1st Monday
		{Task{Kind: Nth, Mday: -1, Weekdays: "5"}, []string{"2026-10-30"}, []string{"2026-10-23"}},                            // last Friday
		{Task{Kind: Once, Date: "2026-12-25"}, []string{"2026-12-25"}, []string{"2026-12-24", "2027-12-25"}},
		{Task{Kind: Movable, Date: "2026-10-30", Every: 3}, []string{"2026-10-30", "2026-11-01"}, []string{"2026-10-29", "2026-11-02"}},
	}
	for _, c := range cases {
		for _, d := range c.yes {
			if !c.task.On(day(d)) {
				t.Errorf("%s should be on %s", c.task.Schedule(), d)
			}
		}
		for _, d := range c.no {
			if c.task.On(day(d)) {
				t.Errorf("%s should not be on %s", c.task.Schedule(), d)
			}
		}
	}
}

func TestForDayOrder(t *testing.T) {
	ts := []Task{
		{Title: "untimed", Kind: Daily, Active: true},
		{Title: "untimed important", Kind: Daily, Active: true, Important: true},
		{Title: "9am", Kind: Daily, Active: true, At: "09:00"},
		{Title: "7am", Kind: Daily, Active: true, At: "07:00"},
		{Title: "paused", Kind: Daily},
	}
	got := ForDay(ts, day("2026-10-05"))
	want := []string{"7am", "9am", "untimed important", "untimed"}
	if len(got) != len(want) {
		t.Fatalf("got %d tasks", len(got))
	}
	for i := range want {
		if got[i].Title != want[i] {
			t.Fatalf("order %d: got %q want %q", i, got[i].Title, want[i])
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []Task{
		{Title: "", Kind: Daily},
		{Title: "x", Kind: Weekly},
		{Title: "x", Kind: Interval, Every: 1, Date: "2026-10-05"},
		{Title: "x", Kind: Once},
		{Title: "x", Kind: Daily, At: "10:00", Until: "09:00"},
		{Title: "x", Kind: "nope"},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("expected error for %+v", b)
		}
	}
	ok := Task{Title: " x ", Kind: Weekly, Weekdays: "6610", Date: "2026-10-05"}
	if err := ok.Validate(); err != nil || ok.Weekdays != "016" || ok.Date != "" || ok.Title != "x" {
		t.Fatalf("normalise: %v %+v", err, ok)
	}
}
