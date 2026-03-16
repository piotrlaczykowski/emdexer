package vfs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type OSFileSystem struct {
	Root string
}

func (o *OSFileSystem) resolve(name string) (string, error) {
	absRoot, err := filepath.Abs(o.Root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absRoot, name)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absPath, absRoot) {
		return "", fmt.Errorf("vfs: path traversal detected: %s is outside of root", name)
	}
	return absPath, nil
}

func (o *OSFileSystem) Open(name string) (fs.File, error) {
	path, err := o.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (o *OSFileSystem) Stat(name string) (fs.FileInfo, error) {
	path, err := o.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(path)
}

func (o *OSFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	path, err := o.resolve(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

func (o *OSFileSystem) Close() error {
	return nil
}
