package vfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// newTestS3FS creates an S3FileSystem backed by the given httptest server.
func newTestS3FS(t *testing.T, srv *httptest.Server, bucket, prefix string) *S3FileSystem {
	t.Helper()
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKID", "SECRET", "")),
		config.WithHTTPClient(srv.Client()),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	return &S3FileSystem{
		client: client,
		bucket: bucket,
		prefix: prefix,
		ctx:    ctx,
	}
}

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

func TestS3OpenContext(t *testing.T) {
	body := "hello from S3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetObject
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.Header().Set("Last-Modified", "Mon, 17 Mar 2026 10:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	f, err := s3fs.OpenContext(context.Background(), "file.txt")
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != body {
		t.Errorf("body = %q, want %q", string(data), body)
	}

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != int64(len(body)) {
		t.Errorf("size = %d, want %d", info.Size(), len(body))
	}
}

func TestS3OpenNotExist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "missing.txt") {
			// GetObject → NoSuchKey
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`))
			return
		}
		// ListObjectsV2 → empty (not a directory either)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><KeyCount>0</KeyCount></ListBucketResult>`))
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	_, err := s3fs.Open("missing.txt")
	if err != fs.ErrNotExist {
		t.Errorf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestS3ReadDirFlat(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		token := r.URL.Query().Get("continuation-token")
		if token == "" {
			// First page with continuation
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <KeyCount>2</KeyCount>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>page2</NextContinuationToken>
  <Contents>
    <Key>docs/a.txt</Key><Size>100</Size><LastModified>2026-03-17T10:00:00.000Z</LastModified>
  </Contents>
  <Contents>
    <Key>docs/b.txt</Key><Size>200</Size><LastModified>2026-03-17T11:00:00.000Z</LastModified>
  </Contents>
</ListBucketResult>`))
		} else {
			// Second page, final
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <KeyCount>1</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>docs/sub/c.txt</Key><Size>300</Size><LastModified>2026-03-17T12:00:00.000Z</LastModified>
  </Contents>
</ListBucketResult>`))
		}
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "docs")
	entries, err := s3fs.ReadDirFlat(".")
	if err != nil {
		t.Fatalf("ReadDirFlat: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify paths are VFS-root-relative (prefix stripped)
	wantPaths := []string{"a.txt", "b.txt", "sub/c.txt"}
	for i, want := range wantPaths {
		if entries[i].Path != want {
			t.Errorf("entry[%d].Path = %q, want %q", i, entries[i].Path, want)
		}
	}

	// Verify sizes
	wantSizes := []int64{100, 200, 300}
	for i, want := range wantSizes {
		if entries[i].Size != want {
			t.Errorf("entry[%d].Size = %d, want %d", i, entries[i].Size, want)
		}
	}
}

func TestS3ReadDirEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>`))
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	entries, err := s3fs.ReadDirFlat(".")
	if err != nil {
		t.Fatalf("ReadDirFlat: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestS3StatObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", "42")
			w.Header().Set("Last-Modified", "Mon, 17 Mar 2026 10:00:00 GMT")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	info, err := s3fs.Stat("report.pdf")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name() != "report.pdf" {
		t.Errorf("Name = %q, want %q", info.Name(), "report.pdf")
	}
	if info.Size() != 42 {
		t.Errorf("Size = %d, want 42", info.Size())
	}
	if info.IsDir() {
		t.Error("expected file, got directory")
	}
}

func TestS3StatDirectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			// HeadObject → not found (it's a "directory")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotFound</Code></Error>`))
			return
		}
		// ListObjectsV2 → has children → is a directory
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <KeyCount>1</KeyCount>
  <Contents><Key>subdir/file.txt</Key><Size>10</Size><LastModified>2026-03-17T10:00:00.000Z</LastModified></Contents>
</ListBucketResult>`))
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	info, err := s3fs.Stat("subdir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
	if info.Name() != "subdir" {
		t.Errorf("Name = %q, want %q", info.Name(), "subdir")
	}
}

func TestS3Ping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>`))
	}))
	defer srv.Close()

	s3fs := newTestS3FS(t, srv, "test-bucket", "")
	if err := s3fs.Ping(context.Background()); err != nil {
		t.Errorf("Ping should succeed: %v", err)
	}
}
