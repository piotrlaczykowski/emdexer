package watcher

// delta_test.go — AUDITOR tests for checksum-based delta detection.
//
// Scenarios:
//   1. Unchanged file (same path + size + mtime) → must be SKIPPED (no handler call).
//   2. File with same mtime but changed content  → must be DETECTED and re-indexed.
//   3. New file                                  → must be INDEXED on first poll.

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── Helpers: richer MockFS that supports io.ReaderAt ─────────────────────────

type DeltaMockFile struct {
	FileName    string
	FileContent []byte
	FileIsDir   bool
	FileMTime   time.Time

	// io.ReaderAt support for partial-hash computation.
	offset int64
}

func newDeltaMockFile(name string, content []byte, mtime time.Time) *DeltaMockFile {
	return &DeltaMockFile{FileName: name, FileContent: content, FileMTime: mtime}
}

func (f *DeltaMockFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *DeltaMockFile) Read(p []byte) (int, error) {
	if f.offset >= int64(len(f.FileContent)) {
		return 0, io.EOF
	}
	n := copy(p, f.FileContent[f.offset:])
	f.offset += int64(n)
	return n, nil
}
func (f *DeltaMockFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.FileContent)) {
		return 0, io.EOF
	}
	n := copy(p, f.FileContent[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (f *DeltaMockFile) Close() error              { f.offset = 0; return nil }
func (f *DeltaMockFile) Name() string              { return f.FileName }
func (f *DeltaMockFile) Size() int64               { return int64(len(f.FileContent)) }
func (f *DeltaMockFile) Mode() fs.FileMode         { return 0 }
func (f *DeltaMockFile) ModTime() time.Time        { return f.FileMTime }
func (f *DeltaMockFile) IsDir() bool               { return f.FileIsDir }
func (f *DeltaMockFile) Sys() interface{}          { return nil }

type DeltaMockFS struct {
	Files map[string]*DeltaMockFile
}

func (m *DeltaMockFS) Open(name string) (fs.File, error) {
	f, ok := m.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	// Return a fresh copy so offset resets per open.
	clone := *f
	clone.offset = 0
	return &clone, nil
}

func (m *DeltaMockFS) ReadDir(name string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	for path, f := range m.Files {
		if filepath.Dir(path) == name && path != name {
			entries = append(entries, &deltaMockDirEntry{f})
		}
	}
	return entries, nil
}

func (m *DeltaMockFS) Stat(name string) (fs.FileInfo, error) {
	f, ok := m.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return f, nil
}

func (m *DeltaMockFS) Close() error { return nil }

type deltaMockDirEntry struct{ info os.FileInfo }

func (e *deltaMockDirEntry) Name() string               { return e.info.Name() }
func (e *deltaMockDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e *deltaMockDirEntry) Type() os.FileMode          { return e.info.Mode() }
func (e *deltaMockDirEntry) Info() (os.FileInfo, error) { return e.info, nil }

// ── newDeltaPoller creates a Poller with delta detection ENABLED ─────────────

func newDeltaPoller(t *testing.T, mockFS *DeltaMockFS, tmpDir string,
	indexed map[string]int) *Poller {
	t.Helper()
	cache, err := NewMetadataCache(filepath.Join(tmpDir, "delta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })

	// Enable delta detection for all tests.
	t.Setenv("EMDEX_DELTA_ENABLED", "1")
	t.Setenv("EMDEX_FULL_HASH", "0")

	return NewPoller(mockFS, ".", cache, time.Hour,
		func(path string, _ []byte) error {
			indexed[path]++
			fmt.Printf("[test] indexed: %s (count=%d)\n", path, indexed[path])
			return nil
		},
		func(_ string) error { return nil },
	)
}

// ── Test 1: Unchanged file ───────────────────────────────────────────────────

func TestDelta_UnchangedFile_IsSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Unix(1_700_000_000, 0)
	content := []byte("stable content")

	mockFS := &DeltaMockFS{
		Files: map[string]*DeltaMockFile{
			".":          {FileName: ".", FileIsDir: true},
			"stable.txt": newDeltaMockFile("stable.txt", content, fixedTime),
		},
	}

	indexed := make(map[string]int)
	p := newDeltaPoller(t, mockFS, tmpDir, indexed)

	// First poll — file is new, must be indexed.
	p.poll()
	if indexed["stable.txt"] != 1 {
		t.Fatalf("first poll: expected 1 index call, got %d", indexed["stable.txt"])
	}

	// Second poll — nothing changed.
	p.poll()
	if indexed["stable.txt"] != 1 {
		t.Errorf("second poll: file is unchanged, should NOT be re-indexed (got %d calls)", indexed["stable.txt"])
	}
}

// ── Test 2: Same mtime, different content (hash detects change) ──────────────

func TestDelta_SameMtimeDifferentContent_IsDetected(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Unix(1_700_000_000, 0)
	original := []byte("original content — version 1")

	mockFS := &DeltaMockFS{
		Files: map[string]*DeltaMockFile{
			".":          {FileName: ".", FileIsDir: true},
			"tricky.txt": newDeltaMockFile("tricky.txt", original, fixedTime),
		},
	}

	indexed := make(map[string]int)
	p := newDeltaPoller(t, mockFS, tmpDir, indexed)

	// First poll — indexes the file and stores its partial hash.
	p.poll()
	if indexed["tricky.txt"] != 1 {
		t.Fatalf("first poll: expected 1 index call, got %d", indexed["tricky.txt"])
	}

	// Simulate mtime spoofing / silent overwrite: same mtime, different content,
	// same length (to ensure the hash — not just the size — catches the change).
	tampered := []byte("tampered content — version 2")
	// Keep the same mtime and same byte length.
	mockFS.Files["tricky.txt"] = newDeltaMockFile("tricky.txt", tampered, fixedTime)

	p.poll()
	if indexed["tricky.txt"] != 2 {
		t.Errorf("second poll: content changed (same mtime), file MUST be re-indexed (got %d calls)", indexed["tricky.txt"])
	}
}

// ── Test 3: New file ─────────────────────────────────────────────────────────

func TestDelta_NewFile_IsIndexed(t *testing.T) {
	tmpDir := t.TempDir()
	fixedTime := time.Unix(1_700_000_000, 0)

	mockFS := &DeltaMockFS{
		Files: map[string]*DeltaMockFile{
			".": {FileName: ".", FileIsDir: true},
		},
	}

	indexed := make(map[string]int)
	p := newDeltaPoller(t, mockFS, tmpDir, indexed)

	// First poll — empty FS, nothing to index.
	p.poll()
	if indexed["new.txt"] != 0 {
		t.Fatal("no files yet, nothing should be indexed")
	}

	// Add a new file between polls.
	mockFS.Files["new.txt"] = newDeltaMockFile("new.txt", []byte("brand new"), fixedTime)

	p.poll()
	if indexed["new.txt"] != 1 {
		t.Errorf("new file should be indexed on next poll, got %d calls", indexed["new.txt"])
	}
}
