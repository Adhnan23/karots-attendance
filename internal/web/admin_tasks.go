package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/schedule"
)

func (s *Server) loadTasks(where string, args ...any) ([]schedule.Task, error) {
	rows, err := s.db.Query(`SELECT t.id,t.title,t.notes,COALESCE(t.user_id,0),COALESCE(u.name,'All workers'),t.kind,t.date,
		t.weekdays,t.every,t.mday,t.start_date,t.end_date,t.at,t.until,t.important,t.active
		FROM tasks t LEFT JOIN users u ON u.id=t.user_id `+where+`
		ORDER BY t.active DESC, t.kind='once', t.at='', t.at, t.title`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []schedule.Task
	for rows.Next() {
		var t schedule.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Notes, &t.UserID, &t.Who, &t.Kind, &t.Date, &t.Weekdays, &t.Every, &t.Mday,
			&t.StartDate, &t.EndDate, &t.At, &t.Until, &t.Important, &t.Active); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tasksFor returns the tasks on worker uid's sheet for day d.
func (s *Server) tasksFor(uid int64, d time.Time) ([]schedule.Task, error) {
	all, err := s.loadTasks(`WHERE t.active=1 AND (t.user_id IS NULL OR t.user_id=?)`, uid)
	return schedule.ForDay(all, d), err
}

type option struct {
	Value int
	Label string
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request, u *user) {
	all, err := s.loadTasks("")
	if err != nil {
		fail(w, err)
		return
	}
	workers, err := s.listUsers("WHERE role='worker' AND active=1")
	if err != nil {
		fail(w, err)
		return
	}
	now := time.Now()
	f := schedule.Task{Kind: schedule.Daily, Weekdays: "123456", Every: 2, Mday: 1, Date: now.Format(dayFmt), Active: true}
	if id := pathID(r); id != 0 {
		found := false
		for _, t := range all {
			if t.ID == id {
				f, found = t, true
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}

	// Preview: what one worker's sheet looks like on a chosen day.
	pday := parseDay(r.FormValue("pdate"), now)
	puser, _ := strconv.ParseInt(r.FormValue("puser"), 10, 64)
	if puser == 0 && len(workers) > 0 {
		puser = workers[0].ID
	}
	var preview []schedule.Task
	if puser != 0 {
		if preview, err = s.tasksFor(puser, pday); err != nil {
			fail(w, err)
			return
		}
	}
	var active int
	for _, t := range all {
		if t.Active {
			active++
		}
	}
	longDays := make([]string, 7)
	for i := range longDays {
		longDays[i] = time.Weekday(i).String()
	}
	s.render(w, r, "tasks", data{"U": u, "Nav": "tasks", "Title": "Tasks", "Tasks": all, "ActiveCount": active, "Workers": workers, "F": f,
		"Kinds": schedule.Kinds, "Days": schedule.ShortDays, "LongDays": longDays,
		"Nths":  []option{{1, "First"}, {2, "Second"}, {3, "Third"}, {4, "Fourth"}, {-1, "Last"}},
		"PDate": pday.Format(dayFmt), "PUser": puser, "Preview": preview})
}

// taskFromForm reads only the fields that belong to the chosen repeat kind.
func taskFromForm(r *http.Request) (schedule.Task, error) {
	r.ParseForm()
	t := schedule.Task{Title: r.FormValue("title"), Notes: r.FormValue("notes"), Kind: r.FormValue("kind"),
		At: r.FormValue("at"), Until: r.FormValue("until"), StartDate: r.FormValue("start_date"), EndDate: r.FormValue("end_date"),
		Important: r.FormValue("important") == "on", Active: true}
	t.UserID, _ = strconv.ParseInt(r.FormValue("user"), 10, 64)
	switch t.Kind {
	case schedule.Weekly:
		t.Weekdays = strings.Join(r.Form["wd"], "")
	case schedule.Interval:
		t.Every, t.Date = formInt(r, "every"), r.FormValue("from")
	case schedule.Monthly:
		t.Mday = formInt(r, "mday")
	case schedule.Nth:
		t.Mday, t.Weekdays = formInt(r, "nth"), r.FormValue("nthday")
	case schedule.Once:
		t.Date = r.FormValue("date")
	}
	return t, t.Validate()
}

func (s *Server) saveTask(w http.ResponseWriter, r *http.Request, u *user) {
	id := pathID(r)
	page := "/admin/tasks"
	if id != 0 {
		page += "/" + strconv.FormatInt(id, 10)
	}
	t, err := taskFromForm(r)
	if err != nil {
		back(w, r, page, err, "")
		return
	}
	var who any
	if t.UserID > 0 {
		who = t.UserID
	}
	args := []any{t.Title, t.Notes, who, t.Kind, t.Date, t.Weekdays, t.Every, t.Mday, t.StartDate, t.EndDate, t.At, t.Until, t.Important}
	kind := "admin.task_create"
	if id == 0 {
		_, err = s.db.Exec(`INSERT INTO tasks(title,notes,user_id,kind,date,weekdays,every,mday,start_date,end_date,at,until,important)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	} else {
		kind = "admin.task_update"
		_, err = s.db.Exec(`UPDATE tasks SET title=?,notes=?,user_id=?,kind=?,date=?,weekdays=?,every=?,mday=?,start_date=?,end_date=?,
			at=?,until=?,important=? WHERE id=?`, append(args, id)...)
	}
	if err != nil {
		back(w, r, page, err, "")
		return
	}
	s.event(r, u.ID, u.Name, kind, t.Title+" · "+t.Schedule())
	back(w, r, "/admin/tasks", nil, "Saved “"+t.Title+"”")
}

func (s *Server) toggleTask(w http.ResponseWriter, r *http.Request, u *user) {
	var title string
	var active bool
	err := s.db.QueryRow(`UPDATE tasks SET active=NOT active WHERE id=? RETURNING title, active`, pathID(r)).Scan(&title, &active)
	if err != nil {
		back(w, r, "/admin/tasks", err, "")
		return
	}
	msg := "Paused “" + title + "”"
	if active {
		msg = "Resumed “" + title + "”"
	}
	s.event(r, u.ID, u.Name, "admin.task_toggle", msg)
	back(w, r, "/admin/tasks", nil, msg)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request, u *user) {
	var title string
	if err := s.db.QueryRow(`DELETE FROM tasks WHERE id=? RETURNING title`, pathID(r)).Scan(&title); err != nil {
		back(w, r, "/admin/tasks", err, "")
		return
	}
	s.event(r, u.ID, u.Name, "admin.task_delete", title)
	back(w, r, "/admin/tasks", nil, "Deleted “"+title+"”")
}

// adminSheet prints any worker's sheet for any day, e.g. tomorrow's in advance.
func (s *Server) adminSheet(w http.ResponseWriter, r *http.Request, u *user) {
	uid, _ := strconv.ParseInt(r.FormValue("user"), 10, 64)
	var name string
	if err := s.db.QueryRow(`SELECT name FROM users WHERE id=?`, uid).Scan(&name); err != nil {
		http.NotFound(w, r)
		return
	}
	day := parseDay(r.FormValue("date"), time.Now())
	tasks, err := s.tasksFor(uid, day)
	if err != nil {
		fail(w, err)
		return
	}
	s.render(w, r, "sheet", data{"Name": name, "Date": day, "Arrived": int64(0), "Tasks": tasks, "Blank": make([]int, 3),
		"Back": "/admin/tasks?pdate=" + day.Format(dayFmt) + "&puser=" + strconv.FormatInt(uid, 10)})
}
