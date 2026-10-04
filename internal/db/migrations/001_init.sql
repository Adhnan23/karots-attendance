-- Shop computers/laptops allowed for worker logins. The browser keeps the raw token in a cookie.
CREATE TABLE devices(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT UNIQUE NOT NULL,
  created INTEGER NOT NULL,
  last_seen INTEGER
);

CREATE TABLE users(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  phone TEXT UNIQUE NOT NULL,
  pw TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('admin','worker')),
  active INTEGER NOT NULL DEFAULT 1,
  start_at TEXT NOT NULL DEFAULT ''        -- expected arrival "HH:MM"; later = late
);

-- No rows for a worker = any registered device. No ON DELETE: an assigned device can't be removed.
CREATE TABLE user_devices(
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id INTEGER NOT NULL REFERENCES devices(id),
  PRIMARY KEY(user_id, device_id)
);

CREATE TABLE logins(
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires INTEGER NOT NULL
);

-- One row per work session (login .. End day). Times are unix seconds.
CREATE TABLE shifts(
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  day TEXT NOT NULL,                       -- YYYY-MM-DD local
  started INTEGER NOT NULL,
  ended INTEGER,
  printed INTEGER,                         -- when the task sheet was first printed
  edited INTEGER NOT NULL DEFAULT 0        -- changed/added by an admin
);
CREATE INDEX shifts_day ON shifts(day);

CREATE TABLE breaks(
  id INTEGER PRIMARY KEY,
  shift_id INTEGER NOT NULL REFERENCES shifts(id) ON DELETE CASCADE,
  started INTEGER NOT NULL,
  ended INTEGER
);
CREATE INDEX breaks_shift ON breaks(shift_id);

-- See internal/schedule for what each kind uses.
CREATE TABLE tasks(
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,  -- NULL = all workers
  kind TEXT NOT NULL CHECK(kind IN ('daily','weekly','interval','monthly','nth','once')),
  date TEXT NOT NULL DEFAULT '',
  weekdays TEXT NOT NULL DEFAULT '',
  every INTEGER NOT NULL DEFAULT 0,
  mday INTEGER NOT NULL DEFAULT 0,
  start_date TEXT NOT NULL DEFAULT '',
  end_date TEXT NOT NULL DEFAULT '',
  at TEXT NOT NULL DEFAULT '',
  until TEXT NOT NULL DEFAULT '',
  important INTEGER NOT NULL DEFAULT 0,
  active INTEGER NOT NULL DEFAULT 1
);

-- Activity log: logins, refusals, attendance actions and admin changes.
CREATE TABLE events(
  id INTEGER PRIMARY KEY,
  at INTEGER NOT NULL,
  user_id INTEGER,
  who TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_at ON events(at);

CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
