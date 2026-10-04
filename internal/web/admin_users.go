package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Adhnan23/karots-attendance/internal/auth"
)

func (s *Server) listUsers(where string, args ...any) ([]user, error) {
	rows, err := s.db.Query(`SELECT id,name,phone,role,start_at,active,created,
		COALESCE((SELECT group_concat(device_id) FROM user_devices WHERE user_id=users.id),'')
		FROM users `+where+` ORDER BY role DESC, active DESC, name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []user
	for rows.Next() {
		var u user
		var devs string
		if err := rows.Scan(&u.ID, &u.Name, &u.Phone, &u.Role, &u.StartAt, &u.Active, &u.Created, &devs); err != nil {
			return nil, err
		}
		for _, v := range strings.Split(devs, ",") {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				u.Devices = append(u.Devices, id)
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Server) users(w http.ResponseWriter, r *http.Request, u *user) {
	list, err := s.listUsers("")
	if err != nil {
		fail(w, err)
		return
	}
	devs, err := s.listDevices()
	if err != nil {
		fail(w, err)
		return
	}
	f := user{Role: "worker", Active: true}
	if id := pathID(r); id != 0 {
		found := false
		for _, x := range list {
			if x.ID == id {
				f, found = x, true
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	s.render(w, r, "users", data{"U": u, "Nav": "users", "Title": "Users", "Users": list, "Devices": devs, "F": f})
}

func validateUser(f user, pw string, isNew bool) error {
	switch {
	case f.Name == "" || len(f.Name) > 80:
		return errors.New("enter a name (up to 80 characters)")
	case len(f.Phone) < 6 || len(f.Phone) > 20 || strings.IndexFunc(f.Phone, func(r rune) bool { return !unicode.IsDigit(r) && r != '+' }) >= 0:
		return errors.New("enter a valid phone number (digits only)")
	case f.Role != "admin" && f.Role != "worker":
		return errors.New("pick a role")
	case isNew && pw == "":
		return errors.New("set a password")
	case pw != "" && f.Role == "admin" && len(pw) < 8:
		return errors.New("admin passwords need at least 8 characters")
	case pw != "" && len(pw) < 6:
		return errors.New("passwords need at least 6 characters")
	}
	if _, err := time.Parse("15:04", f.StartAt); f.StartAt != "" && err != nil {
		return errors.New("invalid arrival time")
	}
	return nil
}

func (s *Server) saveUser(w http.ResponseWriter, r *http.Request, u *user) {
	r.ParseForm()
	id := pathID(r)
	page := "/admin/users"
	if id != 0 {
		page += "/" + strconv.FormatInt(id, 10)
	}
	f := user{ID: id, Name: strings.TrimSpace(r.FormValue("name")), Phone: normPhone(r.FormValue("phone")),
		Role: r.FormValue("role"), StartAt: r.FormValue("start_at"), Active: id == 0 || r.FormValue("active") == "on"}
	pw := r.FormValue("password")
	if err := validateUser(f, pw, id == 0); err != nil {
		back(w, r, page, err, "")
		return
	}
	if id == u.ID && (!f.Active || f.Role != "admin") {
		back(w, r, page, errors.New("you can't deactivate or demote yourself"), "")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback()
	if id == 0 {
		err = tx.QueryRow(`INSERT INTO users(name,phone,pw,role,start_at,created) VALUES(?,?,?,?,?,?) RETURNING id`,
			f.Name, f.Phone, auth.HashPassword(pw), f.Role, f.StartAt, time.Now().Format(dayFmt)).Scan(&id)
	} else {
		_, err = tx.Exec(`UPDATE users SET name=?, phone=?, role=?, start_at=?, active=? WHERE id=?`, f.Name, f.Phone, f.Role, f.StartAt, f.Active, id)
		if err == nil && pw != "" { // new password: sign out their other sessions
			if _, err = tx.Exec(`UPDATE users SET pw=? WHERE id=?`, auth.HashPassword(pw), id); err == nil {
				_, err = tx.Exec(`DELETE FROM logins WHERE user_id=? AND token_hash<>?`, id, s.currentToken(r))
			}
		}
		if err == nil && !f.Active {
			_, err = tx.Exec(`DELETE FROM logins WHERE user_id=?`, id)
		}
	}
	if err == nil {
		_, err = tx.Exec(`DELETE FROM user_devices WHERE user_id=?`, id)
	}
	for _, d := range r.Form["device"] {
		if err == nil {
			_, err = tx.Exec(`INSERT INTO user_devices(user_id,device_id) SELECT ?,id FROM devices WHERE id=?`, id, d)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		back(w, r, page, err, "")
		return
	}
	kind := "admin.user_update"
	if f.ID == 0 {
		kind = "admin.user_create"
	}
	detail := f.Name + " (" + f.Role + ")"
	if pw != "" && f.ID != 0 {
		detail += " · password changed"
	}
	s.event(r, u.ID, u.Name, kind, detail)
	back(w, r, "/admin/users", nil, "Saved "+f.Name)
}
