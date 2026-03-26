package indexer

import (
	"fmt"
	"io"
	"log"
	"path/filepath"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

// WalkStats holds counters accumulated during a Walk.
type WalkStats struct {
	FilesIndexed   int
	FilesSkipped   int
	DirsSkipped    int
	ArchivesFound  int
	ArchivesFailed int
}

type Indexer struct {
	fs vfs.FileSystem
}

func NewIndexer(fs vfs.FileSystem) *Indexer {
	return &Indexer{fs: fs}
}

func (i *Indexer) Walk(root string, callback func(path string, isDir bool, content []byte) error) (WalkStats, error) {
	var stats WalkStats
	err := i.recursiveWalk(".", callback, &stats)
	log.Printf("[indexer] Walk complete: root=%s indexed=%d skipped=%d dirs_skipped=%d archives=%d archive_errors=%d",
		root, stats.FilesIndexed, stats.FilesSkipped, stats.DirsSkipped, stats.ArchivesFound, stats.ArchivesFailed)
	return stats, err
}

func (i *Indexer) recursiveWalk(path string, callback func(path string, isDir bool, content []byte) error, stats *WalkStats) error {
	entries, err := i.fs.ReadDir(path)
	if err != nil {
		log.Printf("[indexer] ReadDir failed: path=%s err=%v", path, err)
		return fmt.Errorf("failed to read dir %s: %w", path, err)
	}

	archiveWalker := vfs.NewArchiveFileSystem(i.fs)

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			if err := i.recursiveWalk(fullPath, callback, stats); err != nil {
				log.Printf("[indexer] Skipping unreadable dir: path=%s err=%v", fullPath, err)
				stats.DirsSkipped++
			}
		} else {
			// Check if it's an archive
			ext := filepath.Ext(entry.Name())
			if ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".7z" || ext == ".iso" {
				stats.ArchivesFound++
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
						stats.FilesIndexed++
					}
					continue
				}
				// Archive extraction failed — fall through to regular file treatment
				log.Printf("[indexer] Archive extraction failed, treating as regular file: path=%s err=%v", fullPath, err)
				stats.ArchivesFailed++
			}

			// Regular file
			f, err := i.fs.Open(fullPath)
			if err != nil {
				log.Printf("[indexer] Skipping file (open failed): path=%s err=%v", fullPath, err)
				stats.FilesSkipped++
				continue
			}

			// Limit file size to 50MB to prevent OOM
			content, err := io.ReadAll(io.LimitReader(f, 50*1024*1024))
			f.Close()
			if err != nil {
				log.Printf("[indexer] Skipping file (read failed): path=%s err=%v", fullPath, err)
				stats.FilesSkipped++
				continue
			}

			if err := callback(fullPath, false, content); err != nil {
				return err
			}
			stats.FilesIndexed++
		}
	}
	return nil
}
