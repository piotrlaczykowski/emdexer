package watcher

import (
	"database/sql"
	"fmt"
)

const currentSchemaVersion = 1

var migrations = []func(*sql.Tx) error{
	func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS file_cache (
			path         TEXT PRIMARY KEY,
			size         INTEGER,
			mtime        INTEGER,
			partial_hash TEXT,
			full_hash    TEXT,
			algorithm    TEXT,
			last_seen    INTEGER
		)`)
		if err != nil {
			return err
		}
		needed := []struct{ name, defn string }{
			{"partial_hash", "TEXT"},
			{"full_hash", "TEXT"},
			{"algorithm", "TEXT"},
			{"last_seen", "INTEGER"},
		}
		for _, c := range needed {
			if err := addColumnIfMissing(tx, "file_cache", c.name, c.defn); err != nil {
				return err
			}
		}
		return nil
	},
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}

	for v := current + 1; v <= currentSchemaVersion; v++ {
		idx := v - 1
		if idx < 0 || idx >= len(migrations) {
			return fmt.Errorf("missing migration for version %d", v)
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := migrations[idx](tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration v%d: %w", v, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_version (version, applied_at) VALUES (?, strftime('%s','now'))`, v,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record v%d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(tx *sql.Tx, table, col, defn string) error {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	_, err = tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, defn))
	return err
}
