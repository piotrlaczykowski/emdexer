package watcher

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/vfs"

	_ "github.com/mattn/go-sqlite3"
)

// deltaConfig holds runtime delta-detection settings derived from env vars.
type deltaConfig struct {
	enabled  bool // EMDEX_DELTA_ENABLED (default: true)
	fullHash bool // EMDEX_FULL_HASH (default: false)
}

func loadDeltaConfig() deltaConfig {
	enabled := os.Getenv("EMDEX_DELTA_ENABLED") != "0"
	fullHash := os.Getenv("EMDEX_FULL_HASH") == "1"
	return deltaConfig{enabled: enabled, fullHash: fullHash}
}

// MetadataCache wraps the SQLite file-cache database.
type MetadataCache struct {
	db *sql.DB
}

func NewMetadataCache(dbPath string) (*MetadataCache, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Wait a bit to ensure file is created by SQLite or create it if it doesn't exist
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		f, err := os.Create(dbPath)
		if err == nil {
			f.Close()
		}
	}

	// Explicitly call os.Chmod(dbPath, 0600) to secure SQLite cache permissions
	if err := os.Chmod(dbPath, 0600); err != nil {
		log.Printf("[cache] Warning: Failed to set 0600 permissions on %s: %v", dbPath, err)
	}

	// Base table — create if not present.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS file_cache (
		path         TEXT PRIMARY KEY,
		size         INTEGER,
		mtime        INTEGER,
		partial_hash TEXT,
		full_hash    TEXT,
		algorithm    TEXT,
		last_seen    INTEGER
	)`)
	if err != nil {
		return nil, err
	}

	// Idempotent schema migrations for databases created before this release.
	// We use static SQL strings and handle errors properly, ignoring "duplicate column" errors.
	migrations := []string{
		"ALTER TABLE file_cache ADD COLUMN partial_hash TEXT",
		"ALTER TABLE file_cache ADD COLUMN full_hash TEXT",
		"ALTER TABLE file_cache ADD COLUMN algorithm TEXT",
	}
	for _, stmt := range migrations {
		if _, err := db.Exec(stmt); err != nil {
			// SQLite returns "duplicate column name: <name>" if it already exists.
			// In some versions it might just be "duplicate column name".
			// We check for the substring to be safe.
			if !strings.Contains(err.Error(), "duplicate column name") {
				return nil, fmt.Errorf("migration failed (%q): %w", stmt, err)
			}
		}
	}

	return &MetadataCache{db: db}, nil
}

func (c *MetadataCache) Close() error {
	return c.db.Close()
}

// FileState represents the persisted metadata for a single cached file.
type FileState struct {
	Path        string
	Size        int64
	Mtime       int64
	PartialHash string
	FullHash    string
	Algorithm   string
}

// Poller periodically walks a VFS root, detects changes, and calls handlers.
type Poller struct {
	fs       vfs.FileSystem
	root     string
	cache    *MetadataCache
	interval time.Duration
	handler  func(path string, content []byte) error
	onDelete func(path string) error
	stopCh   chan struct{}
	delta    deltaConfig
}

func NewPoller(
	fs vfs.FileSystem,
	root string,
	cache *MetadataCache,
	interval time.Duration,
	handler func(path string, content []byte) error,
	onDelete func(path string) error,
) *Poller {
	return &Poller{
		fs:       fs,
		root:     root,
		cache:    cache,
		interval: interval,
		handler:  handler,
		onDelete: onDelete,
		stopCh:   make(chan struct{}),
		delta:    loadDeltaConfig(),
	}
}

func (p *Poller) Start() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	log.Printf("[poller] Starting remote VFS poller on %s (interval=%v, delta=%v, fullHash=%v)",
		p.root, p.interval, p.delta.enabled, p.delta.fullHash)

	// Initial poll
	p.poll()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.poll()
		}
	}
}

func (p *Poller) Stop() {
	close(p.stopCh)
}

func (p *Poller) poll() {
	p.pollPath(p.root)
}

func (p *Poller) pollPath(path string) {
	now := time.Now().Unix()
	log.Printf("[poller] Polling %s...", path)

	walkErr := p.recursiveWalk(path, func(filePath string, size int64, mtime int64) {
		log.Printf("[poller] Walking file: %s", filePath)

		var cachedSize int64
		var cachedMtime int64
		var cachedPartial sql.NullString
		var cachedFull sql.NullString
		var cachedAlgo sql.NullString

		queryErr := p.cache.db.QueryRow(
			`SELECT size, mtime, partial_hash, full_hash, algorithm
			   FROM file_cache WHERE path = ?`, filePath,
		).Scan(&cachedSize, &cachedMtime, &cachedPartial, &cachedFull, &cachedAlgo)

		if queryErr == sql.ErrNoRows {
			log.Printf("[poller] New file: %s", filePath)
			p.indexFile(filePath, size, mtime, now)
			return
		}
		if queryErr != nil {
			log.Printf("[poller] Query/Scan error for %s: %v", filePath, queryErr)
			return
		}

		// Delta detection is disabled — use legacy stat-only check.
		if !p.delta.enabled {
			if size != cachedSize || mtime != cachedMtime {
				log.Printf("[poller] Changed file (stat, delta disabled): %s", filePath)
				p.indexFile(filePath, size, mtime, now)
			} else {
				p.touchLastSeen(filePath, now)
			}
			return
		}

		// ── Stage 1: Stat-check ───────────────────────────────────────────────
		// If size or mtime changed we can skip straight to content hashing.
		// If they *match* we still verify with a partial hash (Stage 2) to
		// catch mtime-spoofing / silent overwrites with identical metadata.
		statMatch := size == cachedSize && mtime == cachedMtime
		if !statMatch {
			log.Printf("[poller] Stat changed for %s (size %d→%d, mtime %d→%d) — checking content",
				filePath, cachedSize, size, cachedMtime, mtime)
		} else {
			log.Printf("[poller] Stat match for %s — verifying partial hash", filePath)
		}

		// ── Stage 2: Sparse hash ──────────────────────────────────────────────
		partialHash, err := p.computePartialHash(filePath, size)
		if err != nil {
			// VFS backend does not support io.ReaderAt (or another transient
			// error). Fall back to stat-only: if stats matched we skip, if
			// they changed we re-index.
			log.Printf("[poller] Partial hash unavailable for %s: %v — using stat-only fallback", filePath, err)
			if statMatch {
				p.touchLastSeen(filePath, now)
			} else {
				p.indexFile(filePath, size, mtime, now)
			}
			return
		}

		// ── PREVENT RE-INDEXING STORM: Warm up cache if metadata matches but hash is missing ──
		if statMatch && !cachedPartial.Valid {
			log.Printf("[poller] Metadata match but no cached partial_hash for %s — warming cache", filePath)
			p.updateCacheHashes(filePath, size, mtime, now, partialHash, "")
			return
		}

		if cachedPartial.Valid && cachedPartial.String == partialHash {
			// ── OPTIMIZE POLLING: Metadata changed but hash confirms content is same ──
			if !statMatch {
				log.Printf("[poller] Stat changed but partial hash match for %s — updating metadata cache (%s)", filePath, indexer.DeltaStatChanged)
				p.updateCacheHashes(filePath, size, mtime, now, partialHash, "")
				return
			}

			if !p.delta.fullHash {
				// Content confirmed unchanged — skip Qdrant, update last_seen only.
				log.Printf("[poller] Partial hash match: %s — skipping (%s)", filePath, indexer.DeltaUnchanged)
				p.touchLastSeen(filePath, now)
				return
			}

			// ── Stage 3: Full hash (opt-in via EMDEX_FULL_HASH=1) ────────────
			fullHash, ferr := p.computeFullHash(filePath)
			if ferr != nil {
				log.Printf("[poller] Full hash error for %s: %v — treating as changed", filePath, ferr)
				p.indexFile(filePath, size, mtime, now)
				return
			}
			if cachedFull.Valid && cachedFull.String == fullHash {
				log.Printf("[poller] Full hash match: %s — skipping (%s)", filePath, indexer.DeltaUnchanged)
				p.touchLastSeen(filePath, now)
				return
			}
			// Full hash differs — must re-index.
			log.Printf("[poller] Full hash changed: %s — re-indexing", filePath)
			p.indexFileWithHashes(filePath, size, mtime, now, partialHash, fullHash)
			return
		}

		// Partial hash changed — definitely re-index.
		log.Printf("[poller] Partial hash changed: %s — re-indexing (%s)", filePath, indexer.DeltaChanged)

		// Compute full hash only if requested (so cache stays warm).
		fullHash := ""
		if p.delta.fullHash {
			if fh, ferr := p.computeFullHash(filePath); ferr == nil {
				fullHash = fh
			}
		}
		p.indexFileWithHashes(filePath, size, mtime, now, partialHash, fullHash)
	})

	if walkErr != nil {
		log.Printf("[poller] Walk error: %v - ABORTING deletion detection to avoid transient removals", walkErr)
		return
	}

	// Detect deleted files
	rows, err := p.cache.db.Query("SELECT path, last_seen FROM file_cache")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pth string
			var ls int64
			if err := rows.Scan(&pth, &ls); err != nil {
				log.Printf("[poller] Scan error: %v", err)
				continue
			}
			log.Printf("[poller] Cache entry: %s (last_seen=%d, now=%d)", pth, ls, now)
			if ls < now {
				log.Printf("[poller] Deleted file: %s", pth)
				if err := p.onDelete(pth); err == nil {
					p.cache.db.Exec("DELETE FROM file_cache WHERE path = ?", pth)
				}
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("[poller] Rows error: %v", err)
		}
	}
}

func (p *Poller) recursiveWalk(path string, callback func(path string, size int64, mtime int64)) error {
	entries, err := p.fs.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		var fullPath string
		if path == "." || path == "" {
			fullPath = entry.Name()
		} else {
			fullPath = filepath.Join(path, entry.Name())
		}

		if entry.IsDir() {
			p.recursiveWalk(fullPath, callback)
		} else {
			info, err := entry.Info()
			if err == nil {
				callback(fullPath, info.Size(), info.ModTime().Unix())
			}
		}
	}
	return nil
}

// touchLastSeen updates only last_seen for a skipped file — no Qdrant call.
func (p *Poller) touchLastSeen(path string, now int64) {
	if _, err := p.cache.db.Exec(
		"UPDATE file_cache SET last_seen = ? WHERE path = ?", now, path,
	); err != nil {
		log.Printf("[poller] Failed to update last_seen for %s: %v", path, err)
	}
}

// indexFile opens the file via VFS, calls the handler, computes a partial hash,
// and writes the cache row. Computing the partial hash here means subsequent
// polls can use Stage 2 to skip unchanged files even after a first-time index.
//
// I/O optimisation: the file is opened and read exactly once. The partial hash
// is computed from the already-in-memory content bytes via a bytes.Reader, so
// there is no second VFS open call for the hash step.
func (p *Poller) indexFile(path string, size int64, mtime int64, now int64) {
	log.Printf("[poller] indexFile called for %s", path)
	f, err := p.fs.Open(path)
	if err != nil {
		log.Printf("[poller] Failed to open %s: %v", path, err)
		return
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		log.Printf("[poller] Failed to read %s: %v", path, err)
		return
	}

	if err := p.handler(path, content); err != nil {
		log.Printf("[poller] Handler failed for %s: %v", path, err)
		return
	}

	// Best-effort partial hash so the cache is warm for the next poll.
	// Compute directly from in-memory bytes — no second VFS open required.
	partialHash, herr := indexer.CalculatePartialHash(bytes.NewReader(content), int64(len(content)))
	if herr != nil {
		log.Printf("[poller] Partial hash skipped for %s: %v", path, herr)
		partialHash = ""
	}

	var phVal interface{}
	if partialHash != "" {
		phVal = partialHash
	}
	_, err = p.cache.db.Exec(
		`REPLACE INTO file_cache (path, size, mtime, partial_hash, full_hash, algorithm, last_seen)
		 VALUES (?, ?, ?, ?, NULL, 'xxh3', ?)`,
		path, size, mtime, phVal, now,
	)
	if err != nil {
		log.Printf("[poller] Cache update failed for %s: %v", path, err)
	}
}

// indexFileWithHashes is like indexFile but callers supply pre-computed hash
// values (e.g. from Stage 2/3 in poll()). The file is opened only once.
func (p *Poller) indexFileWithHashes(path string, size, mtime, now int64, partialHash, fullHash string) {
	log.Printf("[poller] indexFileWithHashes called for %s", path)
	f, err := p.fs.Open(path)
	if err != nil {
		log.Printf("[poller] Failed to open %s: %v", path, err)
		return
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		log.Printf("[poller] Failed to read %s: %v", path, err)
		return
	}

	if err := p.handler(path, content); err != nil {
		log.Printf("[poller] Handler failed for %s: %v", path, err)
		return
	}

	var fhVal interface{}
	if fullHash != "" {
		fhVal = fullHash
	}
	_, err = p.cache.db.Exec(
		`REPLACE INTO file_cache (path, size, mtime, partial_hash, full_hash, algorithm, last_seen)
		 VALUES (?, ?, ?, ?, ?, 'xxh3', ?)`,
		path, size, mtime, partialHash, fhVal, now,
	)
	if err != nil {
		log.Printf("[poller] Cache update failed for %s: %v", path, err)
	}
}

// computePartialHash opens the file via VFS and calculates the sparse hash.
// All file access goes through p.fs.Open — never os.Open directly.
func (p *Poller) computePartialHash(path string, size int64) (string, error) {
	rat, err := indexer.OpenReaderAt(p.fs, path)
	if err != nil {
		return "", err
	}
	defer rat.Close()
	return indexer.CalculatePartialHash(rat, size)
}

// computeFullHash opens the file via VFS and hashes the entire content.
// All file access goes through p.fs.Open — never os.Open directly.
func (p *Poller) computeFullHash(path string) (string, error) {
	f, err := p.fs.Open(path)
	if err != nil {
		return "", fmt.Errorf("vfs.Open %q: %w", path, err)
	}
	defer f.Close()
	return indexer.CalculateFullHash(f.(io.Reader))
}

// updateCacheHashes updates the metadata and hashes in the database WITHOUT triggering re-indexing.
func (p *Poller) updateCacheHashes(path string, size, mtime, now int64, partialHash, fullHash string) {
	var phVal interface{}
	if partialHash != "" {
		phVal = partialHash
	}
	var fhVal interface{}
	if fullHash != "" {
		fhVal = fullHash
	}

	_, err := p.cache.db.Exec(
		`UPDATE file_cache SET size = ?, mtime = ?, partial_hash = ?, full_hash = ?, algorithm = 'xxh3', last_seen = ?
		 WHERE path = ?`,
		size, mtime, phVal, fhVal, now, path,
	)
	if err != nil {
		log.Printf("[poller] Metadata update failed for %s: %v", path, err)
	}
}
