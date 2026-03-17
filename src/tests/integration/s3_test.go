//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/vfs"
)

// TestS3VFSToIndexerWalk verifies the full chain:
// S3 VFS → Indexer.Walk (FlatListingFS fast path) → content delivered via callback.
func TestS3VFSToIndexerWalk(t *testing.T) {
	fileA := "content of file A"
	fileB := "content of file B in subdir"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		query := r.URL.Query()

		// ListObjectsV2 — returns flat listing
		if query.Get("list-type") == "2" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <KeyCount>2</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>data/a.txt</Key><Size>%d</Size><LastModified>2026-03-17T10:00:00.000Z</LastModified>
  </Contents>
  <Contents>
    <Key>data/sub/b.txt</Key><Size>%d</Size><LastModified>2026-03-17T11:00:00.000Z</LastModified>
  </Contents>
</ListBucketResult>`, len(fileA), len(fileB))))
			return
		}

		// GetObject — serve file contents
		if r.Method == "GET" {
			switch {
			case path == "/test-bucket/data/a.txt":
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fileA)))
				w.Header().Set("Last-Modified", "Mon, 17 Mar 2026 10:00:00 GMT")
				w.Write([]byte(fileA))
			case path == "/test-bucket/data/sub/b.txt":
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fileB)))
				w.Header().Set("Last-Modified", "Mon, 17 Mar 2026 11:00:00 GMT")
				w.Write([]byte(fileB))
			default:
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code></Error>`))
			}
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

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

	s3fs := vfs.NewS3FileSystemFromClient(client, "test-bucket", "data", ctx)

	// Verify FlatListingFS is implemented
	if _, ok := vfs.FileSystem(s3fs).(vfs.FlatListingFS); !ok {
		t.Fatal("S3FileSystem should implement FlatListingFS")
	}

	idx := indexer.NewIndexer(s3fs)

	collected := make(map[string]string)
	err = idx.Walk(".", func(path string, isDir bool, content []byte) error {
		if isDir {
			return nil
		}
		collected[path] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	if len(collected) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(collected), collected)
	}

	expected := map[string]string{
		"a.txt":     fileA,
		"sub/b.txt": fileB,
	}
	for path, want := range expected {
		got, ok := collected[path]
		if !ok {
			t.Errorf("missing file %s", path)
			continue
		}
		if got != want {
			t.Errorf("file %s content = %q, want %q", path, got, want)
		}
	}
}
