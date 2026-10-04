package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/report"
)

// Admins fix attendance here: forgotten "End day", wrong times, missed logins.

func parseLocal(v string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02T15:04", v, time.Local)
}

func (s *Server) loadShift(id int64) (report.Session, error) {
	var day string
	var uid int64
	if err := s.db.QueryRow(`SELECT day,user_id FROM shifts WHERE id=?`, id).Scan(&day, &uid); err != nil {
		return report.Session{}, err
	}
	list, err := report.Load(s.db, day, day, uid, time.Now())
	for _, x := range list {
		if x.ID == id {
			return x, err
		}
	}
	return report.Session{}, errors.New("session not found")
}

func reportLink(day string) string { return "/admin/report?from=" + day + "&to=" + day }

func (s *Server) shiftPage(w http.ResponseWriter, r *http.Request, u *user) {
	sh, err := s.loadShift(pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "shift", data{"U": u, "Nav": "report", "Title": "Edit session", "S": sh})
}

// shiftTimes validates the in/out form fields. out may be empty only for a session still running today.
func shiftTimes(r *http.Request) (in time.Time, out *time.Time, err error) {
	in, err = parseLocal(r.FormValue("in"))
	if err != nil {
		return in, nil, errors.New("enter the arrival time")
	}
	if v := r.FormValue("out"); v != "" {
		o, err := parseLocal(v)
		if err != nil || !o.After(in) {
			return in, nil, errors.New("leave time must be after arrival")
		}
		if o.Sub(in) > 24*time.Hour {
			return in, nil, errors.New("a session can't be longer than 24 hours")
		}
		out = &o
	} else if in.Format(dayFmt) != time.Now().Format(dayFmt) {
		return in, nil, errors.New("a past session needs a leave time")
	}
	if in.After(time.Now()) {
		return in, nil, errors.New("arrival can't be in the future")
	}
	return in, out, nil
}

func (s *Server) saveShift(w http.ResponseWriter, r *http.Request, u *user) {
	id := pathID(r)
	page := "/admin/shifts/" + strconv.FormatInt(id, 10)
	old, err := s.loadShift(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	in, out, err := shiftTimes(r)
	if err != nil {
		back(w, r, page, err, "")
		return
	}
	var ended any
	if out != nil {
		ended = out.Unix()
	}
	tx, err := s.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE shifts SET started=?, ended=?, day=?, edited=1 WHERE id=?`, in.Unix(), ended, in.Format(dayFmt), id); err != nil {
		fail(w, err)
		return
	}
	if out != nil {
		if _, err := tx.Exec(`UPDATE breaks SET ended=? WHERE shift_id=? AND ended IS NULL`, out.Unix(), id); err != nil {
			fail(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, err)
		return
	}
	s.event(r, u.ID, u.Name, "admin.shift_edit", fmt.Sprintf("%s %s: %s → %s", old.Name, old.Day,
		time.Unix(old.Start, 0).Format("15:04"), in.Format("15:04")))
	back(w, r, reportLink(in.Format(dayFmt)), nil, "Session updated")
}

func (s *Server) addShift(w http.ResponseWriter, r *http.Request, u *user) {
	uid, _ := strconv.ParseInt(r.FormValue("user"), 10, 64)
	in, out, err := shiftTimes(r)
	if err == nil && out == nil {
		err = errors.New("enter the leave time")
	}
	if err != nil {
		back(w, r, "/admin/report", err, "")
		return
	}
	var name string
	if err := s.db.QueryRow(`SELECT name FROM users WHERE id=? AND role='worker'`, uid).Scan(&name); err != nil {
		back(w, r, "/admin/report", errors.New("pick a worker"), "")
		return
	}
	if _, err := s.db.Exec(`INSERT INTO shifts(user_id,day,started,ended,edited) VALUES(?,?,?,?,1)`,
		uid, in.Format(dayFmt), in.Unix(), out.Unix()); err != nil {
		fail(w, err)
		return
	}
	s.event(r, u.ID, u.Name, "admin.shift_add", fmt.Sprintf("%s %s %s–%s", name, in.Format(dayFmt), in.Format("15:04"), out.Format("15:04")))
	back(w, r, reportLink(in.Format(dayFmt)), nil, "Session added for "+name)
}

func (s *Server) deleteShift(w http.ResponseWriter, r *http.Request, u *user) {
	old, err := s.loadShift(pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.db.Exec(`DELETE FROM shifts WHERE id=?`, old.ID); err != nil {
		fail(w, err)
		return
	}
	s.event(r, u.ID, u.Name, "admin.shift_delete", old.Name+" "+old.Day)
	back(w, r, reportLink(old.Day), nil, "Session deleted")
}

func (s *Server) deleteBreak(w http.ResponseWriter, r *http.Request, u *user) {
	var sid int64
	if err := s.db.QueryRow(`SELECT shift_id FROM breaks WHERE id=?`, pathID(r)).Scan(&sid); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.db.Exec(`DELETE FROM breaks WHERE id=?`, pathID(r)); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.db.Exec(`UPDATE shifts SET edited=1 WHERE id=?`, sid); err != nil {
		fail(w, err)
		return
	}
	s.event(r, u.ID, u.Name, "admin.break_delete", fmt.Sprint("session ", sid))
	back(w, r, "/admin/shifts/"+strconv.FormatInt(sid, 10), nil, "Break deleted")
}
