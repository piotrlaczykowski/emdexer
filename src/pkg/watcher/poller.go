package watcher

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/piotrlaczykowski/emdexer/vfs"

	_ "github.com/mattn/go-sqlite3"
)

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

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS file_cache (
		path TEXT PRIMARY KEY,
		size INTEGER,
		mtime INTEGER,
		last_seen INTEGER
	)`)
	if err != nil {
		return nil, err
	}

	return &MetadataCache{db: db}, nil
}

func (c *MetadataCache) Close() error {
	return c.db.Close()
}

type FileState struct {
	Path  string
	Size  int64
	Mtime int64
}

type Poller struct {
	fs           vfs.FileSystem
	root         string
	cache        *MetadataCache
	interval     time.Duration
	handler      func(path string, content []byte) error
	onDelete     func(path string) error
	stopCh       chan struct{}
}

func NewPoller(fs vfs.FileSystem, root string, cache *MetadataCache, interval time.Duration, handler func(path string, content []byte) error, onDelete func(path string) error) *Poller {
	return &Poller{
		fs:       fs,
		root:     root,
		cache:    cache,
		interval: interval,
		handler:  handler,
		onDelete: onDelete,
		stopCh:   make(chan struct{}),
	}
}

func (p *Poller) Start() {
	ticker := time.NewTicker(p.interval)
	log.Printf("[poller] Starting remote VFS poller on %s (interval=%v)", p.root, p.interval)
	
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

	err := p.recursiveWalk(path, func(filePath string, size int64, mtime int64) {
		log.Printf("[poller] Walking file: %s", filePath)
		
		var cachedSize int64
		var cachedMtime int64
		err := p.cache.db.QueryRow("SELECT size, mtime FROM file_cache WHERE path = ?", filePath).Scan(&cachedSize, &cachedMtime)
		
		if err == sql.ErrNoRows {
			log.Printf("[poller] New file: %s", filePath)
			p.indexFile(filePath, size, mtime, now)
		} else if err == nil {
			if size != cachedSize || mtime != cachedMtime {
				log.Printf("[poller] Changed file: %s", filePath)
				p.indexFile(filePath, size, mtime, now)
			} else {
				// Update last_seen
				p.cache.db.Exec("UPDATE file_cache SET last_seen = ? WHERE path = ?", now, filePath)
			}
		}
	})

	if err != nil {
		log.Printf("[poller] Walk error: %v", err)
	}

	// Detect deleted files
	rows, err := p.cache.db.Query("SELECT path, last_seen FROM file_cache")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var pth string
			var ls int64
			rows.Scan(&pth, &ls)
			log.Printf("[poller] Cache entry: %s (last_seen=%d, now=%d)", pth, ls, now)
			if ls < now {
				log.Printf("[poller] Deleted file: %s", pth)
				if err := p.onDelete(pth); err == nil {
					p.cache.db.Exec("DELETE FROM file_cache WHERE path = ?", pth)
				}
			}
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

func (p *Poller) indexFile(path string, size int64, mtime int64, now int64) {
	log.Printf("[poller] indexFile called for %s", path)
	f, err := p.fs.Open(path)
	if err != nil {
		log.Printf("[poller] Failed to open %s: %v", path, err)
		return
	}
	defer f.Close()

	if err := p.handler(path, nil); err != nil {
		log.Printf("[poller] Handler failed for %s: %v", path, err)
		return
	}

	_, err = p.cache.db.Exec("REPLACE INTO file_cache (path, size, mtime, last_seen) VALUES (?, ?, ?, ?)", path, size, mtime, now)
	if err != nil {
		log.Printf("[poller] Cache update failed for %s: %v", path, err)
	}
}
