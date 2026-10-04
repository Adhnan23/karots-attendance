package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/auth"
)

type handler func(http.ResponseWriter, *http.Request, *user)

func (s *Server) current(r *http.Request) *user {
	c, err := r.Cookie("s")
	if err != nil {
		return nil
	}
	var u user
	err = s.db.QueryRow(`SELECT u.id,u.name,u.phone,u.role,u.start_at FROM logins l JOIN users u ON u.id=l.user_id
		WHERE l.token_hash=? AND l.expires>? AND u.active=1`, auth.HashToken(c.Value), time.Now().Unix()).
		Scan(&u.ID, &u.Name, &u.Phone, &u.Role, &u.StartAt)
	if err != nil {
		return nil
	}
	return &u
}

func (s *Server) currentToken(r *http.Request) string {
	if c, err := r.Cookie("s"); err == nil {
		return auth.HashToken(c.Value)
	}
	return ""
}

// workerGate decides whether a worker may use this browser. It must carry a registered
// device cookie (one of the worker's assigned devices, if any are assigned) and, when an
// IP allowlist is set, come from the shop network. Returns the device name, or why not.
func (s *Server) workerGate(r *http.Request, uid int64) (device, why string) {
	ips, err := s.setting("worker_ips")
	if err != nil {
		return "", "server error"
	}
	if ips != "" && !auth.IPAllowed(s.ip(r), ips) {
		return "", "network " + s.ip(r) + " not allowed"
	}
	c, err := r.Cookie("dev")
	if err != nil {
		return "", "unregistered device"
	}
	var ok bool
	err = s.db.QueryRow(`SELECT name, NOT EXISTS(SELECT 1 FROM user_devices WHERE user_id=?)
		OR EXISTS(SELECT 1 FROM user_devices WHERE user_id=? AND device_id=devices.id)
		FROM devices WHERE token_hash=?`, uid, uid, auth.HashToken(c.Value)).Scan(&device, &ok)
	switch {
	case err != nil:
		return "", "unregistered device"
	case !ok:
		return "", "device " + device + " not assigned to this worker"
	}
	return device, ""
}

// require lets only logged-in users of role through; workers also pass the device gate on every request.
func (s *Server) require(role string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.current(r)
		if u == nil || u.Role != role {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if role == "worker" {
			if _, why := s.workerGate(r, u.ID); why != "" {
				http.Error(w, "Not allowed here ("+why+"). Use the shop computer.", http.StatusForbidden)
				return
			}
		}
		h(w, r, u)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	switch u := s.current(r); {
	case u == nil:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case u.Role == "admin":
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/me", http.StatusSeeOther)
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.current(r) != nil {
		s.index(w, r)
		return
	}
	s.render(w, r, "login", data{"Title": "Log in", "Phone": ""})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	now, ip := time.Now(), s.ip(r)
	phone := normPhone(r.FormValue("phone"))
	page := func(msg string) { s.render(w, r, "login", data{"Title": "Log in", "Err": msg, "Phone": phone}) }
	if s.lim.Blocked(ip, now) {
		s.event(r, 0, phone, "login_locked", "")
		page("Too many failed attempts. Try again in 15 minutes.")
		return
	}
	var id int64
	var role, pw, name string
	err := s.db.QueryRow(`SELECT id,role,pw,name FROM users WHERE phone=? AND active=1`, phone).Scan(&id, &role, &pw, &name)
	if err != nil {
		pw = auth.DummyHash
	}
	if !auth.CheckPassword(pw, r.FormValue("password")) || err != nil {
		s.lim.Fail(ip, now)
		s.event(r, 0, phone, "login_failed", "")
		page("Wrong phone number or password.")
		return
	}
	device, life := "", 7*24*time.Hour
	if role == "worker" {
		var why string
		if device, why = s.workerGate(r, id); why != "" {
			s.event(r, id, name, "login_refused", why)
			page("You can't log in from this device. Use the shop computer.")
			return
		}
		// Ends at midnight, so a forgotten open /me page can't auto-refresh into a fake arrival tomorrow.
		// ponytail: no shifts across midnight; give night workers a longer life if the shop ever has them.
		y, m, d := now.Date()
		life = min(16*time.Hour, time.Date(y, m, d+1, 0, 0, 0, 0, time.Local).Sub(now))
	}
	s.lim.Reset(ip)
	tok, exp := auth.NewToken(), now.Add(life)
	if _, err := s.db.Exec(`DELETE FROM logins WHERE expires<?`, now.Unix()); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.db.Exec(`INSERT INTO logins(token_hash,user_id,expires) VALUES(?,?,?)`, auth.HashToken(tok), id, exp.Unix()); err != nil {
		fail(w, err)
		return
	}
	s.event(r, id, name, "login", device)
	target := "/admin"
	if role == "worker" {
		target = "/me"
		s.db.Exec(`UPDATE devices SET last_seen=? WHERE name=?`, now.Unix(), device)
		if _, err := s.openShift(r, &user{ID: id, Name: name}, now); err != nil { // login = arrival
			fail(w, err)
			return
		}
	}
	s.setCookie(w, r, "s", tok, exp)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if u := s.current(r); u != nil && u.Role == "worker" {
		var open int
		s.db.QueryRow(`SELECT COUNT(*) FROM shifts WHERE user_id=? AND ended IS NULL`, u.ID).Scan(&open)
		if open > 0 {
			s.event(r, u.ID, u.Name, "logout_no_end", "logged out without End day; task reasons skipped")
		}
	}
	if tok := s.currentToken(r); tok != "" {
		s.db.Exec(`DELETE FROM logins WHERE token_hash=?`, tok)
	}
	s.setCookie(w, r, "s", "", time.Unix(1, 0))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func normPhone(p string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, strings.TrimSpace(p))
}
