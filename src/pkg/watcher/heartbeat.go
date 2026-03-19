package watcher

import (
	"sync/atomic"
	"time"
)

// Heartbeat tracks the last time a worker goroutine was active.
// It is safe for concurrent use.
type Heartbeat struct {
	lastActive atomic.Int64 // unix timestamp in seconds
}

// NewHeartbeat creates a Heartbeat initialized to the current time.
func NewHeartbeat() *Heartbeat {
	h := &Heartbeat{}
	h.Touch()
	return h
}

// Touch updates the heartbeat to the current time.
func (h *Heartbeat) Touch() {
	h.lastActive.Store(time.Now().Unix())
}

// LastActive returns the time of the last heartbeat.
func (h *Heartbeat) LastActive() time.Time {
	return time.Unix(h.lastActive.Load(), 0)
}

// Alive returns true if the heartbeat was updated within the given threshold.
func (h *Heartbeat) Alive(threshold time.Duration) bool {
	return time.Since(h.LastActive()) < threshold
}
