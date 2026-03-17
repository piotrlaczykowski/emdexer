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
	Close() error
}

type FlatListingFS interface {
	FileSystem
	// ReadDirFlat returns all files under name recursively as a flat list.
	// Each Entry.Path is relative to the VFS root (not to name), matching
	// the semantics of recursiveWalk so that the same cache keys and Open
	// paths are produced regardless of which backend is used.
	ReadDirFlat(name string) ([]Entry, error)
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
