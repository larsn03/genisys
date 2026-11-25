// Package store is the SQLite persistence layer (SPEC §6.1/§6.2): projects,
// sessions, messages, the events ledger, checkpoint index and permissions.
// Driver is modernc.org/sqlite (pure Go, no CGO — SPEC §17).
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps the sqlite handle.
type DB struct{ h *sql.DB }

// Open opens (creating if needed) the database at path with WAL,
// busy_timeout and foreign keys on (SPEC §6.1).
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := mkdirAll(dir); err != nil {
			return nil, err
		}
	}
	h, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := h.Exec(p); err != nil {
			h.Close()
			return nil, fmt.Errorf("store: %s: %w", p, err)
		}
	}
	// single writer keeps SQLITE_BUSY rare; the busy_timeout covers reads.
	h.SetMaxOpenConns(1)
	return &DB{h: h}, nil
}

// Close closes the handle.
func (db *DB) Close() error { return db.h.Close() }

// --- migrations --------------------------------------------------------------

// Migrate applies all pending embedded migrations in order.
func (db *DB) Migrate() error {
	if _, err := db.h.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (
		   version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	cur := 0
	if err := db.h.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&cur); err != nil {
		return err
	}
	apps, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, a := range apps {
		name := a.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(name, "%04d_", &v); err != nil {
			return fmt.Errorf("store: bad migration name %q", name)
		}
		if v <= cur {
			continue
		}
		up, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := db.apply(v, string(up)); err != nil {
			return fmt.Errorf("store: migrate %s: %w", name, err)
		}
	}
	return nil
}

func (db *DB) apply(version int, up string) error {
	tx, err := db.h.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(up); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, version); err != nil {
		return err
	}
	return tx.Commit()
}

// MigrateDown rolls back to the given version (0 = empty database). Used by
// tests and future schema changes.
func (db *DB) MigrateDown(to int) error {
	cur := 0
	if err := db.h.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&cur); err != nil {
		return err
	}
	for v := cur; v > to; v-- {
		down, err := migrationsFS.ReadFile(fmt.Sprintf("migrations/%04d_*.down.sql", v))
		_ = down
		if err != nil {
			// glob fallback
			names, _ := migrationsFS.ReadDir("migrations")
			for _, n := range names {
				if strings.HasPrefix(n.Name(), fmt.Sprintf("%04d_", v)) && strings.HasSuffix(n.Name(), ".down.sql") {
					down, err = migrationsFS.ReadFile("migrations/" + n.Name())
					break
				}
			}
			if err != nil {
				return fmt.Errorf("store: no down migration for %d", v)
			}
		}
		tx, err := db.h.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(down)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migrate down %d: %w", v, err)
		}
		if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version=?`, v); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion returns the applied schema version.
func (db *DB) SchemaVersion() int {
	var v int
	db.h.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v
}
