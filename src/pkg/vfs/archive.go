package vfs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func getMaxArchiveEntrySize() int64 {
	maxSizeStr := os.Getenv("EMDEX_MAX_ARCHIVE_ENTRY_SIZE")
	maxSize := int64(10 * 1024 * 1024) // 10MB default
	if maxSizeStr != "" {
		if strings.HasSuffix(maxSizeStr, "MB") {
			fmt.Sscanf(strings.TrimSuffix(maxSizeStr, "MB"), "%d", &maxSize)
			maxSize *= 1024 * 1024
		} else if strings.HasSuffix(maxSizeStr, "GB") {
			fmt.Sscanf(strings.TrimSuffix(maxSizeStr, "GB"), "%d", &maxSize)
			maxSize *= 1024 * 1024 * 1024
		} else {
			fmt.Sscanf(maxSizeStr, "%d", &maxSize)
		}
	}
	return maxSize
}

func sanitizeArchivePath(p string) (string, error) {
	v := filepath.Clean(p)
	if filepath.IsAbs(v) || strings.HasPrefix(v, ".."+string(filepath.Separator)) || v == ".." {
		return "", fmt.Errorf("invalid archive path: %s", p)
	}
	return v, nil
}

type ArchiveFileSystem struct {
	baseFS FileSystem
}

func NewArchiveFileSystem(base FileSystem) *ArchiveFileSystem {
	return &ArchiveFileSystem{baseFS: base}
}

type ArchiveEntry struct {
	Name    string
	Content []byte
	IsDir   bool
	Size    int64
	MTime   time.Time
}

func getMaxArchiveMB() int64 {
	maxSizeStr := os.Getenv("EMDEX_MAX_ARCHIVE_MB")
	maxSize := int64(512) // 512MB default
	if maxSizeStr != "" {
		fmt.Sscanf(maxSizeStr, "%d", &maxSize)
	}
	return maxSize * 1024 * 1024
}

func (a *ArchiveFileSystem) IndexArchive(path string) ([]ArchiveEntry, error) {
	ext := strings.ToLower(filepath.Ext(path))
	file, err := a.baseFS.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Check archive size limit
	maxSize := getMaxArchiveMB()
	buf, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > maxSize {
		return nil, fmt.Errorf("archive exceeds maximum size limit of %d MB", maxSize/(1024*1024))
	}

	switch {
	case ext == ".zip":
		return a.readZip(buf)
	case ext == ".tar":
		return a.readTar(buf)
	case ext == ".gz" || strings.HasSuffix(path, ".tar.gz"):
		return a.readTarGz(buf)
	case ext == ".7z":
		return a.read7z(buf)
	case ext == ".iso":
		return a.readIso(buf)
	}

	return nil, fmt.Errorf("unsupported archive type: %s", ext)
}

func (a *ArchiveFileSystem) readZip(buf []byte) ([]ArchiveEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, err
	}

	var entries []ArchiveEntry
	for _, f := range reader.File {
		cleanPath, err := sanitizeArchivePath(f.Name)
		if err != nil {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		// Limit archive entry size
		maxSize := getMaxArchiveEntrySize()
		content, _ := io.ReadAll(io.LimitReader(rc, maxSize))
		rc.Close()

		entries = append(entries, ArchiveEntry{
			Name:    cleanPath,
			Content: content,
			IsDir:   f.FileInfo().IsDir(),
			Size:    f.FileInfo().Size(),
			MTime:   f.Modified,
		})
	}
	return entries, nil
}

func (a *ArchiveFileSystem) readTar(buf []byte) ([]ArchiveEntry, error) {
	tr := tar.NewReader(bytes.NewReader(buf))
	var entries []ArchiveEntry
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		var content []byte
		cleanPath, err := sanitizeArchivePath(header.Name)
		if err != nil {
			continue
		}
		if header.Typeflag == tar.TypeReg {
			// Limit tar entry size
			maxSize := getMaxArchiveEntrySize()
			content, _ = io.ReadAll(io.LimitReader(tr, maxSize))
		}

		entries = append(entries, ArchiveEntry{
			Name:    cleanPath,
			Content: content,
			IsDir:   header.Typeflag == tar.TypeDir,
			Size:    header.Size,
			MTime:   header.ModTime,
		})
	}
	return entries, nil
}

func (a *ArchiveFileSystem) readTarGz(buf []byte) ([]ArchiveEntry, error) {
	gr, err := gzip.NewReader(bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	// Limit decompression size to prevent ZIP bomb
	maxSize := getMaxArchiveMB()
	uncompressed, err := io.ReadAll(io.LimitReader(gr, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(uncompressed)) > maxSize {
		return nil, fmt.Errorf("decompressed archive exceeds maximum size limit of %d MB", maxSize/(1024*1024))
	}

	return a.readTar(uncompressed)
}

func (a *ArchiveFileSystem) read7z(buf []byte) ([]ArchiveEntry, error) {
	// 7z implementation placeholder - using '7zz' or '7z' CLI if available
	// Better: Use a library like "github.com/saracen/go7z"
	return nil, fmt.Errorf("7z support requires external lib or CLI")
}

func (a *ArchiveFileSystem) readIso(buf []byte) ([]ArchiveEntry, error) {
	// ISO implementation placeholder
	return nil, fmt.Errorf("iso support requires external lib or CLI")
}
