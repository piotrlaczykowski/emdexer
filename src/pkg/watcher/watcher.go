// Package watcher implements real-time filesystem monitoring via fsnotify.
//
// This is the P4 implementation that was previously marked "Done" but was
// actually one-shot only.  The Watcher runs continuously: it adds inotify
// watches recursively for new directories and fires callbacks on file changes.
//
// Design constraints:
//   - Only local filesystems are supported (inotify is kernel-level).
//   - Remote VFS (SMB/SFTP/NFS) must use scheduled poll instead.
//   - Debounce: rapid writes (editors saving) coalesce into a single event.
//   - Errors are logged but never panic — the watch loop must be resilient.
package watcher

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// debounceDelay prevents duplicate indexing when editors write temp files
	// and then rename them.  500ms covers most editors (vim, VSCode, nano).
	debounceDelay = 500 * time.Millisecond
)

// FileEvent describes a change detected on the filesystem.
type FileEvent struct {
	Path string
	Op   fsnotify.Op
}

// OnFileChange is called for each debounced change.  If the callback returns
// an error it is logged but the watcher continues — one bad file must not
// stall the whole stream.
type OnFileChange func(event FileEvent) error

// OnFileDelete is called when a file is removed or renamed.
type OnFileDelete func(path string) error

// Watcher wraps fsnotify with recursive directory watching and debouncing.
type Watcher struct {
	root     string
	inner    *fsnotify.Watcher
	handler  OnFileChange
	onDelete OnFileDelete

	mu      sync.Mutex
	pending map[string]*time.Timer // debounce timers keyed by path

	stopCh    chan struct{}
	Heartbeat *Heartbeat
}

// New creates a Watcher rooted at root.  Call Start() to begin watching.
// The optional onDelete callback is invoked when files are removed or renamed.
func New(root string, handler OnFileChange, opts ...OnFileDelete) (*Watcher, error) {
	inner, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	var deleteFn OnFileDelete
	if len(opts) > 0 {
		deleteFn = opts[0]
	}
	w := &Watcher{
		root:      root,
		inner:     inner,
		handler:   handler,
		onDelete:  deleteFn,
		pending:   make(map[string]*time.Timer),
		stopCh:    make(chan struct{}),
		Heartbeat: NewHeartbeat(),
	}
	// Recursively add all existing directories.
	if err := w.addRecursive(root); err != nil {
		inner.Close()
		return nil, fmt.Errorf("initial watch setup: %w", err)
	}
	return w, nil
}

// addRecursive walks path and registers every directory with fsnotify.
func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("[watcher] WalkDir error at %s: %v (skipping)", path, err)
			return nil // non-fatal
		}
		if d.IsDir() {
			if err := w.inner.Add(path); err != nil {
				log.Printf("[watcher] Add(%s) error: %v (skipping)", path, err)
			}
		}
		return nil
	})
}

// Start begins the event loop in the calling goroutine.  It blocks until
// Stop() is called.  Run it in a goroutine: go w.Start().
func (w *Watcher) Start() {
	log.Printf("[watcher] Starting real-time watcher on %s", w.root)
	for {
		select {
		case <-w.stopCh:
			log.Printf("[watcher] Stopped")
			return

		case event, ok := <-w.inner.Events:
			if !ok {
				log.Printf("[watcher] Events channel closed — stopping")
				return
			}
			w.Heartbeat.Touch()
			w.handleEvent(event)

		case err, ok := <-w.inner.Errors:
			if !ok {
				return
			}
			log.Printf("[watcher] fsnotify error: %v", err)
		}
	}
}

// Stop shuts down the watcher cleanly.
func (w *Watcher) Stop() {
	close(w.stopCh)
	w.inner.Close()
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	// If a new directory appears, watch it immediately so files created inside
	// are caught without needing a restart.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			if err := w.addRecursive(event.Name); err != nil {
				log.Printf("[watcher] Failed to watch new dir %s: %v", event.Name, err)
			} else {
				log.Printf("[watcher] Now watching new directory: %s", event.Name)
			}
			return // directory create — no need to index the dir itself
		}
	}

	// Handle file deletions — remove corresponding points from Qdrant.
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		if w.onDelete != nil {
			log.Printf("[watcher] File removed: %s — deleting from index", event.Name)
			if err := w.onDelete(event.Name); err != nil {
				log.Printf("[watcher] Delete handler error for %s: %v", event.Name, err)
			}
		}
		return
	}

	// Debounce: reset the timer on every event for this path.
	w.mu.Lock()
	if t, ok := w.pending[event.Name]; ok {
		t.Stop()
	}
	w.pending[event.Name] = time.AfterFunc(debounceDelay, func() {
		w.mu.Lock()
		delete(w.pending, event.Name)
		w.mu.Unlock()

		fe := FileEvent{Path: event.Name, Op: event.Op}
		log.Printf("[watcher] Indexing changed file: %s (op=%s)", fe.Path, fe.Op)
		if err := w.handler(fe); err != nil {
			log.Printf("[watcher] Handler error for %s: %v", fe.Path, err)
		}
	})
	w.mu.Unlock()
}
