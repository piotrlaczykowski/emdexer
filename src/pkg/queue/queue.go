package queue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/qdrant/go-client/qdrant"
	_ "github.com/mattn/go-sqlite3"
)

type BatchItem struct {
	ID     int64
	Points []*qdrant.PointStruct
}

type PersistentQueue struct {
	db   *sql.DB
	mu   sync.Mutex
	name string
}

func NewPersistentQueue(dbPath string) (*PersistentQueue, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS queue (id INTEGER PRIMARY KEY AUTOINCREMENT, data BLOB, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)")
	if err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	// Security: restrict database file permissions to 0600
	if err := os.Chmod(dbPath, 0600); err != nil {
		log.Printf("[queue] Warning: Failed to set 0600 permissions on %s: %v", dbPath, err)
	}

	return &PersistentQueue{db: db}, nil
}

func (q *PersistentQueue) Enqueue(points []*qdrant.PointStruct) error {
	data, err := json.Marshal(points)
	if err != nil {
		return fmt.Errorf("marshal points: %w", err)
	}

	_, err = q.db.Exec("INSERT INTO queue (data) VALUES (?)", data)
	return err
}

func (q *PersistentQueue) Dequeue() (*BatchItem, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var id int64
	var data []byte
	err := q.db.QueryRow("SELECT id, data FROM queue ORDER BY id ASC LIMIT 1").Scan(&id, &data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var points []*qdrant.PointStruct
	if err := json.Unmarshal(data, &points); err != nil {
		return nil, fmt.Errorf("unmarshal points: %w", err)
	}

	return &BatchItem{ID: id, Points: points}, nil
}

func (q *PersistentQueue) Delete(id int64) error {
	_, err := q.db.Exec("DELETE FROM queue WHERE id = ?", id)
	return err
}

func (q *PersistentQueue) Close() error {
	return q.db.Close()
}
