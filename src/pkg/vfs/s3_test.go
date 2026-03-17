package vfs

import (
	"testing"
)

func TestS3FileSystemPathHandling(t *testing.T) {
	fs := &S3FileSystem{
		bucket: "test-bucket",
		prefix: "my/prefix",
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"", "my/prefix"},
		{".", "my/prefix"},
		{"file.txt", "my/prefix/file.txt"},
		{"/file.txt", "my/prefix/file.txt"},
		{"dir/file.txt", "my/prefix/dir/file.txt"},
	}

	for _, tt := range tests {
		got := fs.fullPath(tt.input)
		if got != tt.expected {
			t.Errorf("fullPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFsName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file.txt", "file.txt"},
		{"path/to/file.txt", "file.txt"},
		{"path/to/dir/", "dir"},
		{"", ""},
	}

	for _, tt := range tests {
		got := fsName(tt.input)
		if got != tt.expected {
			t.Errorf("fsName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
