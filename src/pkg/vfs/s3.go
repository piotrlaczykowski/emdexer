package vfs

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3FileSystem implements FileSystem for S3/MinIO-compatible object storage.
type S3FileSystem struct {
	client *minio.Client
	bucket string
}

// NewS3FileSystem creates an S3FileSystem connected to the given endpoint and bucket.
func NewS3FileSystem(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3FileSystem, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	exists, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("s3: bucket %q does not exist", bucket)
	}
	return &S3FileSystem{client: client, bucket: bucket}, nil
}

// Open downloads the S3 object and returns it as an fs.File.
func (s *S3FileSystem) Open(name string) (fs.File, error) {
	obj, err := s.client.GetObject(context.Background(), s.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, err
	}
	return &S3File{obj: obj, info: info, name: name}, nil
}

// ReadDir lists objects under the given prefix at one level of depth.
func (s *S3FileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	prefix := name
	if prefix == "." || prefix == "" {
		prefix = ""
	} else if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	ctx := context.Background()
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}

	var entries []fs.DirEntry
	seen := make(map[string]bool)

	for obj := range s.client.ListObjects(ctx, s.bucket, opts) {
		if obj.Err != nil {
			return entries, obj.Err
		}

		key := obj.Key
		// Strip the prefix to get the relative name
		rel := strings.TrimPrefix(key, prefix)
		if rel == "" {
			continue
		}

		// Check if this is a "directory" (common prefix ending with /)
		isDir := strings.HasSuffix(rel, "/")
		entryName := strings.TrimSuffix(rel, "/")

		// Avoid duplicate directory entries
		if seen[entryName] {
			continue
		}
		seen[entryName] = true

		entries = append(entries, &s3DirEntry{
			name:  entryName,
			isDir: isDir,
			info: &s3FileInfo{
				name:    entryName,
				size:    obj.Size,
				modTime: obj.LastModified,
				isDir:   isDir,
			},
		})
	}
	return entries, nil
}

// Stat returns file info for the given S3 object key.
func (s *S3FileSystem) Stat(name string) (fs.FileInfo, error) {
	info, err := s.client.StatObject(context.Background(), s.bucket, name, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	return &s3FileInfo{
		name:    path.Base(name),
		size:    info.Size,
		modTime: info.LastModified,
		isDir:   false,
		etag:    info.ETag,
	}, nil
}

// Close is a no-op — the MinIO client is stateless HTTP.
func (s *S3FileSystem) Close() error { return nil }

// --- S3File ---

// S3File wraps a MinIO object to implement fs.File, io.Seeker, and io.ReaderAt.
type S3File struct {
	obj  *minio.Object
	info minio.ObjectInfo
	name string
}

func (f *S3File) Read(p []byte) (int, error)                { return f.obj.Read(p) }
func (f *S3File) Seek(off int64, whence int) (int64, error)  { return f.obj.Seek(off, whence) }
func (f *S3File) ReadAt(p []byte, off int64) (int, error)    { return f.obj.ReadAt(p, off) }
func (f *S3File) Close() error                               { return f.obj.Close() }

func (f *S3File) Stat() (fs.FileInfo, error) {
	return &s3FileInfo{
		name:    path.Base(f.name),
		size:    f.info.Size,
		modTime: f.info.LastModified,
		isDir:   false,
		etag:    f.info.ETag,
	}, nil
}

// --- s3FileInfo ---

type s3FileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
	etag    string
}

func (fi *s3FileInfo) Name() string        { return fi.name }
func (fi *s3FileInfo) Size() int64         { return fi.size }
func (fi *s3FileInfo) Mode() fs.FileMode {
	if fi.isDir {
		return 0755 | fs.ModeDir
	}
	return 0644
}
func (fi *s3FileInfo) ModTime() time.Time  { return fi.modTime }
func (fi *s3FileInfo) IsDir() bool         { return fi.isDir }
func (fi *s3FileInfo) Sys() interface{}    { return fi.etag }

// --- s3DirEntry ---

type s3DirEntry struct {
	name  string
	isDir bool
	info  fs.FileInfo
}

func (e *s3DirEntry) Name() string               { return e.name }
func (e *s3DirEntry) IsDir() bool                { return e.isDir }
func (e *s3DirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e *s3DirEntry) Info() (fs.FileInfo, error)  { return e.info, nil }
