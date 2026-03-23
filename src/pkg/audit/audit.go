package audit

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
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

var (
	auditCh      = make(chan Entry, bufferSize)
	auditDone    = make(chan struct{})
	shutdownOnce sync.Once
)

func init() {
	go run()
}

// Shutdown drains the audit buffer and waits for all pending entries to be
// written to disk. Call before process exit to avoid losing buffered entries.
func Shutdown() {
	shutdownOnce.Do(func() { close(auditCh) })
	<-auditDone
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

// run is the background goroutine that owns the file handle and serialises all writes.
func run() {
	defer close(auditDone)

	f := openLogFile()
	defer func() {
		if f != nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}()

	for entry := range auditCh {
		if f == nil {
			// Retry in case the directory was created after startup.
			f = openLogFile()
		}
		if f == nil {
			continue
		}
		b, _ := json.Marshal(entry)
		_, _ = f.Write(append(b, '\n'))
	}

	// Drain complete — final sync before the deferred Close.
	if f != nil {
		_ = f.Sync()
	}
}

func openLogFile() *os.File {
	logPath := os.Getenv("EMDEX_AUDIT_LOG_FILE")
	if logPath == "" {
		cwd, _ := os.Getwd()
		logPath = filepath.Join(cwd, "logs", "audit.json")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		log.Printf("[audit] failed to create log directory: %v", err)
		return nil
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[audit] failed to open log file: %v", err)
		return nil
	}
	return f
}
