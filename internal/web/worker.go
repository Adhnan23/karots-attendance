package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/schedule"
)

type shift struct {
	ID, Started int64
	Printed     sql.NullInt64
}

// openShift returns the worker's open shift for today, starting one (= arrival) if needed.
func (s *Server) openShift(r *http.Request, u *user, now time.Time) (sh shift, err error) {
	day := now.Format(dayFmt)
	err = s.db.QueryRow(`SELECT id,started,printed FROM shifts WHERE user_id=? AND day=? AND ended IS NULL
		ORDER BY id DESC LIMIT 1`, u.ID, day).Scan(&sh.ID, &sh.Started, &sh.Printed)
	if errors.Is(err, sql.ErrNoRows) {
		sh.Started = now.Unix()
		err = s.db.QueryRow(`INSERT INTO shifts(user_id,day,started) VALUES(?,?,?) RETURNING id`, u.ID, day, sh.Started).Scan(&sh.ID)
		if err == nil {
			s.event(r, u.ID, u.Name, "arrive", "")
		}
	}
	return
}

func (s *Server) workerData(r *http.Request, u *user) (data, error) {
	now := time.Now()
	sh, err := s.openShift(r, u, now)
	if err != nil {
		return nil, err
	}
	var brk, since int64
	err = s.db.QueryRow(`SELECT COALESCE(SUM(COALESCE(ended,?)-started),0), COALESCE(MAX(CASE WHEN ended IS NULL THEN started END),0)
		FROM breaks WHERE shift_id=?`, now.Unix(), sh.ID).Scan(&brk, &since)
	if err != nil {
		return nil, err
	}
	tasks, err := s.dayTasks(u.ID, now)
	return data{"U": u, "Title": "Today", "S": sh, "OnBreak": since > 0, "BreakSince": since, "Break": brk,
		"Worked": max(0, now.Unix()-sh.Started-brk), "Tasks": tasks, "Now": now}, err
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, u *user) {
	d, err := s.workerData(r, u)
	if err != nil {
		fail(w, err)
		return
	}
	d["Refresh"] = true
	s.render(w, r, "worker", d)
}

func (s *Server) printSheet(w http.ResponseWriter, r *http.Request, u *user) {
	d, err := s.workerData(r, u)
	if err != nil {
		fail(w, err)
		return
	}
	sh := d["S"].(shift)
	if !sh.Printed.Valid {
		if _, err := s.db.Exec(`UPDATE shifts SET printed=? WHERE id=?`, time.Now().Unix(), sh.ID); err != nil {
			fail(w, err)
			return
		}
		s.event(r, u.ID, u.Name, "print", "")
	}
	s.render(w, r, "sheet", data{"Name": u.Name, "Date": d["Now"], "Arrived": sh.Started, "Tasks": d["Tasks"], "Back": "/me", "Blank": make([]int, 3)})
}

func (s *Server) action(w http.ResponseWriter, r *http.Request, u *user) {
	now := time.Now()
	sh, err := s.openShift(r, u, now)
	if err != nil {
		fail(w, err)
		return
	}
	switch act := r.PathValue("action"); act {
	case "pause":
		var res sql.Result
		res, err = s.db.Exec(`INSERT INTO breaks(shift_id,started) SELECT ?,?
			WHERE NOT EXISTS(SELECT 1 FROM breaks WHERE shift_id=? AND ended IS NULL)`, sh.ID, now.Unix(), sh.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				s.event(r, u.ID, u.Name, "break", "")
			}
		}
	case "resume":
		var res sql.Result
		res, err = s.db.Exec(`UPDATE breaks SET ended=? WHERE shift_id=? AND ended IS NULL`, now.Unix(), sh.ID)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				s.event(r, u.ID, u.Name, "resume", "")
			}
		}
	case "end":
		if !s.tasksAnswered(w, r, u, now) {
			return
		}
		if _, err = s.db.Exec(`UPDATE breaks SET ended=? WHERE shift_id=? AND ended IS NULL`, now.Unix(), sh.ID); err == nil {
			_, err = s.db.Exec(`UPDATE shifts SET ended=? WHERE id=?`, now.Unix(), sh.ID)
		}
		if err == nil {
			s.event(r, u.ID, u.Name, "end", "")
			s.logout(w, r)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}

// markTask ticks (after the worker types "yes") or unticks one of today's tasks.
func (s *Server) markTask(w http.ResponseWriter, r *http.Request, u *user) {
	now := time.Now()
	ts, err := s.tasksFor(u.ID, now)
	if err != nil {
		fail(w, err)
		return
	}
	var task schedule.Task
	for _, t := range ts {
		if t.ID == pathID(r) {
			task = t
		}
	}
	if task.ID == 0 { // not on this worker's list today: someone else's task, another day, paused, …
		http.NotFound(w, r)
		return
	}
	id, title, day := task.ID, task.Title, now.Format(dayFmt)
	switch r.PathValue("op") {
	case "done":
		if task.At != "" && now.Format("15:04") < task.At {
			back(w, r, "/me", errors.New("“"+title+"” starts at "+task.At+"; you can't tick it before then"), "")
			return
		}
		switch strings.ToLower(strings.TrimSpace(r.FormValue("answer"))) {
		case "": // no JavaScript (or an empty prompt): ask on the page instead
			d, err := s.workerData(r, u)
			if err != nil {
				fail(w, err)
				return
			}
			d["Ask"] = task
			s.render(w, r, "worker", d)
			return
		case "yes":
			_, err = s.db.Exec(`INSERT INTO task_done(user_id,day,task_id,done_at) VALUES(?,?,?,?)
				ON CONFLICT DO UPDATE SET done_at=excluded.done_at, reason=''`, u.ID, day, id, now.Unix())
			if err == nil {
				s.event(r, u.ID, u.Name, "task_done", title)
				back(w, r, "/me", nil, "✓ "+title)
				return
			}
		case "no":
			http.Redirect(w, r, "/me", http.StatusSeeOther)
			return
		default:
			back(w, r, "/me", errors.New(`type "yes" to tick “`+title+`”`), "")
			return
		}
	case "undo":
		if _, err = s.db.Exec(`DELETE FROM task_done WHERE user_id=? AND day=? AND task_id=?`, u.ID, day, id); err == nil {
			s.event(r, u.ID, u.Name, "task_undo", title)
			back(w, r, "/me", nil, "Unticked “"+title+"”")
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	fail(w, err)
}

// minReason stops one-letter "reasons" like "." or "ok".
const minReason = 10

// tasksAnswered saves any posted reasons ("why<task id>") and reports whether every task today
// is ticked or has a reason. If not, it shows the worker page with the reasons popup.
func (s *Server) tasksAnswered(w http.ResponseWriter, r *http.Request, u *user, now time.Time) bool {
	ts, err := s.dayTasks(u.ID, now)
	if err != nil {
		fail(w, err)
		return false
	}
	var pending []dayTask
	short := false
	for _, t := range ts {
		if t.DoneAt != 0 || t.Reason != "" || t.Kind == schedule.Movable && t.LastDay() != now.Format(dayFmt) {
			continue // a movable task not on its last day just moves to tomorrow
		}
		why := []rune(strings.TrimSpace(r.FormValue("why" + strconv.FormatInt(t.ID, 10))))
		why = why[:min(len(why), 500)]
		if len(why) < minReason {
			short = short || len(why) > 0
			pending = append(pending, t)
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO task_done(user_id,day,task_id,reason) VALUES(?,?,?,?)
			ON CONFLICT DO UPDATE SET reason=excluded.reason`, u.ID, now.Format(dayFmt), t.ID, string(why)); err != nil {
			fail(w, err)
			return false
		}
		s.event(r, u.ID, u.Name, "task_skip", t.Title+": "+string(why))
	}
	if len(pending) == 0 {
		return true
	}
	d, err := s.workerData(r, u)
	if err != nil {
		fail(w, err)
		return false
	}
	d["Pending"], d["MinReason"] = pending, minReason
	if short {
		d["Err"] = fmt.Sprintf("Write a real reason, at least %d letters.", minReason)
	}
	s.render(w, r, "worker", d)
	return false
}
