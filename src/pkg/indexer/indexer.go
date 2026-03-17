package indexer

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

type Indexer struct {
	fs vfs.FileSystem
}

func NewIndexer(fs vfs.FileSystem) *Indexer {
	return &Indexer{fs: fs}
}

func (i *Indexer) Walk(root string, callback func(path string, isDir bool, content []byte) error) error {
	if flatFS, ok := i.fs.(vfs.FlatListingFS); ok {
		return i.flatWalk(flatFS, root, callback)
	}
	return i.recursiveWalk(root, callback)
}

// flatWalk uses ReadDirFlat for backends like S3 that can list all objects in
// a single paginated API call, avoiding recursive ReadDir round-trips.
func (i *Indexer) flatWalk(flatFS vfs.FlatListingFS, root string, callback func(path string, isDir bool, content []byte) error) error {
	entries, err := flatFS.ReadDirFlat(root)
	if err != nil {
		return fmt.Errorf("flat walk %s: %w", root, err)
	}

	archiveWalker := vfs.NewArchiveFileSystem(i.fs)

	for _, entry := range entries {
		if entry.IsDir {
			continue
		}

		ext := filepath.Ext(entry.Path)
		if ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".7z" || ext == ".iso" {
			archEntries, archErr := archiveWalker.IndexArchive(entry.Path)
			if archErr == nil {
				for _, ae := range archEntries {
					if ae.IsDir {
						continue
					}
					vfsPath := fmt.Sprintf("%s/%s", entry.Path, ae.Name)
					if err := callback(vfsPath, false, ae.Content); err != nil {
						return err
					}
				}
				continue
			}
		}

		f, err := i.fs.Open(entry.Path)
		if err != nil {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(f, 50*1024*1024))
		f.Close()
		if err != nil {
			continue
		}

		if err := callback(entry.Path, false, content); err != nil {
			return err
		}
	}
	return nil
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
