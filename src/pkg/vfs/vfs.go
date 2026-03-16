package vfs

import (
	"io"
	"io/fs"
	"time"
)

type FileSystem interface {
	fs.FS
	Open(name string) (fs.File, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	CheckPermissions() error
	Close() error
}

type File interface {
	fs.File
	io.Seeker
	io.ReaderAt
}

type Entry struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
	MTime time.Time
}
