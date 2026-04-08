package watcher

import (
	"path/filepath"
	"testing"
)

func TestNewMetadataCache_WALMode(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMetadataCache(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	defer cache.Close()

	var mode string
	if err := cache.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA query: %v", err)
	}
	if mode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", mode)
	}
}
