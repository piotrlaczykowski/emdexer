package watcher

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newCacheT(t *testing.T) *MetadataCache {
	t.Helper()
	c, err := NewMetadataCache(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPurgeStale_DropsOldRows(t *testing.T) {
	c := newCacheT(t)
	now := time.Now().Unix()
	old := now - int64((30*24*time.Hour).Seconds()) - 60
	fresh := now - 60

	if _, err := c.db.Exec(
		`INSERT INTO file_cache (path, size, mtime, last_seen) VALUES (?, ?, ?, ?)`,
		"/old", 1, 1, old,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(
		`INSERT INTO file_cache (path, size, mtime, last_seen) VALUES (?, ?, ?, ?)`,
		"/fresh", 1, 1, fresh,
	); err != nil {
		t.Fatal(err)
	}

	deleted, err := c.PurgeStale(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d want 1", deleted)
	}
	var n int
	_ = c.db.QueryRow(`SELECT COUNT(*) FROM file_cache WHERE path = '/fresh'`).Scan(&n)
	if n != 1 {
		t.Fatalf("fresh row gone")
	}
	_ = c.db.QueryRow(`SELECT COUNT(*) FROM file_cache WHERE path = '/old'`).Scan(&n)
	if n != 0 {
		t.Fatalf("old row not deleted")
	}
}

func TestVacuum_RunsOnPopulatedDB(t *testing.T) {
	c := newCacheT(t)
	for i := 0; i < 100; i++ {
		path := "/p" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		if _, err := c.db.Exec(
			`INSERT INTO file_cache (path, size, mtime, last_seen) VALUES (?, ?, ?, ?)`,
			path, 1, 1, time.Now().Unix(),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Vacuum(context.Background()); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
}

func TestLastSeenTouch_AdvancesTimestamp(t *testing.T) {
	c := newCacheT(t)
	old := time.Now().Add(-72 * time.Hour).Unix()
	if _, err := c.db.Exec(
		`INSERT INTO file_cache (path, size, mtime, last_seen) VALUES (?, ?, ?, ?)`,
		"/x", 1, 1, old,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(
		`UPDATE file_cache SET last_seen = ? WHERE path = ?`,
		time.Now().Unix(), "/x",
	); err != nil {
		t.Fatal(err)
	}
	var got int64
	_ = c.db.QueryRow(`SELECT last_seen FROM file_cache WHERE path = '/x'`).Scan(&got)
	if got <= old {
		t.Fatalf("last_seen not advanced: %d <= %d", got, old)
	}
}
