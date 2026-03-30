package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/registry"
)

func makeNodes(urls ...string) []registry.NodeInfo {
	nodes := make([]registry.NodeInfo, len(urls))
	for i, u := range urls {
		nodes[i] = registry.NodeInfo{
			ID:         "node-" + string(rune('a'+i)),
			URL:        u,
			Namespaces: []string{"prod"},
			Protocol:   "nfs",
		}
	}
	return nodes
}

func TestSDWriter_Write_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	w := NewSDWriter(path, "")
	nodes := makeNodes("http://host-a:8081", "http://host-b:8081")
	w.Write(nodes)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}

	var targets []SDTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	for i, tgt := range targets {
		if len(tgt.Targets) != 1 {
			t.Errorf("target[%d]: expected 1 address, got %d", i, len(tgt.Targets))
		}
		if tgt.Labels["job"] != "emdexer-node" {
			t.Errorf("target[%d]: expected job=emdexer-node, got %q", i, tgt.Labels["job"])
		}
		if tgt.Labels["namespace"] != "prod" {
			t.Errorf("target[%d]: expected namespace=prod, got %q", i, tgt.Labels["namespace"])
		}
	}
}

func TestSDWriter_Write_EmptyNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	w := NewSDWriter(path, "")
	w.Write([]registry.NodeInfo{})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}

	var targets []SDTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if targets == nil || len(targets) != 0 {
		t.Errorf("expected empty array, got %v", targets)
	}
}

func TestSDWriter_Write_SkipsInvalidURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	nodes := []registry.NodeInfo{
		{ID: "valid", URL: "http://host-a:8081", Namespaces: []string{"ns"}, Protocol: "nfs"},
		{ID: "empty-url", URL: "", Namespaces: []string{"ns"}, Protocol: "nfs"},
		{ID: "no-host", URL: "not-a-url", Namespaces: []string{"ns"}, Protocol: "nfs"},
	}

	w := NewSDWriter(path, "")
	w.Write(nodes)

	data, _ := os.ReadFile(path)
	var targets []SDTarget
	_ = json.Unmarshal(data, &targets)

	// Only the valid node should appear.
	if len(targets) != 1 {
		t.Errorf("expected 1 valid target, got %d", len(targets))
	}
	if len(targets) > 0 && targets[0].Targets[0] != "host-a:8081" {
		t.Errorf("expected host-a:8081, got %q", targets[0].Targets[0])
	}
}

func TestSDWriter_Write_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")
	w := NewSDWriter(path, "")
	nodes := makeNodes("http://host-a:8081")

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Write(nodes)
		}()
	}
	wg.Wait()

	// Give goroutines a moment to flush.
	time.Sleep(10 * time.Millisecond)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created after concurrent writes: %v", err)
	}
	var targets []SDTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Errorf("file contains invalid JSON after concurrent writes: %v", err)
	}
}

func TestSDWriter_NoOp_WhenPathEmpty(t *testing.T) {
	w := NewSDWriter("", "")
	// Must not panic and must not create any file.
	w.Write(makeNodes("http://host-a:8081"))
	// No assertion needed — the test passing without panic is the success condition.
}

func TestSDWriter_HostOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	w := NewSDWriter(path, "10.0.0.1")
	w.Write(makeNodes("http://abc123:8081"))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}

	var targets []SDTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Targets[0] != "10.0.0.1:8081" {
		t.Errorf("expected 10.0.0.1:8081, got %q", targets[0].Targets[0])
	}
}

func TestSDWriter_NoOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	w := NewSDWriter(path, "")
	w.Write(makeNodes("http://192.168.0.156:8082"))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}

	var targets []SDTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Targets[0] != "192.168.0.156:8082" {
		t.Errorf("expected 192.168.0.156:8082, got %q", targets[0].Targets[0])
	}
}
