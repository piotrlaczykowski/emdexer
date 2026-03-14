package indexer

import (
	"fmt"
	"io"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"path/filepath"
)

type Indexer struct {
	fs vfs.FileSystem
}

func NewIndexer(fs vfs.FileSystem) *Indexer {
	return &Indexer{fs: fs}
}

func (i *Indexer) Walk(root string, callback func(path string, isDir bool, content []byte) error) error {
	// Note: fs.WalkDir doesn't work directly with our interface because of the way fs.FS is structured.
	// We'll implement a simple recursive walker.
	return i.recursiveWalk(root, callback)
}

func (i *Indexer) recursiveWalk(path string, callback func(path string, isDir bool, content []byte) error) error {
	entries, err := i.fs.ReadDir(path)
	if err != nil {
		return fmt.Errorf("failed to read dir %s: %w", path, err)
	}

	archiveWalker := vfs.NewArchiveFileSystem(i.fs)

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			if err := i.recursiveWalk(fullPath, callback); err != nil {
				return err
			}
		} else {
			// Check if it's an archive
			ext := filepath.Ext(entry.Name())
			if ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".7z" || ext == ".iso" {
				archEntries, err := archiveWalker.IndexArchive(fullPath)
				if err == nil {
					for _, ae := range archEntries {
						if ae.IsDir {
							continue
						}
						// Path reflects internal structure
						vfsPath := fmt.Sprintf("%s/%s", fullPath, ae.Name)
						if err := callback(vfsPath, false, ae.Content); err != nil {
							return err
						}
					}
					continue
				}
				// If archive extraction fails, treat as regular file
			}

			// Regular file
			f, err := i.fs.Open(fullPath)
			if err != nil {
				continue
			}
			
			// Limit file size to 50MB to prevent OOM
			content, err := io.ReadAll(io.LimitReader(f, 50*1024*1024))
			f.Close()
			if err != nil {
				continue
			}

			if err := callback(fullPath, false, content); err != nil {
				return err
			}
		}
	}
	return nil
}
