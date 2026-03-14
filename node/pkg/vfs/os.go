package vfs

import (
	"io/fs"
	"os"
)

type OSFileSystem struct{}

func (o *OSFileSystem) Open(name string) (fs.File, error) {
	return os.Open(name)
}

func (o *OSFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

func (o *OSFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}

func (o *OSFileSystem) Close() error {
	return nil
}
