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
	// 1. Evaluate symlinks on root to get the real canonical path
	realRoot, err := filepath.EvalSymlinks(o.Root)
	if err != nil {
		return "", fmt.Errorf("vfs: failed to resolve root: %w", err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", err
	}

	// 2. Join root and target name
	path := filepath.Join(realRoot, name)

	// 3. Evaluate symlinks on the target path to check for escapes via symlinks
	// Note: EvalSymlinks requires the path to exist if we want to resolve it fully.
	// If it doesn't exist, we still want to prevent traversal in the path string.
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If path doesn't exist, we still need to validate the joined path string
		realPath, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
	} else {
		realPath, err = filepath.Abs(realPath)
		if err != nil {
			return "", err
		}
	}

	// 4. Use filepath.Rel to check if the path is truly relative to the root.
	// Rel returns a path relative to basepath such that join(basepath, rel) == targetpath.
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return "", fmt.Errorf("vfs: path traversal detected: %w", err)
	}

	// 5. Reject if it starts with ".." (outside root) or is an absolute path (on some OSs)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("vfs: path traversal detected: %s is outside of root", name)
	}

	return realPath, nil
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
