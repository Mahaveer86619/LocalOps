// Package storage owns the single SQLite database connection and applies
// migrations. It intentionally knows nothing about tasks/watchers - those
// packages own their own SQL.
package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/Mahaveer86619/LocalOps/migrations"
	_ "modernc.org/sqlite" // pure-Go driver, no cgo - cross-compiles cleanly to the mini PC
)

// Open opens (creating if needed) the SQLite database at path and applies
// migrations. path may be ":memory:" for tests.
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("storage: empty database path")
	}
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			// Caller is expected to have created the directory; we don't
			// silently create arbitrary paths here.
		}
	}

	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite + WAL: one writer is simplest and sufficient at this scale

	if _, err := db.Exec(migrations.Init); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: apply migrations: %w", err)
	}
	return db, nil
}
