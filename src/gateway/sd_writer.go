package main

import (
	"encoding/json"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/piotrlaczykowski/emdexer/registry"
)

// SDTarget is a Prometheus file_sd target entry.
type SDTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// SDWriter writes a Prometheus file_sd JSON file from the node registry.
type SDWriter struct {
	path         string
	hostOverride string // when non-empty, replace hostname in all targets
	mu           sync.Mutex
}

// NewSDWriter creates an SDWriter. path is the output file path.
// hostOverride, when non-empty, replaces the hostname portion of every target
// address (preserving the port). Use this when nodes register with Docker-internal
// hostnames that are unreachable from an external Prometheus.
// It is a no-op if path is empty.
func NewSDWriter(path, hostOverride string) *SDWriter {
	return &SDWriter{path: path, hostOverride: hostOverride}
}

// Write atomically writes the current node list as a Prometheus file_sd JSON.
// Each node becomes one target entry with labels: job, node_id, namespace, protocol.
// The metrics port is derived from the node's registered URL (same host, same port).
func (w *SDWriter) Write(nodes []registry.NodeInfo) {
	if w.path == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	var targets []SDTarget
	for _, n := range nodes {
		// Parse the node URL to extract host:port.
		// Node URL is always http://<host>:<port> (health/metrics port).
		u, err := url.Parse(n.URL)
		if err != nil || u.Host == "" {
			log.Printf("[sd-writer] skipping node %s: invalid URL %q", n.ID, n.URL)
			continue
		}

		ns := "unknown"
		if len(n.Namespaces) > 0 {
			ns = n.Namespaces[0]
		}

		host := u.Host
		if w.hostOverride != "" {
			// Replace hostname, keep port so Prometheus uses the reachable IP.
			_, port, err := net.SplitHostPort(u.Host)
			if err == nil && port != "" {
				host = net.JoinHostPort(w.hostOverride, port)
			} else {
				host = w.hostOverride
			}
		}

		targets = append(targets, SDTarget{
			Targets: []string{host}, // "host:port" — Prometheus appends metrics_path
			Labels: map[string]string{
				"job":       "emdexer-node",
				"node_id":   n.ID,
				"namespace": ns,
				"protocol":  n.Protocol,
			},
		})
	}

	if targets == nil {
		targets = []SDTarget{} // Write empty array, not null
	}

	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		log.Printf("[sd-writer] marshal error: %v", err)
		return
	}

	// Atomic write via temp file + rename.
	tmp := w.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(w.path), 0755); err != nil {
		log.Printf("[sd-writer] mkdir error: %v", err)
		return
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("[sd-writer] write error: %v", err)
		return
	}
	if err := os.Rename(tmp, w.path); err != nil {
		log.Printf("[sd-writer] rename error: %v", err)
		return
	}
	log.Printf("[sd-writer] wrote %d node targets to %s", len(targets), w.path)
}
