package watcher

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

type MockFS struct {
	Files map[string]*MockFile
}

type MockFile struct {
	FileName    string
	FileContent []byte
	FileIsDir   bool
	FileMTime   time.Time
}

func (m *MockFile) Stat() (fs.FileInfo, error) { return m, nil }
func (m *MockFile) Read(p []byte) (int, error) { return 0, io.EOF }
func (m *MockFile) Close() error               { return nil }
func (m *MockFile) Name() string               { return m.FileName }
func (m *MockFile) Size() int64                { return int64(len(m.FileContent)) }
func (m *MockFile) Mode() fs.FileMode          { return 0 }
func (m *MockFile) ModTime() time.Time         { return m.FileMTime }
func (m *MockFile) IsDir() bool                { return m.FileIsDir }
func (m *MockFile) Sys() interface{}           { return nil }

func (fs *MockFS) Open(name string) (fs.File, error) {
	if f, ok := fs.Files[name]; ok {
		return f, nil
	}
	return nil, os.ErrNotExist
}

func (fs *MockFS) ReadDir(name string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	for path, f := range fs.Files {
		// Simplified dir check for test
		if filepath.Dir(path) == name && path != name {
			entries = append(entries, &MockDirEntry{f})
		}
	}
	return entries, nil
}

type MockDirEntry struct {
	info os.FileInfo
}

func (m *MockDirEntry) Name() string               { return m.info.Name() }
func (m *MockDirEntry) IsDir() bool                { return m.info.IsDir() }
func (m *MockDirEntry) Type() os.FileMode          { return m.info.Mode() }
func (m *MockDirEntry) Info() (os.FileInfo, error) { return m.info, nil }

func (fs *MockFS) Stat(name string) (fs.FileInfo, error) {
	if f, ok := fs.Files[name]; ok {
		return f, nil
	}
	return nil, os.ErrNotExist
}

func (fs *MockFS) Close() error { return nil }

// MockFlatFS is a MockFS that also implements FlatListingFS using vfs.Entry.
type MockFlatFS struct {
	MockFS
	FlatEntries []vfs.Entry
	FlatErr     error
}

func (m *MockFlatFS) ReadDirFlat(_ string) ([]vfs.Entry, error) {
	return m.FlatEntries, m.FlatErr
}

func TestPoller(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "poller_test")
	defer os.RemoveAll(tmpDir)

	cache, err := NewMetadataCache(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	mockFS := &MockFS{
		Files: map[string]*MockFile{
			".": {FileName: ".", FileIsDir: true},
			"file1.txt": {FileName: "file1.txt", FileContent: []byte("v1"), FileMTime: time.Now()},
		},
	}

	indexed := make(map[string]bool)
	deleted := make(map[string]bool)

	p := NewPoller(mockFS, ".", cache, 100*time.Millisecond, func(path string, content []byte) error {
		indexed[path] = true
		fmt.Printf("MOCK INDEX: %s\n", path)
		return nil
	}, func(path string) error {
		deleted[path] = true
		fmt.Printf("MOCK DELETE: %s\n", path)
		return nil
	})

	// 1. Initial poll
	p.poll()
	if !indexed["file1.txt"] {
		t.Error("Expected file1.txt to be indexed")
	}

	// 2. Poll again, no change
	delete(indexed, "file1.txt")
	p.poll()
	if indexed["file1.txt"] {
		t.Error("Expected file1.txt NOT to be re-indexed")
	}

	// 3. Update file
	mockFS.Files["file1.txt"].FileMTime = time.Now().Add(time.Second)
	p.poll()
	if !indexed["file1.txt"] {
		t.Error("Expected file1.txt to be re-indexed after change")
	}

	// 4. Delete file
	delete(mockFS.Files, "file1.txt")
	time.Sleep(2 * time.Second)
	p.poll()
	if !deleted["file1.txt"] {
		t.Error("Expected file1.txt to be detected as deleted")
	}
}

// TestPollerFlatListing verifies that the FlatListingFS fast path uses
// Entry.Path directly (VFS-root-relative), matching recursiveWalk semantics.
func TestPollerFlatListing(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "poller_flat_test")
	defer os.RemoveAll(tmpDir)

	cache, err := NewMetadataCache(filepath.Join(tmpDir, "flat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	now := time.Now()
	flatFS := &MockFlatFS{
		MockFS: MockFS{
			Files: map[string]*MockFile{
				".":                  {FileName: ".", FileIsDir: true},
				"dir/file.txt":       {FileName: "file.txt", FileContent: []byte("hello"), FileMTime: now},
				"dir/sub/nested.txt": {FileName: "nested.txt", FileContent: []byte("world"), FileMTime: now},
			},
		},
		FlatEntries: []vfs.Entry{
			{Name: "file.txt", Path: "dir/file.txt", Size: 5, MTime: now},
			{Name: "nested.txt", Path: "dir/sub/nested.txt", Size: 5, MTime: now},
		},
	}

	indexed := make(map[string]bool)

	p := NewPoller(flatFS, ".", cache, 100*time.Millisecond, func(path string, content []byte) error {
		indexed[path] = true
		return nil
	}, func(path string) error {
		return nil
	})

	p.poll()

	if !indexed["dir/file.txt"] {
		t.Error("Expected dir/file.txt to be indexed via flat listing")
	}
	if !indexed["dir/sub/nested.txt"] {
		t.Error("Expected dir/sub/nested.txt to be indexed via flat listing")
	}
}

// TestPollerFlatListingWalkErrorPreventsDeletes verifies that a walk error in
// the flat-listing path aborts deletion detection to avoid false tombstones.
func TestPollerFlatListingWalkErrorPreventsDeletes(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "poller_flat_err_test")
	defer os.RemoveAll(tmpDir)

	cache, err := NewMetadataCache(filepath.Join(tmpDir, "flat_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	now := time.Now()

	// Phase 1: seed the cache with one file
	flatFS := &MockFlatFS{
		MockFS: MockFS{
			Files: map[string]*MockFile{
				".":       {FileName: ".", FileIsDir: true},
				"a.txt":   {FileName: "a.txt", FileContent: []byte("v1"), FileMTime: now},
			},
		},
		FlatEntries: []vfs.Entry{
			{Name: "a.txt", Path: "a.txt", Size: 2, MTime: now},
		},
	}

	deleted := make(map[string]bool)

	p := NewPoller(flatFS, ".", cache, 100*time.Millisecond, func(path string, content []byte) error {
		return nil
	}, func(path string) error {
		deleted[path] = true
		return nil
	})

	p.poll()

	// Phase 2: simulate a listing error — deletion detection must be skipped.
	time.Sleep(2 * time.Second)
	flatFS.FlatEntries = nil
	flatFS.FlatErr = errors.New("network error")

	p.poll()

	if deleted["a.txt"] {
		t.Error("a.txt should NOT be deleted when walk returns an error")
	}
}
