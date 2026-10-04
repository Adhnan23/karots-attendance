package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Adhnan23/karots-attendance/internal/auth"
	"github.com/Adhnan23/karots-attendance/internal/db"
)

type browser struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newBrowser(t *testing.T, srv *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t, srv, &http.Client{Jar: jar}}
}

// do returns "STATUS PATH BODY" so tests can match on any of them.
func (b *browser) do(method, path string, kv ...string) string {
	b.t.Helper()
	f := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		f.Add(kv[i], kv[i+1])
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, strings.NewReader(f.Encode()))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.Status + " " + res.Request.URL.Path + " " + string(body)
}

func (b *browser) get(path string) string { b.t.Helper(); return b.do("GET", path) }
func (b *browser) post(path string, kv ...string) string {
	b.t.Helper()
	return b.do("POST", path, kv...)
}

func expect(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("want %q in:\n%.600s", want, got)
		}
	}
}

func TestFlow(t *testing.T) {
	conn, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec(`INSERT INTO users(name,phone,pw,role) VALUES('Owner','0700000000',?,'admin')`, auth.HashPassword("secret123"))
	srv := httptest.NewServer(New(conn, false).Handler())
	defer srv.Close()

	// --- admin sets up the shop PC, a worker and tasks ---
	shop := newBrowser(t, srv)
	expect(t, shop.post("/login", "phone", "070 000 0000", "password", "secret123"), "200 OK /admin ", "Today")
	expect(t, shop.post("/admin/devices", "name", "Counter PC"), "registered as <b>Counter PC</b>")
	expect(t, shop.post("/admin/devices", "name", "Again"), "already registered")
	expect(t, shop.post("/admin/users", "name", "Ali", "phone", "0711111111", "password", "123", "role", "worker"), "at least 6")
	expect(t, shop.post("/admin/users", "name", "Ali", "phone", "0711111111", "password", "ali123", "role", "worker", "device", "1", "start_at", "00:00"), "Saved Ali")
	expect(t, shop.post("/admin/users", "name", "Dup", "phone", "0711111111", "password", "dup123", "role", "worker"), "already used")
	expect(t, shop.post("/admin/tasks", "title", "Open shutters", "kind", "daily", "at", "00:00", "until", "00:15", "important", "on"), "Saved")
	expect(t, shop.post("/admin/tasks", "title", "Stock count", "kind", "monthly", "mday", "-1"), "Monthly on the last day")
	expect(t, shop.post("/admin/tasks", "title", "Bad", "kind", "weekly"), "pick at least one weekday")
	today := time.Now().Format(dayFmt)
	expect(t, shop.post("/admin/tasks", "title", "Festival display", "kind", "once", "date", today, "notes", "Use the red banners"), "Once on")
	tomorrow := time.Now().AddDate(0, 0, 1).Format(dayFmt)
	expect(t, shop.post("/admin/tasks", "title", "Lock up", "kind", "daily", "at", "23:59"), "Saved")
	expect(t, shop.post("/admin/tasks", "title", "Fix shelf", "kind", "movable", "first", today, "days", "2"), "moves on until done")
	expect(t, shop.get("/admin/tasks?puser=2&pdate="+tomorrow), "Fix shelf", "Moves to the next day")
	expect(t, shop.get("/admin/tasks?pdate="+today), "Open shutters", "Festival display", "Print this sheet")
	expect(t, shop.get("/admin/sheet?user=2&date="+today), "Daily Task Sheet", "★ Open shutters", "Use the red banners", "Worker signature")
	expect(t, shop.get("/admin/tasks/1"), "Edit task", `value="Open shutters"`)
	expect(t, shop.post("/admin/tasks/2/toggle"), "Paused")
	expect(t, shop.get("/admin/users/2"), "Edit Ali")
	expect(t, shop.post("/logout"), "/login ")

	// --- the worker's own phone is refused; wrong passwords lock out ---
	phone := newBrowser(t, srv)
	expect(t, phone.post("/login", "phone", "0711111111", "password", "ali123"), "log in from this device")
	expect(t, phone.get("/me"), "/login ")

	// --- worker day on the shop PC ---
	expect(t, shop.post("/login", "phone", "0711111111", "password", "ali123"), "/me ", "Hi Ali", "Open shutters", "Not printed")
	expect(t, shop.get("/me/print"), "Daily Task Sheet", "Ali", "Festival display")
	expect(t, shop.get("/me"), "✓")
	expect(t, shop.post("/me/pause"), "On break since", "Resume work")
	expect(t, shop.post("/me/pause"), "On break since") // double pause = one break
	expect(t, shop.post("/me/resume"), "Take a break")
	expect(t, shop.get("/admin"), "200 OK /me ") // workers bounce back to their own page
	expect(t, shop.post("/me/tasks/1/done", "answer", "maybe"), `type &#34;yes&#34;`)
	expect(t, shop.post("/me/tasks/1/done"), "Did you finish “Open shutters”?", `name="answer"`) // no JavaScript
	expect(t, shop.post("/me/tasks/4/done", "answer", "yes"), "starts at 23:59")                 // too early to tick
	expect(t, shop.post("/me/tasks/1/done", "answer", "no"), "☐ Done")
	expect(t, shop.post("/me/tasks/1/done", "answer", " YES "), "✓ Open shutters", "✓ Done")
	expect(t, shop.post("/me/tasks/1/undo"), "Unticked")
	expect(t, shop.post("/me/tasks/1/done", "answer", "Yes"), "✓ Done")
	expect(t, shop.post("/me/tasks/2/done", "answer", "yes"), "404") // paused, not on today's list
	end := shop.post("/me/end")
	expect(t, end, "/me/end ", "Before you go", `name="why3"`, `name="why4"`)
	if strings.Contains(end, `name="why5"`) {
		t.Fatal("movable task asked for a reason before its last day")
	}
	expect(t, shop.post("/me/tasks/5/done", "answer", "yes"), "✓ Fix shelf")
	expect(t, shop.post("/me/end", "why3", "  "), "Before you go")                    // blank reason doesn't count
	expect(t, shop.post("/me/end", "why3", "ok", "why4", "."), "at least 10 letters") // nor a token one
	expect(t, shop.post("/me/end", "why3", "No red banners in stock", "why4", "Owner locked up tonight"), "/login ")
	expect(t, shop.get("/me"), "/login ")

	// --- admin reviews ---
	expect(t, shop.post("/login", "phone", "0700000000", "password", "secret123"), "/admin ", "Ali", "Left", "late",
		"Movable tasks", "Fix shelf", "✓ Done")
	expect(t, shop.get("/admin/tasks?puser=2&pdate="+today), "✓ Done", "No red banners in stock")
	if strings.Contains(shop.get("/admin/tasks?puser=2&pdate="+tomorrow), "Moves to the next day") {
		t.Fatal("ticked movable task still moves to tomorrow")
	}
	expect(t, shop.post("/admin/tasks", "title", "Old job", "kind", "movable", "first", time.Now().AddDate(0, 0, -3).Format(dayFmt), "days", "2"), "Saved")
	expect(t, shop.post("/admin/tasks", "title", "Paint door", "kind", "movable", "first", time.Now().AddDate(0, 0, -1).Format(dayFmt), "days", "3"), "Saved")
	// A daily task ticked today, then turned into a movable one starting tomorrow, still shows tomorrow.
	expect(t, shop.post("/admin/tasks/1", "title", "Open shutters", "kind", "movable", "first", tomorrow, "days", "2"), "Saved")
	preview, _, _ := strings.Cut(shop.get("/admin/tasks?puser=2&pdate="+tomorrow), "<h3>All tasks</h3>")
	expect(t, preview, "Open shutters")
	expect(t, shop.get("/admin/security?g=tasks"), "Task done", "Task unticked", "Task not done", "No red banners in stock")
	// Ali joined today, after "Old job" started, so it isn't theirs; backdate the join and it is.
	if strings.Contains(shop.get("/admin"), "Old job") {
		t.Fatal("movable task from before a worker joined was given to them")
	}
	conn.Exec(`UPDATE users SET created='2020-01-01' WHERE id=2`)
	expect(t, shop.get("/admin"), "Old job", "Expired, not done", "Paint door", "Moving, not done yet", "1 day(s)")
	rep := shop.get("/admin/report")
	expect(t, rep, "By worker", "Attendance grid", "Sessions", "Edit", "Tasks CSV", "Task not done", "Owner locked up tonight")
	expect(t, shop.get("/admin/report.csv?tasks=1&from="+today+"&to="+today), "Date,Worker,Task,Time,Status,Done at,Reason",
		",Ali,Festival display,,not done,,No red banners in stock")
	expect(t, shop.get("/admin/report?from=2026-01-01&to=2026-12-31"), "62 days or fewer")
	csv := shop.get("/admin/report.csv?from=" + today + "&to=" + today)
	expect(t, csv, "Date,Worker,Arrived", today+",Ali,")
	expect(t, shop.get("/admin/shifts/1"), "Edit session", "Breaks")
	expect(t, shop.post("/admin/shifts/1", "in", "2020-01-01T08:00"), "past session needs a leave time")
	expect(t, shop.post("/admin/shifts/1", "in", today+"T08:00", "out", today+"T07:00"), "after arrival")
	expect(t, shop.post("/admin/shifts", "user", "2", "in", "2026-01-05T08:00", "out", "2026-01-05T17:00"), "Session added for Ali")
	expect(t, shop.get("/admin/report?from=2026-01-05&to=2026-01-05"), "9h 00m", "edited")
	expect(t, shop.post("/admin/devices/1/delete"), "still assigned")
	expect(t, shop.get("/admin/security"), "Blocked device", "Arrived", "Logged in", "task create")
	expect(t, shop.post("/admin/security", "action", "ips", "worker_ips", "nonsense"), "not an IP")
	expect(t, shop.post("/admin/users/1", "name", "Owner", "phone", "0700000000", "role", "worker", "active", "on"), "deactivate or demote yourself")

	// --- network lock: the shop PC (127.0.0.1) is outside the allowed range ---
	expect(t, shop.post("/admin/security", "action", "ips", "worker_ips", "203.0.113.0/24"), "Network lock saved")
	expect(t, newBrowserWithCookies(t, srv, shop).post("/login", "phone", "0711111111", "password", "ali123"), "log in from this device")
	expect(t, shop.post("/admin/security", "action", "ips", "worker_ips", "127.0.0.1"), "Network lock saved")

	// --- a worker skipping End day by posting /logout by hand is flagged ---
	tab := newBrowserWithCookies(t, srv, shop)
	expect(t, tab.post("/login", "phone", "0711111111", "password", "ali123"), "/me ")
	expect(t, tab.post("/logout"), "/login ")
	expect(t, shop.get("/admin/security?g=attendance"), "Left without End day")

	// --- headers, CSRF and lockout ---
	res, _ := http.Get(srv.URL + "/login")
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing security headers")
	}
	req, _ := http.NewRequest("POST", srv.URL+"/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST should be rejected, got %d", res.StatusCode)
	}
	attacker := newBrowser(t, srv)
	for range 5 {
		expect(t, attacker.post("/login", "phone", "0700000000", "password", "guess"), "Wrong phone number or password")
	}
	expect(t, attacker.post("/login", "phone", "0700000000", "password", "secret123"), "Too many failed attempts")
	expect(t, res.Header.Get("Cache-Control"), "no-store")
	expect(t, shop.get("/static/pico.min.css"), "Pico CSS")
}

// newBrowserWithCookies is a second tab on the same browser (shares the device cookie, not the login).
func newBrowserWithCookies(t *testing.T, srv *httptest.Server, from *browser) *browser {
	b := newBrowser(t, srv)
	u, _ := url.Parse(srv.URL)
	for _, c := range from.c.Jar.Cookies(u) {
		if c.Name == "dev" {
			b.c.Jar.SetCookies(u, []*http.Cookie{c})
		}
	}
	return b
}
