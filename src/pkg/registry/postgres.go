package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

type DBNodeRegistry struct {
	db        *sql.DB
	replicaDB *sql.DB // optional read replica; nil means use db for reads too
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
		_ = db.Close()
		return nil, fmt.Errorf("[registry] failed to ping postgres: %w", err)
	}

	r := &DBNodeRegistry{db: db}
	if err := r.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("[registry] migration failed: %w", err)
	}

	log.Println("[registry] PostgreSQL backend ready")
	return r, nil
}

// OpenReplica attaches an optional read replica connection to the registry.
// Read queries (List) are routed to the replica when available; write queries
// always use the primary. On any error the replica is silently skipped and
// all queries continue to use the primary — never fatal.
func (r *DBNodeRegistry) OpenReplica(replicaDSN string) {
	db, err := sql.Open("postgres", replicaDSN)
	if err != nil {
		log.Printf("⚠️  [registry] read replica open failed (%v) — all queries use primary", err)
		return
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		log.Printf("⚠️  [registry] read replica ping failed (%v) — all queries use primary", err)
		return
	}
	r.replicaDB = db
	log.Println("[registry] PostgreSQL read replica attached")
}

// DB returns the primary write connection. Exposed for read-only aggregate
// queries (e.g. namespace stats) that need direct SQL access.
func (r *DBNodeRegistry) DB() *sql.DB { return r.db }

// readDB returns replicaDB when available, otherwise the primary.
func (r *DBNodeRegistry) readDB() *sql.DB {
	if r.replicaDB != nil {
		return r.replicaDB
	}
	return r.db
}

// migrate creates the registered_nodes table and adds columns for namespace aggregation.
// All ALTER TABLE statements use IF NOT EXISTS for idempotent re-runs.
func (r *DBNodeRegistry) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS registered_nodes (
    id             TEXT        PRIMARY KEY,
    url            TEXT        NOT NULL,
    collections    JSONB       NOT NULL DEFAULT '[]',
    registered_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE registered_nodes ADD COLUMN IF NOT EXISTS namespaces     JSONB       NOT NULL DEFAULT '[]';
ALTER TABLE registered_nodes ADD COLUMN IF NOT EXISTS protocol       TEXT        NOT NULL DEFAULT '';
ALTER TABLE registered_nodes ADD COLUMN IF NOT EXISTS health_status  TEXT        NOT NULL DEFAULT 'unknown';
ALTER TABLE registered_nodes ADD COLUMN IF NOT EXISTS last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT NOW();
`
	_, err := r.db.Exec(ddl)
	return err
}

func (r *DBNodeRegistry) Register(ctx context.Context, n NodeInfo) error {
	n.RegisteredAt = time.Now()
	n.LastHeartbeat = time.Now()
	colsJSON, _ := json.Marshal(n.Collections)
	nsJSON, _ := json.Marshal(n.Namespaces)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO registered_nodes (id, url, collections, namespaces, protocol, health_status, last_heartbeat, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE
		  SET url            = EXCLUDED.url,
		      collections    = EXCLUDED.collections,
		      namespaces     = EXCLUDED.namespaces,
		      protocol       = EXCLUDED.protocol,
		      health_status  = EXCLUDED.health_status,
		      last_heartbeat = EXCLUDED.last_heartbeat,
		      registered_at  = EXCLUDED.registered_at
	`, n.ID, n.URL, string(colsJSON), string(nsJSON), n.Protocol, n.HealthStatus, n.LastHeartbeat, n.RegisteredAt)
	if err != nil {
		log.Printf("[registry] Register error: %v", err)
		return err
	}
	return nil
}

func (r *DBNodeRegistry) Deregister(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM registered_nodes WHERE id = $1`, id); err != nil {
		log.Printf("[registry] Deregister error: %v", err)
		return err
	}
	return nil
}

func (r *DBNodeRegistry) List(ctx context.Context) ([]NodeInfo, error) {
	rows, err := r.readDB().QueryContext(ctx, `
		SELECT id, url, collections, namespaces, protocol, health_status, last_heartbeat, registered_at
		FROM registered_nodes ORDER BY registered_at`)
	if err != nil {
		log.Printf("[registry] List error: %v", err)
		return []NodeInfo{}, err
	}
	defer func() { _ = rows.Close() }()

	out := []NodeInfo{}
	for rows.Next() {
		var n NodeInfo
		var colsJSON, nsJSON string
		if err := rows.Scan(&n.ID, &n.URL, &colsJSON, &nsJSON, &n.Protocol, &n.HealthStatus, &n.LastHeartbeat, &n.RegisteredAt); err != nil {
			log.Printf("[registry] scan error: %v", err)
			continue
		}
		if err := json.Unmarshal([]byte(colsJSON), &n.Collections); err != nil {
			n.Collections = []string{}
		}
		if err := json.Unmarshal([]byte(nsJSON), &n.Namespaces); err != nil {
			n.Namespaces = []string{}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[registry] rows error: %v", err)
		return out, err
	}
	return out, nil
}
