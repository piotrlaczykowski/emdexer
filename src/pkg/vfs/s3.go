package vfs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/piotrlaczykowski/emdexer/util"
)

type S3FileSystem struct {
	client *s3.Client
	bucket string
	prefix string
}

type S3Options struct {
	Region           string
	Endpoint         string
	AccessKey        string
	SecretKey        string
	UsePathStyle     bool
	Prefix           string
}

func NewS3FileSystem(ctx context.Context, bucket string, opts S3Options) (*S3FileSystem, error) {
	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(opts.Region),
	}

	if opts.AccessKey != "" && opts.SecretKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, append(cfgOpts, config.WithHTTPClient(util.NewSafeHTTPClient(0)))...)
	if err != nil {
		return nil, fmt.Errorf("failed to load S3 config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
		o.UsePathStyle = opts.UsePathStyle
	})

	return &S3FileSystem{
		client: client,
		bucket: bucket,
		prefix: strings.Trim(opts.Prefix, "/"),
	}, nil
}

func (s *S3FileSystem) fullPath(name string) string {
	name = strings.TrimLeft(name, "/")
	if s.prefix == "" {
		return name
	}
	if name == "" || name == "." {
		return s.prefix
	}
	return s.prefix + "/" + name
}

func (s *S3FileSystem) Open(name string) (fs.File, error) {
	key := s.fullPath(name)
	
	// Check if it's a "directory" (prefix)
	if name == "." || name == "" || strings.HasSuffix(name, "/") {
		return &S3Directory{fs: s, name: name, key: key}, nil
	}

	// Try to get the object to see if it exists and if it's a file
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// If object not found, it might still be a directory prefix
		return &S3Directory{fs: s, name: name, key: key + "/"}, nil
	}

	return &S3File{
		ReadCloser: output.Body,
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return &S3FileInfo{
			name:  fsName(name),
			size:  *head.ContentLength,
			mtime: *head.LastModified,
			isDir: false,
		}, nil
	}

	// Check if it's a directory by listing with prefix
	list, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  aws.String(key + "/"),
		MaxKeys: aws.Int32(1),
	})
	if err == nil && (*list.KeyCount > 0) {
		return &S3FileInfo{
			name:  fsName(name),
			isDir: true,
		}, nil
	}

	return nil, fs.ErrNotExist
}

func (s *S3FileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	key := s.fullPath(name)
	if key != "" && !strings.HasSuffix(key, "/") {
		key += "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String(key),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return nil, err
	}

	var entries []fs.DirEntry
	for _, p := range output.CommonPrefixes {
		entries = append(entries, fs.FileInfoToDirEntry(&S3FileInfo{
			name:  fsName(*p.Prefix),
			isDir: true,
		}))
	}
	for _, o := range output.Contents {
		if *o.Key == key {
			continue // Skip the directory itself if it exists as an object
		}
		entries = append(entries, fs.FileInfoToDirEntry(&S3FileInfo{
			name:  fsName(*o.Key),
			size:  *o.Size,
			mtime: *o.LastModified,
			isDir: false,
		}))
	}

	return entries, nil
}

func (s *S3FileSystem) ReadDirFlat(name string) ([]fs.DirEntry, error) {
	key := s.fullPath(name)
	if key != "" && !strings.HasSuffix(key, "/") {
		key += "/"
	}

	var entries []fs.DirEntry
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(key),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, o := range page.Contents {
			if *o.Key == key || strings.HasSuffix(*o.Key, "/") {
				continue
			}
			// For flat listing, we provide the relative path from the 'name' directory
			relPath := strings.TrimPrefix(*o.Key, key)
			entries = append(entries, fs.FileInfoToDirEntry(&S3FileInfo{
				name:  relPath,
				size:  *o.Size,
				mtime: *o.LastModified,
				isDir: false,
			}))
		}
	}

	return entries, nil
}

func (s *S3FileSystem) Close() error {
	return nil
}

type S3File struct {
	io.ReadCloser
	fs    *S3FileSystem
	name  string
	key   string
	size  int64
	mtime time.Time
}

func (f *S3File) Stat() (fs.FileInfo, error) {
	return &S3FileInfo{
		name:  fsName(f.name),
		size:  f.size,
		mtime: f.mtime,
		isDir: false,
	}, nil
}

type S3Directory struct {
	fs   *S3FileSystem
	name string
	key  string
}

func (d *S3Directory) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("is a directory")
}

func (d *S3Directory) Stat() (fs.FileInfo, error) {
	return &S3FileInfo{
		name:  fsName(d.name),
		isDir: true,
	}, nil
}

func (d *S3Directory) Close() error {
	return nil
}

type S3FileInfo struct {
	name  string
	size  int64
	mtime time.Time
	isDir bool
}

func (fi *S3FileInfo) Name() string       { return fi.name }
func (fi *S3FileInfo) Size() int64        { return fi.size }
func (fi *S3FileInfo) Mode() fs.FileMode  { if fi.isDir { return fs.ModeDir }; return 0444 }
func (fi *S3FileInfo) ModTime() time.Time { return fi.mtime }
func (fi *S3FileInfo) IsDir() bool        { return fi.isDir }
func (fi *S3FileInfo) Sys() interface{}   { return nil }

func fsName(path string) string {
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}
