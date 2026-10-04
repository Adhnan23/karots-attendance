package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/report"
	"github.com/Adhnan23/karots-attendance/internal/schedule"
)

// today is the live board: who is in, on break, late, and what needs fixing.
func (s *Server) today(w http.ResponseWriter, r *http.Request, u *user) {
	now := time.Now()
	day := now.Format(dayFmt)
	sess, err := report.Load(s.db, day, day, 0, now)
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := s.listUsers("WHERE role='worker' AND active=1")
	if err != nil {
		fail(w, err)
		return
	}
	type card struct {
		Name, Status               string
		In, Worked, Break, Printed int64
		Late                       bool
	}
	var cards []card
	var present, working, onBreak, late int
	for _, wk := range workers {
		c := card{Name: wk.Name, Status: "absent"}
		for _, x := range sess { // oldest first, so the last one sets the status
			if x.UserID != wk.ID {
				continue
			}
			if c.In == 0 {
				c.In, c.Late = x.Start, x.Late
			}
			c.Worked += x.Worked
			c.Break += x.Break
			c.Printed = max(c.Printed, x.Printed)
			c.Status = x.Status
		}
		if c.In != 0 {
			present++
		}
		switch c.Status {
		case report.StatusWorking:
			working++
		case report.StatusBreak:
			onBreak++
		}
		if c.Late {
			late++
		}
		cards = append(cards, c)
	}

	type stale struct {
		ID        int64
		Name, Day string
	}
	var stales []stale
	rows, err := s.db.Query(`SELECT s.id,u.name,s.day FROM shifts s JOIN users u ON u.id=s.user_id
		WHERE s.ended IS NULL AND s.day<? ORDER BY s.day DESC LIMIT 20`, day)
	if err != nil {
		fail(w, err)
		return
	}
	for rows.Next() {
		var x stale
		if err := rows.Scan(&x.ID, &x.Name, &x.Day); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		stales = append(stales, x)
	}
	rows.Close()

	var refused int
	midnight := parseDay(day, now).Unix()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE at>=? AND kind IN ('login_failed','login_refused','login_locked')`,
		midnight).Scan(&refused); err != nil {
		fail(w, err)
		return
	}
	movables, err := s.movables(now, workers)
	if err != nil {
		fail(w, err)
		return
	}
	s.render(w, r, "today", data{"U": u, "Nav": "today", "Title": "Today", "Now": now, "Refresh": true, "Cards": cards,
		"Present": present, "Working": working, "OnBreak": onBreak, "Late": late, "Stale": stales, "Refused": refused,
		"Movables": movables})
}

type movable struct {
	Title, Worker, Status, Done, Last, Reason string
	Moved                                     int // days it was carried over
}

// movables reports each worker's state on movable tasks that are running or ended in the last week:
// "moving" (not done yet), "done" (on which day) or "expired" (last day passed, never ticked).
func (s *Server) movables(now time.Time, workers []user) ([]movable, error) {
	today, weekAgo := now.Format(dayFmt), now.AddDate(0, 0, -7).Format(dayFmt)
	tasks, err := s.loadTasks(`WHERE t.kind='movable' AND t.active=1 AND t.date<=?`, today)
	if err != nil {
		return nil, err
	}
	daysFrom := func(a, b string) int {
		x, _ := time.Parse(dayFmt, a)
		y, _ := time.Parse(dayFmt, b)
		return int(y.Sub(x).Hours() / 24)
	}
	var out []movable
	for _, t := range tasks {
		last := t.LastDay()
		if last < weekAgo {
			continue
		}
		// ponytail: one query per task; fine for a shop's handful of movable tasks.
		rows, err := s.db.Query(`SELECT user_id,day,COALESCE(done_at,0),reason FROM task_done WHERE task_id=? AND day>=? ORDER BY day`, t.ID, t.Date)
		if err != nil {
			return nil, err
		}
		done, reason := map[int64]string{}, map[int64]string{}
		for rows.Next() {
			var uid, at int64
			var day, why string
			if err := rows.Scan(&uid, &day, &at, &why); err != nil {
				rows.Close()
				return nil, err
			}
			if at != 0 && done[uid] == "" {
				done[uid] = day
			}
			if why != "" {
				reason[uid] = why
			}
		}
		rows.Close()
		for _, wk := range workers {
			if t.UserID != 0 && t.UserID != wk.ID || wk.Created > t.Date {
				continue
			}
			m := movable{Title: t.Title, Worker: wk.Name, Last: schedule.PrettyDay(last)}
			switch d := done[wk.ID]; {
			case d != "":
				m.Status, m.Done, m.Moved = "done", schedule.PrettyDay(d), daysFrom(t.Date, d)
			case last < today:
				m.Status, m.Moved, m.Reason = "expired", t.Every-1, reason[wk.ID]
			default:
				m.Status, m.Moved = "moving", daysFrom(t.Date, today)
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Server) report(w http.ResponseWriter, r *http.Request, u *user) {
	now := time.Now()
	from, to := parseDay(r.FormValue("from"), now), parseDay(r.FormValue("to"), now)
	if to.Before(from) {
		from, to = to, from
	}
	uid, _ := strconv.ParseInt(r.FormValue("user"), 10, 64)
	sess, err := report.Load(s.db, from.Format(dayFmt), to.Format(dayFmt), uid, now)
	if err != nil {
		fail(w, err)
		return
	}
	all, err := s.listUsers("WHERE role='worker'")
	if err != nil {
		fail(w, err)
		return
	}
	var listed []report.Worker
	for _, x := range all {
		if uid == x.ID || uid == 0 && x.Active {
			listed = append(listed, report.Worker{ID: x.ID, Name: x.Name})
		}
	}
	rep := report.Build(sess, listed, from, to, now)
	tasks, sums, err := s.taskRows(sess, now)
	if err != nil {
		fail(w, err)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".csv") {
		if r.FormValue("tasks") != "" {
			writeTaskCSV(w, tasks, from.Format(dayFmt), to.Format(dayFmt))
		} else {
			writeCSV(w, rep, from.Format(dayFmt), to.Format(dayFmt))
		}
		return
	}
	var notDone []taskRow
	for _, t := range tasks {
		if t.Status == "not done" {
			notDone = append(notDone, t)
		}
	}
	s.render(w, r, "report", data{"U": u, "Nav": "report", "Title": "Report", "R": rep, "From": from.Format(dayFmt),
		"To": to.Format(dayFmt), "UserID": uid, "Workers": all, "Presets": report.Presets(now), "GridMax": report.MaxGridDays,
		"TaskSums": sums, "NotDone": notDone})
}

type taskRow struct {
	Day, Worker, Title, Time, Status, Reason string
	DoneAt                                   int64
}

type taskSum struct {
	Name                       string
	Done, NotDone, Moved, Open int
}

// taskRows lists every task on each day a worker was present: "done", "not done" (with their reason, or
// none if they never pressed End day), "moved" (movable, not its last day) or "open" (today, still running).
func (s *Server) taskRows(sess []report.Session, now time.Time) ([]taskRow, []taskSum, error) {
	today := now.Format(dayFmt)
	seen, idx := map[string]bool{}, map[int64]int{}
	var rows []taskRow
	var sums []taskSum
	for _, x := range sess { // oldest first
		k := x.Day + "|" + strconv.FormatInt(x.UserID, 10)
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := idx[x.UserID]; !ok {
			idx[x.UserID] = len(sums)
			sums = append(sums, taskSum{Name: x.Name})
		}
		sum := &sums[idx[x.UserID]]
		d, _ := time.ParseInLocation(dayFmt, x.Day, time.Local)
		ts, err := s.dayTasks(x.UserID, d) // ponytail: a few queries per worker-day; fine for 62 days × a shop's staff
		if err != nil {
			return nil, nil, err
		}
		for _, t := range ts {
			row := taskRow{Day: x.Day, Worker: x.Name, Title: t.Title, Time: t.Time(), Reason: t.Reason, DoneAt: t.DoneAt}
			switch {
			case t.DoneAt != 0:
				row.Status = "done"
				sum.Done++
			case t.Reason == "" && x.Day == today:
				row.Status = "open"
				sum.Open++
			case t.Reason == "" && t.Kind == schedule.Movable && t.LastDay() != x.Day:
				row.Status = "moved"
				sum.Moved++
			default:
				row.Status = "not done"
				sum.NotDone++
			}
			rows = append(rows, row)
		}
	}
	return rows, sums, nil
}

func writeTaskCSV(w http.ResponseWriter, rows []taskRow, from, to string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="tasks_%s_%s.csv"`, from, to))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Date", "Worker", "Task", "Time", "Status", "Done at", "Reason"})
	for _, x := range rows {
		at := ""
		if x.DoneAt != 0 {
			at = time.Unix(x.DoneAt, 0).Format("15:04")
		}
		cw.Write([]string{x.Day, safeCell(x.Worker), safeCell(x.Title), x.Time, x.Status, at, safeCell(x.Reason)})
	}
	cw.Flush()
}

func writeCSV(w http.ResponseWriter, rep report.Report, from, to string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="attendance_%s_%s.csv"`, from, to))
	cw := csv.NewWriter(w)
	cw.Write([]string{"Date", "Worker", "Arrived", "Left", "Break minutes", "Worked hours", "Late", "Sheet printed", "Status", "Edited by admin"})
	hm := func(t int64) string {
		if t == 0 {
			return ""
		}
		return time.Unix(t, 0).Format("15:04")
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return ""
	}
	for _, x := range rep.Sessions {
		cw.Write([]string{x.Day, safeCell(x.Name), hm(x.Start), hm(x.End), strconv.FormatInt(x.Break/60, 10),
			strconv.FormatFloat(float64(x.Worked)/3600, 'f', 2, 64), yes(x.Late), hm(x.Printed), x.Status, yes(x.Edited)})
	}
	cw.Flush()
}

// safeCell stops spreadsheet apps from treating a cell as a formula (CSV injection).
func safeCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
