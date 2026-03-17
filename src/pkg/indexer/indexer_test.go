package indexer

import (
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

// mockFlatFS implements vfs.FlatListingFS with canned data.
type mockFlatFS struct {
	entries []vfs.Entry
	files   map[string]string // path → content
}

func (m *mockFlatFS) Open(name string) (fs.File, error) {
	content, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &mockFile{content: content, name: name}, nil
}

func (m *mockFlatFS) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, fs.ErrInvalid
}

func (m *mockFlatFS) Stat(string) (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

func (m *mockFlatFS) Close() error { return nil }

func (m *mockFlatFS) ReadDirFlat(string) ([]vfs.Entry, error) {
	return m.entries, nil
}

type mockFile struct {
	content string
	name    string
	reader  *strings.Reader
}

func (f *mockFile) Read(p []byte) (int, error) {
	if f.reader == nil {
		f.reader = strings.NewReader(f.content)
	}
	return f.reader.Read(p)
}

func (f *mockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{name: f.name, size: int64(len(f.content))}, nil
}

func (f *mockFile) Close() error { return nil }

type mockFileInfo struct {
	name string
	size int64
}

func (fi *mockFileInfo) Name() string      { return fi.name }
func (fi *mockFileInfo) Size() int64       { return fi.size }
func (fi *mockFileInfo) Mode() fs.FileMode { return 0444 }
func (fi *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *mockFileInfo) IsDir() bool       { return false }
func (fi *mockFileInfo) Sys() interface{}  { return nil }

func TestWalkFlatListingFS(t *testing.T) {
	mock := &mockFlatFS{
		entries: []vfs.Entry{
			{Name: "a.txt", Path: "a.txt", Size: 5, MTime: time.Now()},
			{Name: "b.txt", Path: "sub/b.txt", Size: 11, MTime: time.Now()},
		},
		files: map[string]string{
			"a.txt":     "hello",
			"sub/b.txt": "hello world",
		},
	}

	idx := NewIndexer(mock)
	var visited []string
	err := idx.Walk(".", func(path string, isDir bool, content []byte) error {
		visited = append(visited, path)
		if isDir {
			t.Errorf("unexpected directory callback for %s", path)
		}
		expected, ok := mock.files[path]
		if !ok {
			t.Errorf("unexpected path %s", path)
			return nil
		}
		if string(content) != expected {
			t.Errorf("content for %s = %q, want %q", path, string(content), expected)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(visited) != 2 {
		t.Errorf("visited %d files, want 2", len(visited))
	}
}

// mockRegularFS implements only vfs.FileSystem (not FlatListingFS) to verify
// the recursive fallback path still works.
type mockRegularFS struct {
	entries map[string][]fs.DirEntry
	files   map[string]string
}

func (m *mockRegularFS) Open(name string) (fs.File, error) {
	content, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &mockFile{content: content, name: name}, nil
}

func (m *mockRegularFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, ok := m.entries[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return entries, nil
}

func (m *mockRegularFS) Stat(string) (fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

func (m *mockRegularFS) Close() error { return nil }

type mockDirEntry struct {
	name  string
	isDir bool
}

func (e *mockDirEntry) Name() string               { return e.name }
func (e *mockDirEntry) IsDir() bool                 { return e.isDir }
func (e *mockDirEntry) Type() fs.FileMode           { return 0 }
func (e *mockDirEntry) Info() (fs.FileInfo, error)   { return nil, nil }

func TestWalkRecursiveFallback(t *testing.T) {
	mock := &mockRegularFS{
		entries: map[string][]fs.DirEntry{
			".": {&mockDirEntry{name: "file.txt", isDir: false}},
		},
		files: map[string]string{
			"file.txt": "content",
		},
	}

	idx := NewIndexer(mock)
	var visited []string
	err := idx.Walk(".", func(path string, isDir bool, content []byte) error {
		visited = append(visited, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(visited) != 1 {
		t.Errorf("visited %d files, want 1", len(visited))
	}
	if visited[0] != "file.txt" {
		t.Errorf("visited[0] = %q, want %q", visited[0], "file.txt")
	}
}
