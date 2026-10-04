// Package web serves the HTML UI: worker attendance pages and the admin area.
package web

import (
	"bytes"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/auth"
	"github.com/Adhnan23/karots-attendance/internal/schedule"
)

//go:embed templates static
var files embed.FS

const dayFmt = "2006-01-02"

type Server struct {
	db    *sql.DB
	tpl   *template.Template
	proxy bool
	lim   *auth.Limiter
}

type data = map[string]any

type user struct {
	ID                         int64
	Name, Phone, Role, StartAt string
	Active                     bool
	Devices                    []int64
}

// New builds the server. proxy=true trusts X-Forwarded-For/-Proto from a reverse proxy.
func New(db *sql.DB, proxy bool) *Server {
	return &Server{db: db, proxy: proxy, lim: auth.NewLimiter(5, 15*time.Minute),
		tpl: template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/*.html"))}
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	m.HandleFunc("GET /{$}", s.index)
	m.HandleFunc("GET /login", s.loginPage)
	m.HandleFunc("POST /login", s.login)
	m.HandleFunc("POST /logout", s.logout)

	worker := func(h handler) http.HandlerFunc { return s.require("worker", h) }
	m.HandleFunc("GET /me", worker(s.home))
	m.HandleFunc("GET /me/print", worker(s.printSheet))
	m.HandleFunc("POST /me/{action}", worker(s.action))

	admin := func(h handler) http.HandlerFunc { return s.require("admin", h) }
	m.HandleFunc("GET /admin", admin(s.today))
	m.HandleFunc("GET /admin/report", admin(s.report))
	m.HandleFunc("GET /admin/report.csv", admin(s.report))
	m.HandleFunc("POST /admin/shifts", admin(s.addShift))
	m.HandleFunc("GET /admin/shifts/{id}", admin(s.shiftPage))
	m.HandleFunc("POST /admin/shifts/{id}", admin(s.saveShift))
	m.HandleFunc("POST /admin/shifts/{id}/delete", admin(s.deleteShift))
	m.HandleFunc("POST /admin/breaks/{id}/delete", admin(s.deleteBreak))
	m.HandleFunc("GET /admin/users", admin(s.users))
	m.HandleFunc("GET /admin/users/{id}", admin(s.users))
	m.HandleFunc("POST /admin/users", admin(s.saveUser))
	m.HandleFunc("POST /admin/users/{id}", admin(s.saveUser))
	m.HandleFunc("GET /admin/tasks", admin(s.tasks))
	m.HandleFunc("GET /admin/tasks/{id}", admin(s.tasks))
	m.HandleFunc("POST /admin/tasks", admin(s.saveTask))
	m.HandleFunc("POST /admin/tasks/{id}", admin(s.saveTask))
	m.HandleFunc("POST /admin/tasks/{id}/toggle", admin(s.toggleTask))
	m.HandleFunc("POST /admin/tasks/{id}/delete", admin(s.deleteTask))
	m.HandleFunc("GET /admin/sheet", admin(s.adminSheet))
	m.HandleFunc("GET /admin/devices", admin(s.devices))
	m.HandleFunc("POST /admin/devices", admin(s.createDevice))
	m.HandleFunc("POST /admin/devices/{id}/delete", admin(s.deleteDevice))
	m.HandleFunc("GET /admin/security", admin(s.security))
	m.HandleFunc("POST /admin/security", admin(s.saveSecurity))

	// CrossOriginProtection rejects cross-site POSTs (CSRF) via Sec-Fetch-Site / Origin.
	return s.headers(http.NewCrossOriginProtection().Handler(m))
}

func (s *Server) headers(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		if s.https(r) {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			hd.Set("Cache-Control", "no-store")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		h.ServeHTTP(w, r)
	})
}

// ---------- request helpers ----------

func (s *Server) https(r *http.Request) bool {
	return r.TLS != nil || s.proxy && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) ip(r *http.Request) string {
	if s.proxy {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			parts := strings.Split(v, ",")
			return strings.TrimSpace(parts[len(parts)-1]) // the entry our own proxy appended
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, val string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: val, Path: "/", Expires: exp, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.https(r)})
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func formInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.FormValue(key))
	return n
}

func parseDay(v string, fallback time.Time) time.Time {
	if d, err := time.ParseInLocation(dayFmt, v, time.Local); err == nil {
		return d
	}
	return time.Date(fallback.Year(), fallback.Month(), fallback.Day(), 0, 0, 0, 0, time.Local)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, d data) {
	for k, v := range map[string]string{"Err": r.URL.Query().Get("err"), "Msg": r.URL.Query().Get("ok"), "Nav": "", "Title": ""} {
		if d[k] == nil {
			d[k] = v
		}
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, d); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
}

// back redirects to path with an error or a success flash message.
func back(w http.ResponseWriter, r *http.Request, path string, err error, ok string) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "UNIQUE") && strings.Contains(msg, "phone"):
			msg = "that phone number is already used"
		case strings.Contains(msg, "constraint"):
			log.Print(err)
			msg = "that change isn't allowed"
		}
		path += sep + "err=" + url.QueryEscape(msg)
	} else if ok != "" {
		path += sep + "ok=" + url.QueryEscape(ok)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (s *Server) event(r *http.Request, uid int64, who, kind, detail string) {
	var id any
	if uid > 0 {
		id = uid
	}
	if _, err := s.db.Exec(`INSERT INTO events(at,user_id,who,ip,kind,detail) VALUES(?,?,?,?,?,?)`,
		time.Now().Unix(), id, who, s.ip(r), kind, detail); err != nil {
		log.Print(err)
	}
}

func (s *Server) setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// ---------- template functions ----------

var statuses = map[string][2]string{ // status → label, badge colour
	"working": {"Working", "green"}, "on break": {"On break", "amber"}, "done": {"Left", "blue"},
	"not ended": {"Not ended", "red"}, "absent": {"Not arrived", "gray"},
}

var eventKinds = map[string][2]string{
	"login": {"Logged in", "green"}, "arrive": {"Arrived", "green"}, "break": {"Break", "amber"},
	"resume": {"Resumed", "green"}, "end": {"End day", "blue"}, "print": {"Printed sheet", "blue"},
	"login_failed": {"Wrong password", "red"}, "login_refused": {"Blocked device", "red"}, "login_locked": {"Locked out", "red"},
}

func badge(label, colour string) template.HTML {
	return template.HTML(`<span class="badge ` + colour + `">` + template.HTMLEscapeString(label) + `</span>`)
}

var funcs = template.FuncMap{
	"clock": func(t int64) string { return time.Unix(t, 0).Format("3:04 PM") },
	"date":  func(t int64) string { return time.Unix(t, 0).Format("Mon 02 Jan") },
	"when":  func(t int64) string { return time.Unix(t, 0).Format("02 Jan 3:04 PM") },
	"dtl":   func(t int64) string { return time.Unix(t, 0).Format("2006-01-02T15:04") },
	"dur":   func(s int64) string { return fmt.Sprintf("%dh %02dm", s/3600, s%3600/60) },
	"hours": func(s int64) string { return strconv.FormatFloat(float64(s)/3600, 'f', 1, 64) },
	"inc":   func(i int) int { return i + 1 },
	"has": func(ids []int64, id int64) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	},
	"hasDay": func(wd string, i int) bool { return strings.ContainsRune(wd, rune('0'+i)) },
	"seq": func(a, b int) []int {
		var out []int
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
		return out
	},
	"ordinal": schedule.Ordinal,
	"status": func(st string) template.HTML {
		v := statuses[st]
		return badge(v[0], v[1])
	},
	"eventBadge": func(kind string) template.HTML {
		if v, ok := eventKinds[kind]; ok {
			return badge(v[0], v[1])
		}
		return badge(strings.ReplaceAll(strings.TrimPrefix(kind, "admin."), "_", " "), "gray")
	},
}
