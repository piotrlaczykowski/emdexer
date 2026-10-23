package vfs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Self-hosted S3 (MinIO, RustFS) sits on a private address; the operator-configured endpoint must be reachable.
func TestNewS3FileSystem_PrivateEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("location") {
			_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
			return
		}
		w.WriteHeader(http.StatusOK) // HEAD /docs: bucket exists
	}))
	defer srv.Close()

	fs, err := NewS3FileSystem(strings.TrimPrefix(srv.URL, "http://"), "key", "secret", "docs", false)
	if err != nil {
		t.Fatalf("NewS3FileSystem against %s: %v", srv.URL, err)
	}
	if fs.bucket != "docs" {
		t.Fatalf("bucket = %q, want docs", fs.bucket)
	}
}
