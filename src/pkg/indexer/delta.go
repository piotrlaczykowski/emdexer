package indexer

import (
	"fmt"
	"io"
	"io/fs"

	"github.com/zeebo/xxh3"
)

// DeltaResult communicates the change-detection outcome for a single file.
type DeltaResult int

const (
	// DeltaChanged means the file content has changed and must be re-indexed.
	DeltaChanged DeltaResult = iota
	// DeltaStatChanged means size or mtime changed but content is still the same
	// (rare; e.g. touch + rewrite with identical bytes). Treated as Changed upstream.
	DeltaStatChanged
	// DeltaUnchanged means all detection stages passed — file can be skipped.
	DeltaUnchanged
)

func (r DeltaResult) String() string {
	switch r {
	case DeltaChanged:
		return "Changed"
	case DeltaStatChanged:
		return "StatChanged"
	case DeltaUnchanged:
		return "Unchanged"
	default:
		return "Unknown"
	}
}

const partialHashChunk = 1 * 1024 * 1024 // 1 MB

// CalculatePartialHash hashes the first 1 MB and the last 1 MB of the file
// (or the whole file if it is smaller than 2 MB).
// The reader must implement io.ReaderAt so we can seek without a full read.
func CalculatePartialHash(r io.ReaderAt, size int64) (string, error) {
	h := xxh3.New()

	readChunk := func(offset, length int64) error {
		if length > 32*1024*1024 { // Cap buffer at 32MB for safety
			return fmt.Errorf("readChunk: length %d too large", length)
		}
		buf := make([]byte, int(length))
		n, err := r.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return fmt.Errorf("ReadAt offset=%d: %w", offset, err)
		}
		_, werr := h.Write(buf[:n])
		return werr
	}

	if size <= 2*partialHashChunk {
		// File fits entirely — hash it all as the "partial" hash.
		if err := readChunk(0, size); err != nil {
			return "", err
		}
	} else {
		// First 1 MB
		if err := readChunk(0, partialHashChunk); err != nil {
			return "", err
		}
		// Last 1 MB
		if err := readChunk(size-partialHashChunk, partialHashChunk); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// CalculateFullHash hashes the entire file content.
func CalculateFullHash(r io.Reader) (string, error) {
	h := xxh3.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("full hash copy: %w", err)
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// readerAtFile is satisfied by any fs.File that also implements io.ReaderAt
// (e.g. *os.File, our VFS wrappers that embed *os.File).
type readerAtFile interface {
	fs.File
	io.ReaderAt
}

// OpenReaderAt opens the named file through the provided VFS and asserts that
// it implements io.ReaderAt — which all our concrete VFS backends do.
// It returns an error rather than falling back to os.Open so that every file
// access is guaranteed to go through the scoped VFS.
func OpenReaderAt(vfsFS interface {
	Open(name string) (fs.File, error)
}, name string) (readerAtFile, error) {
	f, err := vfsFS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("vfs.Open %q: %w", name, err)
	}
	rat, ok := f.(readerAtFile)
	if !ok {
		f.Close()
		return nil, fmt.Errorf("vfs file %q does not implement io.ReaderAt (type %T)", name, f)
	}
	return rat, nil
}
