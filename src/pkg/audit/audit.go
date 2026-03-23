package audit

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Timestamp time.Time              `json:"timestamp"`
	Action    string                 `json:"action"`
	User      string                 `json:"user,omitempty"`
	Query     string                 `json:"query,omitempty"`
	Namespace string                 `json:"namespace,omitempty"`
	Results   int                    `json:"results_count,omitempty"`
	LatencyMS int64                  `json:"latency_ms"`
	Status    int                    `json:"status"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// auditCh buffers entries so Log() never blocks the HTTP hot path.
// Entries are dropped (with a log warning) when the buffer is full.
const bufferSize = 1000

var auditCh = make(chan Entry, bufferSize)

func init() {
	go func() {
		for entry := range auditCh {
			writeEntry(entry)
		}
	}()
}

// Log enqueues an audit entry for async disk write. It never blocks the caller.
func Log(entry Entry) {
	entry.Timestamp = time.Now()
	select {
	case auditCh <- entry:
	default:
		log.Printf("[audit] buffer full (%d), dropping entry action=%s", bufferSize, entry.Action)
	}
}

func writeEntry(entry Entry) {
	logPath := os.Getenv("EMDEX_AUDIT_LOG_FILE")
	if logPath == "" {
		cwd, _ := os.Getwd()
		logPath = filepath.Join(cwd, "logs", "audit.json")
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open audit log: %v", err)
		return
	}
	defer func() { _ = f.Close() }()

	b, _ := json.Marshal(entry)
	_, _ = f.Write(append(b, '\n'))
}
