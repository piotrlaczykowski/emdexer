package watcher

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
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
