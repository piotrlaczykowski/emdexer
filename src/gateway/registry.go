package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type NodeInfo struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Collections  []string  `json:"collections"`
	RegisteredAt time.Time `json:"registered_at"`
}

func deepCopyNodeInfo(n NodeInfo) NodeInfo {
	cols := make([]string, len(n.Collections))
	copy(cols, n.Collections)
	return NodeInfo{
		ID:           n.ID,
		URL:          n.URL,
		Collections:  cols,
		RegisteredAt: n.RegisteredAt,
	}
}

// NodeRegistry is the interface that all registry backends must implement.
type NodeRegistry interface {
	// Register adds or updates a node in the registry.
	Register(n NodeInfo)
	// Deregister removes a node from the registry by ID.
	Deregister(id string)
	// List returns all currently registered nodes.
	List() []NodeInfo
}

// ------------------------------------------------------------
// FileNodeRegistry — local nodes.json backend (default)
// ------------------------------------------------------------

type FileNodeRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]NodeInfo
	dataFile string
}

func NewFileNodeRegistry(dataFile string) *FileNodeRegistry {
	if dir := filepath.Dir(dataFile); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			log.Printf("[registry] Failed to create directory %s: %v", dir, err)
		}
	}
	r := &FileNodeRegistry{
		nodes:    make(map[string]NodeInfo),
		dataFile: dataFile,
	}
	r.load()
	return r
}

func (r *FileNodeRegistry) load() {
	data, err := os.ReadFile(r.dataFile)
	if err != nil {
		return
	}
	var nodes []NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		log.Printf("[registry] Failed to parse %s: %v", r.dataFile, err)
		return
	}
	for _, n := range nodes {
		r.nodes[n.ID] = deepCopyNodeInfo(n)
	}
}

func (r *FileNodeRegistry) persist() {
	nodes := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		nodes = append(nodes, deepCopyNodeInfo(n))
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		log.Printf("[registry] Failed to marshal nodes: %v", err)
		return
	}
	tmp := r.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Printf("[registry] Failed to write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, r.dataFile); err != nil {
		log.Printf("[registry] Failed to rename %s to %s: %v", tmp, r.dataFile, err)
	}
}

func (r *FileNodeRegistry) Register(n NodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n.RegisteredAt = time.Now()
	r.nodes[n.ID] = deepCopyNodeInfo(n)
	r.persist()
}

func (r *FileNodeRegistry) Deregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
	r.persist()
}

func (r *FileNodeRegistry) List() []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, deepCopyNodeInfo(n))
	}
	return out
}

// ------------------------------------------------------------
// DBNodeRegistry — PostgreSQL backend (HA mode)
// ------------------------------------------------------------

type DBNodeRegistry struct {
	db *sql.DB
}

// NewDBNodeRegistry opens a PostgreSQL connection, runs auto-migration,
// and returns a ready-to-use DBNodeRegistry.
func NewDBNodeRegistry(dsn string) (*DBNodeRegistry, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("[registry] failed to open postgres: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("[registry] failed to ping postgres: %w", err)
	}

	r := &DBNodeRegistry{db: db}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("[registry] migration failed: %w", err)
	}

	log.Println("[registry] PostgreSQL backend ready")
	return r, nil
}

// migrate creates the registered_nodes table if it does not already exist.
func (r *DBNodeRegistry) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS registered_nodes (
    id           TEXT        PRIMARY KEY,
    url          TEXT        NOT NULL,
    collections  JSONB       NOT NULL DEFAULT '[]',
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	_, err := r.db.Exec(ddl)
	return err
}

func (r *DBNodeRegistry) Register(n NodeInfo) {
	n.RegisteredAt = time.Now()
	colsJSON, _ := json.Marshal(n.Collections)
	_, err := r.db.Exec(`
		INSERT INTO registered_nodes (id, url, collections, registered_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE
		  SET url          = EXCLUDED.url,
		      collections  = EXCLUDED.collections,
		      registered_at = EXCLUDED.registered_at
	`, n.ID, n.URL, string(colsJSON), n.RegisteredAt)
	if err != nil {
		log.Printf("[registry] Register error: %v", err)
	}
}

func (r *DBNodeRegistry) Deregister(id string) {
	if _, err := r.db.Exec(`DELETE FROM registered_nodes WHERE id = $1`, id); err != nil {
		log.Printf("[registry] Deregister error: %v", err)
	}
}

func (r *DBNodeRegistry) List() []NodeInfo {
	rows, err := r.db.Query(`SELECT id, url, collections, registered_at FROM registered_nodes ORDER BY registered_at`)
	if err != nil {
		log.Printf("[registry] List error: %v", err)
		return nil
	}
	defer rows.Close()

	var out []NodeInfo
	for rows.Next() {
		var n NodeInfo
		var colsJSON string
		if err := rows.Scan(&n.ID, &n.URL, &colsJSON, &n.RegisteredAt); err != nil {
			log.Printf("[registry] scan error: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(colsJSON), &n.Collections); err != nil {
			n.Collections = []string{}
		}
		out = append(out, n)
	}
	return out
}

// ------------------------------------------------------------
// Registry factory — picks backend based on env vars
// ------------------------------------------------------------

// newRegistry returns a DBNodeRegistry if POSTGRES_URL is set,
// otherwise falls back to FileNodeRegistry.
func newRegistry(dataFile string) NodeRegistry {
	if dsn := os.Getenv("POSTGRES_URL"); dsn != "" {
		log.Printf("[registry] POSTGRES_URL detected — using PostgreSQL backend")
		reg, err := NewDBNodeRegistry(dsn)
		if err != nil {
			log.Printf("[registry] WARNING: PostgreSQL init failed (%v) — falling back to FileNodeRegistry", err)
		} else {
			return reg
		}
	}
	log.Printf("[registry] Using FileNodeRegistry at %s", dataFile)
	return NewFileNodeRegistry(dataFile)
}
