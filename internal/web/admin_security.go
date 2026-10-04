package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/auth"
)

// ---------- devices ----------

type device struct {
	ID                int64
	Name              string
	Created, LastSeen int64
	Assigned          int
}

func (s *Server) listDevices() ([]device, error) {
	rows, err := s.db.Query(`SELECT d.id,d.name,d.created,COALESCE(d.last_seen,0),
		(SELECT COUNT(*) FROM user_devices WHERE device_id=d.id) FROM devices d ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []device
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.ID, &d.Name, &d.Created, &d.LastSeen, &d.Assigned); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Server) thisDevice(r *http.Request) string {
	var name string
	if c, err := r.Cookie("dev"); err == nil {
		s.db.QueryRow(`SELECT name FROM devices WHERE token_hash=?`, auth.HashToken(c.Value)).Scan(&name)
	}
	return name
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request, u *user) {
	devs, err := s.listDevices()
	if err != nil {
		fail(w, err)
		return
	}
	s.render(w, r, "devices", data{"U": u, "Nav": "devices", "Title": "Devices", "Devices": devs, "Current": s.thisDevice(r)})
}

// createDevice registers the browser the admin is using right now as a shop device.
func (s *Server) createDevice(w http.ResponseWriter, r *http.Request, u *user) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 60 {
		back(w, r, "/admin/devices", errors.New("give the device a name (up to 60 characters)"), "")
		return
	}
	if cur := s.thisDevice(r); cur != "" {
		back(w, r, "/admin/devices", errors.New("this browser is already registered as "+cur), "")
		return
	}
	tok := auth.NewToken()
	if _, err := s.db.Exec(`INSERT INTO devices(name,token_hash,created) VALUES(?,?,?)`, name, auth.HashToken(tok), time.Now().Unix()); err != nil {
		fail(w, err)
		return
	}
	s.setCookie(w, r, "dev", tok, time.Now().AddDate(10, 0, 0))
	s.event(r, u.ID, u.Name, "admin.device_add", name)
	back(w, r, "/admin/devices", nil, "This browser is now registered as "+name)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request, u *user) {
	var name string
	err := s.db.QueryRow(`DELETE FROM devices WHERE id=? RETURNING name`, pathID(r)).Scan(&name)
	if err != nil {
		back(w, r, "/admin/devices", errors.New("this device is still assigned to a worker — untick it in Users first"), "")
		return
	}
	s.event(r, u.ID, u.Name, "admin.device_remove", name)
	back(w, r, "/admin/devices", nil, "Removed "+name)
}

// ---------- security settings & activity log ----------

var eventGroups = map[string]string{
	"security":   `kind IN ('login_failed','login_refused','login_locked')`,
	"attendance": `kind IN ('login','arrive','break','resume','end','print')`,
	"admin":      `kind LIKE 'admin.%'`,
}

func (s *Server) security(w http.ResponseWriter, r *http.Request, u *user) {
	ips, err := s.setting("worker_ips")
	if err != nil {
		fail(w, err)
		return
	}
	group := r.FormValue("g")
	where, ok := eventGroups[group]
	if !ok {
		group, where = "", "1=1"
	}
	type ev struct {
		At                    int64
		Who, IP, Kind, Detail string
	}
	rows, err := s.db.Query(`SELECT at,who,ip,kind,detail FROM events WHERE ` + where + ` ORDER BY id DESC LIMIT 300`)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	var events []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.At, &e.Who, &e.IP, &e.Kind, &e.Detail); err != nil {
			fail(w, err)
			return
		}
		events = append(events, e)
	}
	var sessions int
	s.db.QueryRow(`SELECT COUNT(*) FROM logins WHERE expires>?`, time.Now().Unix()).Scan(&sessions)
	s.render(w, r, "security", data{"U": u, "Nav": "security", "Title": "Security", "IPs": ips, "IP": s.ip(r),
		"Events": events, "Group": group, "Sessions": sessions})
}

func (s *Server) saveSecurity(w http.ResponseWriter, r *http.Request, u *user) {
	switch r.FormValue("action") {
	case "ips":
		ips := strings.TrimSpace(r.FormValue("worker_ips"))
		if bad := auth.ValidIPList(ips); bad != "" {
			back(w, r, "/admin/security", errors.New("not an IP address or range: "+bad), "")
			return
		}
		if _, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES('worker_ips',?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, ips); err != nil {
			fail(w, err)
			return
		}
		s.event(r, u.ID, u.Name, "admin.network_lock", ips)
		back(w, r, "/admin/security", nil, "Network lock saved")
	case "logout_all":
		if _, err := s.db.Exec(`DELETE FROM logins WHERE token_hash<>?`, s.currentToken(r)); err != nil {
			fail(w, err)
			return
		}
		s.event(r, u.ID, u.Name, "admin.logout_all", "")
		back(w, r, "/admin/security", nil, "Everyone else has been logged out")
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}
