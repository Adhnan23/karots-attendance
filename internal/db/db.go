// Package db opens the SQLite database and applies migrations.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	_ "modernc.org/sqlite" // pure-Go driver: keeps the binary static
)

// Migrations live in migrations/NNN_name.sql and run once each, in file-name order,
// tracked by PRAGMA user_version. Never edit a shipped file; add a new one.
//
//go:embed migrations/*.sql
var migrations embed.FS

// Open opens (creating if needed) the database at path and brings its schema up to date.
// Use ":memory:" for tests.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // ponytail: one connection serialises writes; plenty for a shop
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	names, err := fs.Glob(migrations, "migrations/*.sql") // sorted
	if err != nil {
		return err
	}
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for ; v < len(names); v++ {
		src, err := migrations.ReadFile(names[v])
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(src)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", names[v], err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
