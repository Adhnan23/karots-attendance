// Package report loads work sessions and turns them into attendance reports.
package report

import (
	"database/sql"
	"fmt"
	"slices"
	"time"
)

const DayFmt = "2006-01-02"

// MaxGridDays caps the worker × day grid so long ranges stay readable.
const MaxGridDays = 62

const (
	StatusWorking  = "working"
	StatusBreak    = "on break"
	StatusDone     = "done"
	StatusNotEnded = "not ended" // forgot "End day" on a past day; counts 0h until an admin fixes it
)

type Break struct{ ID, Start, End int64 }

// Session is one shift (login .. End day) with derived numbers. Times are unix seconds.
type Session struct {
	ID, UserID                                     int64
	Name, Day, Status, StartAt                     string
	Start, End, Printed, Break, Worked, BreakSince int64
	Late, Edited                                   bool
	Breaks                                         []Break
}

// Load returns sessions with day in [from, to] (YYYY-MM-DD), oldest first; uid 0 = everyone.
func Load(db *sql.DB, from, to string, uid int64, now time.Time) ([]Session, error) {
	rows, err := db.Query(`SELECT s.id,s.user_id,u.name,u.start_at,s.day,s.started,COALESCE(s.ended,0),COALESCE(s.printed,0),s.edited
		FROM shifts s JOIN users u ON u.id=s.user_id
		WHERE s.day BETWEEN ? AND ? AND (?=0 OR s.user_id=?) ORDER BY s.day, s.started`, from, to, uid, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Session
	idx := map[int64]int{}
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.StartAt, &s.Day, &s.Start, &s.End, &s.Printed, &s.Edited); err != nil {
			return nil, err
		}
		idx[s.ID] = len(list)
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	brs, err := db.Query(`SELECT b.id,b.shift_id,b.started,COALESCE(b.ended,0) FROM breaks b JOIN shifts s ON s.id=b.shift_id
		WHERE s.day BETWEEN ? AND ? AND (?=0 OR s.user_id=?) ORDER BY b.started`, from, to, uid, uid)
	if err != nil {
		return nil, err
	}
	defer brs.Close()
	for brs.Next() {
		var b Break
		var sid int64
		if err := brs.Scan(&b.ID, &sid, &b.Start, &b.End); err != nil {
			return nil, err
		}
		if i, ok := idx[sid]; ok {
			list[i].Breaks = append(list[i].Breaks, b)
		}
	}
	compute(list, now)
	return list, brs.Err()
}

// compute fills Status, Break, Worked, BreakSince and Late. list must be oldest first.
func compute(list []Session, now time.Time) {
	n, today := now.Unix(), now.Format(DayFmt)
	seen := map[string]bool{}
	for i := range list {
		s := &list[i]
		end := s.End
		switch {
		case s.End != 0:
			s.Status = StatusDone
		case s.Day != today:
			s.Status, end = StatusNotEnded, s.Start
		default:
			s.Status, end = StatusWorking, n
		}
		s.Break = 0
		if s.Status != StatusNotEnded {
			for _, b := range s.Breaks {
				be := b.End
				if be == 0 {
					be = end
					if s.Status == StatusWorking {
						s.Status, s.BreakSince = StatusBreak, b.Start
					}
				}
				s.Break += max(0, min(be, end)-b.Start)
			}
		}
		s.Worked = max(0, end-s.Start-s.Break)
		// Only the first arrival of the day can be late.
		if k := fmt.Sprint(s.UserID, s.Day); !seen[k] {
			seen[k] = true
			s.Late = s.StartAt != "" && time.Unix(s.Start, 0).Format("15:04") > s.StartAt
		}
	}
}

type Worker struct {
	ID   int64
	Name string
}

type Summary struct {
	UserID                          int64
	Name, AvgIn                     string
	Days, Late, Unprinted, NotEnded int
	Worked, Break, AvgDay           int64
	Pct                             int // Worked relative to the top worker, for the bar
	arrivals                        int64
}

type Cell struct {
	Worked          int64
	Has, Late, Open bool
	Today           bool
}

type Row struct {
	Name  string
	Cells []Cell
}

type Report struct {
	Sessions                              []Session // newest first
	Totals                                []Summary
	Days                                  []time.Time // grid columns; empty when the range is too long
	Grid                                  []Row
	TotalWorked, AvgDay                   int64
	PersonDays, Late, Unprinted, NotEnded int
}

// Build aggregates sessions (oldest first) per worker and per day. workers are listed even with no sessions.
func Build(sess []Session, workers []Worker, from, to, now time.Time) Report {
	var r Report
	sums := map[int64]*Summary{}
	var order []int64
	get := func(id int64, name string) *Summary {
		if s, ok := sums[id]; ok {
			return s
		}
		sums[id] = &Summary{UserID: id, Name: name}
		order = append(order, id)
		return sums[id]
	}
	for _, w := range workers {
		get(w.ID, w.Name)
	}

	type key struct {
		uid int64
		day string
	}
	days := map[key]*Cell{}
	printed := map[key]bool{}
	for _, s := range sess {
		sm := get(s.UserID, s.Name)
		k := key{s.UserID, s.Day}
		c := days[k]
		if c == nil {
			c = &Cell{Has: true, Late: s.Late}
			days[k] = c
			sm.Days++
			t := time.Unix(s.Start, 0)
			sm.arrivals += int64(t.Hour()*3600 + t.Minute()*60 + t.Second())
			if s.Late {
				sm.Late++
			}
		}
		c.Worked += s.Worked
		c.Open = c.Open || s.Status == StatusNotEnded
		printed[k] = printed[k] || s.Printed != 0
		sm.Worked += s.Worked
		sm.Break += s.Break
		if s.Status == StatusNotEnded {
			sm.NotEnded++
		}
	}
	for k, ok := range printed {
		if !ok {
			sums[k.uid].Unprinted++
		}
	}

	var top int64
	for _, id := range order {
		top = max(top, sums[id].Worked)
	}
	for _, id := range order {
		s := sums[id]
		if s.Days > 0 {
			s.AvgDay = s.Worked / int64(s.Days)
			s.AvgIn = time.Date(2000, 1, 1, 0, 0, int(s.arrivals/int64(s.Days)), 0, time.UTC).Format("3:04 PM")
		}
		if top > 0 {
			s.Pct = int(s.Worked * 100 / top)
		}
		r.TotalWorked += s.Worked
		r.PersonDays += s.Days
		r.Late += s.Late
		r.Unprinted += s.Unprinted
		r.NotEnded += s.NotEnded
		r.Totals = append(r.Totals, *s)
	}
	if r.PersonDays > 0 {
		r.AvgDay = r.TotalWorked / int64(r.PersonDays)
	}

	if n := int(to.Sub(from).Hours()/24) + 1; n >= 1 && n <= MaxGridDays {
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			r.Days = append(r.Days, d)
		}
		today := now.Format(DayFmt)
		for _, id := range order {
			row := Row{Name: sums[id].Name}
			for _, d := range r.Days {
				c := Cell{Today: d.Format(DayFmt) == today}
				if x := days[key{id, d.Format(DayFmt)}]; x != nil {
					c.Worked, c.Has, c.Late, c.Open = x.Worked, true, x.Late, x.Open
				}
				row.Cells = append(row.Cells, c)
			}
			r.Grid = append(r.Grid, row)
		}
	}

	r.Sessions = slices.Clone(sess)
	slices.Reverse(r.Sessions)
	return r
}

type Preset struct{ Label, From, To string }

// Presets are the quick date ranges (weeks start Monday).
func Presets(now time.Time) []Preset {
	d := func(t time.Time) string { return t.Format(DayFmt) }
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	week := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, now.Location())
	return []Preset{
		{"Today", d(today), d(today)},
		{"Yesterday", d(today.AddDate(0, 0, -1)), d(today.AddDate(0, 0, -1))},
		{"This week", d(week), d(today)},
		{"Last week", d(week.AddDate(0, 0, -7)), d(week.AddDate(0, 0, -1))},
		{"This month", d(month), d(today)},
		{"Last month", d(month.AddDate(0, -1, 0)), d(month.AddDate(0, 0, -1))},
	}
}
