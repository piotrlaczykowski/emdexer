package vfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/piotrlaczykowski/emdexer/util"
)

type S3FileSystem struct {
	client *s3.Client
	bucket string
	prefix string
	ctx    context.Context
}

type S3Options struct {
	Region       string
	Endpoint     string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
	Prefix       string
}

func NewS3FileSystem(ctx context.Context, bucket string, opts S3Options) (*S3FileSystem, error) {
	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(opts.Region),
		// Use only the transport (no client-level Timeout) so that large object
		// streams are not aborted mid-read.  Per-request timeouts are controlled
		// via the context passed to OpenContext / pollPath.
		config.WithHTTPClient(&http.Client{Transport: util.NewSafeTransport()}),
	}
	if opts.AccessKey != "" && opts.SecretKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, ""),
		))
	}
	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil { return nil, fmt.Errorf("failed to load S3 config: %w", err) }
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if opts.Endpoint != "" { o.BaseEndpoint = aws.String(opts.Endpoint) }
		o.UsePathStyle = opts.UsePathStyle
	})
	return &S3FileSystem{
		client: client,
		bucket: bucket,
		prefix: strings.Trim(opts.Prefix, "/"),
		ctx:    ctx,
	}, nil
}

// NewS3FileSystemFromClient creates an S3FileSystem using a pre-configured
// client. Useful for testing with custom endpoints.
func NewS3FileSystemFromClient(client *s3.Client, bucket, prefix string, ctx context.Context) *S3FileSystem {
	return &S3FileSystem{
		client: client,
		bucket: bucket,
		prefix: strings.Trim(prefix, "/"),
		ctx:    ctx,
	}
}

func (s *S3FileSystem) fullPath(name string) string {
	name = strings.TrimLeft(name, "/")
	if s.prefix == "" { return name }
	if name == "" || name == "." { return s.prefix }
	return s.prefix + "/" + name
}

// fsName returns the base file/directory name for a given path, stripping any
// trailing slash.  Returns "" for an empty input (unlike path.Base which
// returns ".").
func fsName(p string) string {
	if p == "" {
		return ""
	}
	return path.Base(strings.TrimSuffix(p, "/"))
}

func (s *S3FileSystem) Open(name string) (fs.File, error) {
	return s.OpenContext(s.ctx, name)
}

func (s *S3FileSystem) OpenContext(ctx context.Context, name string) (fs.File, error) {
	key := s.fullPath(name)
	streamCtx, cancel := context.WithCancel(ctx)
	output, err := s.client.GetObject(streamCtx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		cancel()
		var nsk *types.NoSuchKey
		var nf *types.NotFound
		if errors.As(err, &nsk) || errors.As(err, &nf) {
			// Not a file — check whether it is a directory prefix.
			prefix := key
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			list, listErr := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket:  aws.String(s.bucket),
				Prefix:  aws.String(prefix),
				MaxKeys: aws.Int32(1),
			})
			if listErr != nil {
				return nil, fmt.Errorf("s3 open directory check %s: %w", key, listErr)
			}
			if *list.KeyCount > 0 {
				return &S3Directory{fs: s, name: name, key: key}, nil
			}
			return nil, fs.ErrNotExist
		}
		return nil, fmt.Errorf("s3 get object %s: %w", key, err)
	}
	return &S3File{
		ReadCloser: output.Body,
		cancel:     cancel,
		fs:         s,
		name:       name,
		key:        key,
		size:       *output.ContentLength,
		mtime:      *output.LastModified,
	}, nil
}

func (s *S3FileSystem) Stat(name string) (fs.FileInfo, error) {
	key := s.fullPath(name)
	if name == "." || name == "" {
		return &S3FileInfo{name: ".", isDir: true}, nil
	}
	head, err := s.client.HeadObject(s.ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return &S3FileInfo{
			name:  path.Base(name),
			size:  *head.ContentLength,
			mtime: *head.LastModified,
			isDir: false,
		}, nil
	}

	var nsk *types.NoSuchKey
	var nf *types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		list, listErr := s.client.ListObjectsV2(s.ctx, &s3.ListObjectsV2Input{
			Bucket:  aws.String(s.bucket),
			Prefix:  aws.String(key + "/"),
			MaxKeys: aws.Int32(1),
		})
		if listErr != nil {
			return nil, fmt.Errorf("s3 stat directory check %s: %w", key, listErr)
		}
		if *list.KeyCount > 0 {
			return &S3FileInfo{name: path.Base(name), isDir: true}, nil
		}
		return nil, fs.ErrNotExist
	}

	return nil, fmt.Errorf("s3 head object %s: %w", key, err)
}

func (s *S3FileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	key := s.fullPath(name)
	if key != "" && !strings.HasSuffix(key, "/") { key += "/" }
	var entries []fs.DirEntry
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String(key),
		Delimiter: aws.String("/"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(s.ctx)
		if err != nil { return nil, err }
		for _, p := range page.CommonPrefixes {
			entries = append(entries, fs.FileInfoToDirEntry(&S3FileInfo{
				name:  strings.TrimSuffix(path.Base(*p.Prefix), "/"),
				isDir: true,
			}))
		}
		for _, o := range page.Contents {
			if *o.Key == key { continue }
			entries = append(entries, fs.FileInfoToDirEntry(&S3FileInfo{
				name:  path.Base(*o.Key),
				size:  *o.Size,
				mtime: *o.LastModified,
				isDir: false,
			}))
		}
	}
	return entries, nil
}

// ReadDirFlat lists all objects under name without a delimiter (no simulated
// subdirectory boundary).  Each Entry.Path is relative to the VFS root so it
// can be used directly as an Open argument — matching the semantics of
// recursiveWalk.
func (s *S3FileSystem) ReadDirFlat(name string) ([]Entry, error) {
	listingKey := s.fullPath(name)
	if listingKey != "" && !strings.HasSuffix(listingKey, "/") {
		listingKey += "/"
	}
	// rootPrefix is the VFS prefix (with trailing slash) that we strip from
	// each S3 key to obtain a path relative to the VFS root.
	rootPrefix := ""
	if s.prefix != "" {
		rootPrefix = s.prefix + "/"
	}
	var entries []Entry
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(listingKey),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(s.ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 read dir flat %s: %w", listingKey, err)
		}
		for _, o := range page.Contents {
			if *o.Key == listingKey {
				continue
			}
			// Strip the bucket prefix to get a path relative to the VFS root.
			rel := strings.TrimPrefix(*o.Key, rootPrefix)
			entries = append(entries, Entry{
				Name:  fsName(rel),
				Path:  rel,
				IsDir: false,
				Size:  *o.Size,
				MTime: *o.LastModified,
			})
		}
	}
	return entries, nil
}

// Ping verifies connectivity to the S3 bucket by listing a single object.
func (s *S3FileSystem) Ping(ctx context.Context) error {
	_, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return fmt.Errorf("s3 ping %s: %w", s.bucket, err)
	}
	return nil
}

func (s *S3FileSystem) Close() error { return nil }

type S3File struct {
	io.ReadCloser
	cancel context.CancelFunc
	fs *S3FileSystem
	name string
	key string
	size int64
	mtime time.Time
}

func (f *S3File) Stat() (fs.FileInfo, error) {
	return &S3FileInfo{name: path.Base(f.name), size: f.size, mtime: f.mtime, isDir: false}, nil
}

func (f *S3File) Close() error {
	if f.cancel != nil { f.cancel() }
	return f.ReadCloser.Close()
}

type S3Directory struct {
	fs *S3FileSystem
	name string
	key string
}

func (d *S3Directory) Read(p []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}

func (d *S3Directory) Stat() (fs.FileInfo, error) {
	return &S3FileInfo{name: path.Base(d.name), isDir: true}, nil
}

func (d *S3Directory) Close() error { return nil }

type S3FileInfo struct {
	name string
	size int64
	mtime time.Time
	isDir bool
}

func (fi *S3FileInfo) Name() string { return path.Base(fi.name) }
func (fi *S3FileInfo) Size() int64 { return fi.size }
func (fi *S3FileInfo) Mode() fs.FileMode { if fi.isDir { return fs.ModeDir }; return 0444 }
func (fi *S3FileInfo) ModTime() time.Time { return fi.mtime }
func (fi *S3FileInfo) IsDir() bool { return fi.isDir }
func (fi *S3FileInfo) Sys() interface{} { return nil }
