# karots-attendance

Shop attendance in **one static binary + one SQLite file**: workers log in on the shop computer
(that's their arrival time), print an A4 task sheet to sign, take breaks, and end the day. Admins get a
live board, rich reports, task scheduling and an activity log — from any device, phone included.

## Quick start

```sh
make build                                   # ./attendance (static, no cgo, everything embedded)
make admin PHONE=0700000000 PASS='secret123' # create the first admin
make run                                     # http://localhost:8080 (TZ=Asia/Colombo)
make test                                    # vet + all tests
make backup                                  # consistent copy → backups/ (safe while running)
make release                                 # dist/attendance-linux-{amd64,arm64}
```

## Docker

Multi-stage build → a `scratch` image (~20 MB) holding only the static binary. Data lives in `./data` on the host.

```sh
make docker-admin PHONE=0700000000 PASS='secret123'   # first admin (writes ./data/attendance.db)
make up                                               # docker compose up -d --build → :8080
make docker-backup                                    # consistent copy → ./data/backups/
make logs / make down
```

Back up by copying `data/backups/*.db` off the server (e.g. a nightly cron of `make docker-backup`).
Restore: stop, replace `data/attendance.db` with a backup (delete `attendance.db-wal`/`-shm`), start.
If your host user isn't uid 1000, set `APP_UID`/`APP_GID` (see `id -u`). Behind Caddy on the host,
bind to `127.0.0.1:8080:8080` and uncomment `command: ["-proxy"]` in `docker-compose.yml`.

## On the VPS (without Docker)

Copy the binary, then run it behind Caddy (automatic HTTPS):

```sh
TZ=Asia/Colombo ./attendance -addr 127.0.0.1:8080 -db /var/lib/attendance/attendance.db -proxy
```

```caddy
attendance.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

`-proxy` makes it trust `X-Forwarded-For`/`-Proto` from Caddy (for real client IPs, secure cookies and HSTS).
Only use it behind a proxy. Back up the `.db` file (and its `-wal` while running) — that's all the data.

## How it works

**Devices.** A browser can't reveal a MAC address, so devices are registered instead: open
*Devices* on the shop PC/laptop as admin → *Register this browser*. That browser keeps a secret
10-year cookie. Workers can only log in from registered devices — per worker you can tick one or
several (none ticked = any shop device). Admins log in from anywhere.
Optional extra: *Security → Shop network lock* also requires the shop's internet IP.

**Workers.** Login = arrival → print sheet → *Take a break* / *Resume* → *End day* (logs out).

**Tasks.** Every day · weekly on chosen days · every N days · monthly on a date (or last day) ·
Nth weekday of the month (e.g. last Friday) · once on a special day. Optional time window, notes,
"important" flag, active period (e.g. festival season), pause/resume. Preview and print any worker's
sheet for any day.

**Admin.** *Today* live board (auto-refresh) · *Report* with presets, KPIs, per-worker totals,
attendance grid, late arrivals, unprinted sheets, CSV export · edit/add/delete sessions (marked
"edited") · *Security* activity log.

**Security.** PBKDF2 passwords · hashed session/device tokens · 5 failed logins → 15 min lockout per IP ·
CSRF protection (`http.CrossOriginProtection`) · strict CSP, no inline scripts · HttpOnly/SameSite
cookies · server timeouts and body limits · CSV-injection-safe export · audit log of every admin change.

## Layout

```
cmd/attendance/       entry point: flags, `admin` command, graceful shutdown
internal/db/          SQLite open + migrations/NNN_*.sql (applied once, in order)
internal/auth/        passwords, tokens, login limiter, IP allowlist
internal/schedule/    task repeat rules
internal/report/      sessions → hours, late, totals, grid
internal/web/         HTTP handlers (one file per area), templates/, static/ (Pico CSS)
```

Schema changes: add `internal/db/migrations/002_something.sql` — never edit a shipped one.
