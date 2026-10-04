package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/report"
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
	s.render(w, r, "today", data{"U": u, "Nav": "today", "Title": "Today", "Now": now, "Refresh": true, "Cards": cards,
		"Present": present, "Working": working, "OnBreak": onBreak, "Late": late, "Stale": stales, "Refused": refused})
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
	if strings.HasSuffix(r.URL.Path, ".csv") {
		writeCSV(w, rep, from.Format(dayFmt), to.Format(dayFmt))
		return
	}
	s.render(w, r, "report", data{"U": u, "Nav": "report", "Title": "Report", "R": rep, "From": from.Format(dayFmt),
		"To": to.Format(dayFmt), "UserID": uid, "Workers": all, "Presets": report.Presets(now), "GridMax": report.MaxGridDays})
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
