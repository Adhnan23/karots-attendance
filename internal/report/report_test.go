package report

import (
	"testing"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/db"
)

func TestReport(t *testing.T) {
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	must := func(q string, args ...any) {
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	at := func(day string, h, m int) int64 {
		x, _ := time.ParseInLocation(DayFmt, day, time.Local)
		return x.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Unix()
	}
	must(`INSERT INTO users(id,name,phone,pw,role,start_at) VALUES(1,'Ali','1','x','worker','08:00'),(2,'Bob','2','x','worker',''),(3,'Cat','3','x','worker','')`)
	// Ali Mon: 07:55-17:00 with 1h break, printed → 8h05, on time
	must(`INSERT INTO shifts(id,user_id,day,started,ended,printed) VALUES(1,1,'2026-10-05',?,?,?)`, at("2026-10-05", 7, 55), at("2026-10-05", 17, 0), at("2026-10-05", 8, 0))
	must(`INSERT INTO breaks(shift_id,started,ended) VALUES(1,?,?)`, at("2026-10-05", 12, 0), at("2026-10-05", 13, 0))
	// Ali Tue: late 08:30, split day 08:30-12:00 + 13:00-17:00 = 7h30, not printed
	must(`INSERT INTO shifts(id,user_id,day,started,ended) VALUES(2,1,'2026-10-06',?,?),(3,1,'2026-10-06',?,?)`,
		at("2026-10-06", 8, 30), at("2026-10-06", 12, 0), at("2026-10-06", 13, 0), at("2026-10-06", 17, 0))
	// Bob Mon: forgot to end → 0h, flagged
	must(`INSERT INTO shifts(id,user_id,day,started) VALUES(4,2,'2026-10-05',?)`, at("2026-10-05", 9, 0))
	// Bob today (Wed) 09:00, on break since 11:00, now 12:00 → 2h worked
	must(`INSERT INTO shifts(id,user_id,day,started,printed) VALUES(5,2,'2026-10-07',?,?)`, at("2026-10-07", 9, 0), at("2026-10-07", 9, 1))
	must(`INSERT INTO breaks(shift_id,started) VALUES(5,?)`, at("2026-10-07", 11, 0))

	now := time.Unix(at("2026-10-07", 12, 0), 0)
	sess, err := Load(d, "2026-10-01", "2026-10-31", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	by := map[int64]Session{}
	for _, s := range sess {
		by[s.ID] = s
	}
	check := func(id int64, status string, worked int64, late bool) {
		t.Helper()
		s := by[id]
		if s.Status != status || s.Worked != worked || s.Late != late {
			t.Errorf("session %d: got %s %ds late=%v, want %s %ds late=%v", id, s.Status, s.Worked, s.Late, status, worked, late)
		}
	}
	check(1, StatusDone, 8*3600+5*60, false)
	check(2, StatusDone, 3*3600+30*60, true)
	check(3, StatusDone, 4*3600, false) // second session of the day is never "late"
	check(4, StatusNotEnded, 0, false)
	check(5, StatusBreak, 2*3600, false)

	from, _ := time.ParseInLocation(DayFmt, "2026-10-05", time.Local)
	to, _ := time.ParseInLocation(DayFmt, "2026-10-07", time.Local)
	r := Build(sess, []Worker{{1, "Ali"}, {2, "Bob"}, {3, "Cat"}}, from, to, now)
	ali, bob, cat := r.Totals[0], r.Totals[1], r.Totals[2]
	if ali.Days != 2 || ali.Late != 1 || ali.Unprinted != 1 || ali.Worked != (15*3600+35*60) || ali.Pct != 100 {
		t.Errorf("ali: %+v", ali)
	}
	if bob.Days != 2 || bob.NotEnded != 1 || bob.Unprinted != 1 || bob.Worked != 2*3600 {
		t.Errorf("bob: %+v", bob)
	}
	if cat.Days != 0 || cat.Worked != 0 {
		t.Errorf("cat: %+v", cat)
	}
	if r.PersonDays != 4 || len(r.Days) != 3 || len(r.Grid) != 3 || !r.Grid[0].Cells[1].Late || !r.Grid[1].Cells[0].Open || !r.Grid[1].Cells[2].Today {
		t.Errorf("grid/kpis: %+v", r)
	}
	if r.Sessions[0].ID != 5 {
		t.Error("sessions should be newest first")
	}
}
