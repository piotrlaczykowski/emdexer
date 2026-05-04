package watcher

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrate_EmptyDB_AppliesAll(t *testing.T) {
	db := openTempDB(t)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var v int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&v); err != nil {
		t.Fatalf("schema_version row: %v", err)
	}
	if v != currentSchemaVersion {
		t.Fatalf("got version %d want %d", v, currentSchemaVersion)
	}
	rows, err := db.Query("PRAGMA table_info(file_cache)")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = true
	}
	for _, want := range []string{"path", "size", "mtime", "partial_hash", "full_hash", "algorithm", "last_seen"} {
		if !cols[want] {
			t.Fatalf("missing column %q (have %v)", want, cols)
		}
	}
}

func TestMigrate_LegacyDB_GetsUpgraded(t *testing.T) {
	db := openTempDB(t)

	if _, err := db.Exec(`CREATE TABLE file_cache (
		path         TEXT PRIMARY KEY,
		size         INTEGER,
		mtime        INTEGER
	)`); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO file_cache (path, size, mtime) VALUES ('a', 1, 2)`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var size int
	if err := db.QueryRow(`SELECT size FROM file_cache WHERE path = 'a'`).Scan(&size); err != nil {
		t.Fatalf("legacy row gone: %v", err)
	}
	if size != 1 {
		t.Fatalf("size mismatch")
	}
	rows, err := db.Query("PRAGMA table_info(file_cache)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		_ = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk)
		have[name] = true
	}
	for _, want := range []string{"partial_hash", "full_hash", "algorithm", "last_seen"} {
		if !have[want] {
			t.Fatalf("legacy upgrade missed column %q", want)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db := openTempDB(t)
	for i := 0; i < 3; i++ {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate %d: %v", i, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != currentSchemaVersion {
		t.Fatalf("got %d schema_version rows, want %d", n, currentSchemaVersion)
	}
}
