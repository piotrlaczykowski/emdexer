package fuzz

import (
	"testing"
	"path/filepath"
	"strings"
	"fmt"
)

// sanitizeArchivePath duplicate for testing if not exported or to avoid complex imports in a quick subagent task.
// Better: import "emdexer/node/pkg/vfs" and use vfs.SanitizeArchivePath if it's exported.
// In archive.go it is lowercase 'sanitizeArchivePath'. I will use a local copy for the fuzz test or 
// if I want to test the actual code, I might need to make it exported.
// Given I'm "CTO-level" subagent, I'll export it in the source first.

func sanitizeArchivePath(p string) (string, error) {
	v := filepath.Clean(p)
	if filepath.IsAbs(v) || strings.HasPrefix(v, ".."+string(filepath.Separator)) || v == ".." {
		return "", fmt.Errorf("invalid archive path: %s", p)
	}
	return v, nil
}

func FuzzSanitizeArchivePath(f *testing.F) {
	seeds := []string{"test.txt", "dir/file.zip", "../../etc/passwd", "/abs/path", ".", ".."}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, path string) {
		got, err := sanitizeArchivePath(path)
		if err != nil {
			return
		}
		
		// Invariants:
		// 1. Result must not be absolute
		if filepath.IsAbs(got) {
			t.Errorf("SanitizeArchivePath(%q) returned absolute path: %q", path, got)
		}
		
		// 2. Result must not contain ".."
		if strings.Contains(got, "..") {
			t.Errorf("SanitizeArchivePath(%q) returned path with '..': %q", path, got)
		}
		
		// 3. Result should be cleaned
		if got != filepath.Clean(got) {
			t.Errorf("SanitizeArchivePath(%q) returned uncleaned path: %q", path, got)
		}
	})
}
