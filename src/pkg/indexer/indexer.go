package indexer

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

// WalkStats holds counters accumulated during a Walk.
type WalkStats struct {
	FilesIndexed       int
	FilesSkipped       int
	DirsSkipped        int
	ArchivesFound      int
	ArchivesFailed     int
	FilesSkippedExt    int // skipped due to extension filter — zero I/O
	DirsSkippedExclude int // skipped due to exclude path match
}

// WalkConfig controls which files and directories are skipped during a walk.
type WalkConfig struct {
	SkipExts     map[string]bool
	ExcludePaths []string
}

// BuildWalkConfig builds a WalkConfig from node feature flags and the
// EMDEX_EXCLUDE_PATHS environment variable.
func BuildWalkConfig(whisperEnabled, visionEnabled, frameEnabled, ocrEnabled bool) WalkConfig {
	skip := make(map[string]bool)

	// Audio: skip if Whisper is disabled (no fallback extractor)
	if !whisperEnabled {
		for _, ext := range []string{".mp3", ".wav", ".m4a", ".ogg", ".flac"} {
			skip[ext] = true
		}
	}

	// Video: skip if BOTH Whisper and Frame extraction are disabled
	if !whisperEnabled && !frameEnabled {
		for _, ext := range []string{".mp4", ".mkv", ".avi", ".mov", ".webm", ".lrf"} {
			skip[ext] = true
		}
	}

	// Images: skip if BOTH Vision and OCR are disabled
	if !visionEnabled && !ocrEnabled {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".bmp", ".gif", ".tiff", ".tif", ".heic", ".webp"} {
			skip[ext] = true
		}
	}

	// Raw camera formats: always skip — no extractor supports them
	for _, ext := range []string{".dng", ".cr2", ".nef", ".arw", ".orf", ".rw2", ".raw"} {
		skip[ext] = true
	}

	// Parse EMDEX_EXCLUDE_PATHS: comma-separated dir names or glob patterns
	var excludePaths []string
	if v := os.Getenv("EMDEX_EXCLUDE_PATHS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				excludePaths = append(excludePaths, p)
			}
		}
	}

	return WalkConfig{SkipExts: skip, ExcludePaths: excludePaths}
}

// shouldExclude returns true if name or fullPath matches any exclude pattern.
func (c *WalkConfig) shouldExclude(name, fullPath string) bool {
	for _, pattern := range c.ExcludePaths {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
		if strings.Contains(fullPath, pattern) {
			return true
		}
	}
	return false
}

type Indexer struct {
	fs  vfs.FileSystem
	cfg WalkConfig
}

func NewIndexer(fs vfs.FileSystem, cfg WalkConfig) *Indexer {
	return &Indexer{fs: fs, cfg: cfg}
}

func (i *Indexer) Walk(root string, callback func(path string, isDir bool, content []byte) error) (WalkStats, error) {
	var stats WalkStats
	err := i.recursiveWalk(".", callback, &stats)
	log.Printf("[indexer] Walk complete: root=%s indexed=%d skipped=%d skipped_ext=%d dirs_skipped=%d dirs_excluded=%d archives=%d",
		root, stats.FilesIndexed, stats.FilesSkipped, stats.FilesSkippedExt,
		stats.DirsSkipped, stats.DirsSkippedExclude, stats.ArchivesFound)
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

		// Check path exclusions before any I/O (applies to both dirs and files)
		if i.cfg.shouldExclude(entry.Name(), fullPath) {
			if entry.IsDir() {
				stats.DirsSkippedExclude++
				log.Printf("[indexer] Excluded dir: %s", fullPath)
			} else {
				stats.FilesSkipped++
			}
			continue
		}

		if entry.IsDir() {
			if err := i.recursiveWalk(fullPath, callback, stats); err != nil {
				log.Printf("[indexer] Skipping unreadable dir: path=%s err=%v", fullPath, err)
				stats.DirsSkipped++
			}
		} else {
			ext := strings.ToLower(filepath.Ext(entry.Name()))

			// Skip by extension before opening file — zero I/O
			if i.cfg.SkipExts[ext] {
				stats.FilesSkippedExt++
				continue
			}

			// Check if it's an archive
			if ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".7z" || ext == ".iso" {
				stats.ArchivesFound++
				archEntries, err := archiveWalker.IndexArchive(fullPath)
				if err == nil {
					for _, ae := range archEntries {
						if ae.IsDir {
							continue
						}
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
