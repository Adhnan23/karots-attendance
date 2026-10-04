package web

import (
	"database/sql"
	"errors"
	"net/http"
	"time"
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
	tasks, err := s.tasksFor(u.ID, now)
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
