-- Add the 'movable' kind: SQLite can't change a CHECK, so rebuild tasks (nothing references it yet).
CREATE TABLE tasks_new(
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,  -- NULL = all workers
  kind TEXT NOT NULL CHECK(kind IN ('daily','weekly','interval','monthly','nth','once','movable')),
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
INSERT INTO tasks_new SELECT * FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

-- Join day, so a movable task only goes to workers who were there on its first day ('' = joined before this).
ALTER TABLE users ADD COLUMN created TEXT NOT NULL DEFAULT '';

-- Worker logins now end at midnight; drop older 16-hour ones so nobody keeps one.
DELETE FROM logins WHERE user_id IN (SELECT id FROM users WHERE role='worker');

-- A worker's answer for one task on one day: done (done_at set) or not done (reason given at End day).
CREATE TABLE task_done(
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day TEXT NOT NULL,                       -- YYYY-MM-DD local
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  done_at INTEGER,
  reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(user_id, day, task_id)
);
