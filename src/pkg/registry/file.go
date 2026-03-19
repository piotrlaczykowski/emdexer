package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type FileNodeRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]NodeInfo
	dataFile string
}

func NewFileNodeRegistry(dataFile string) *FileNodeRegistry {
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
		if !os.IsNotExist(err) {
			log.Printf("[registry] Failed to read %s: %v", r.dataFile, err)
		}
		return
	}
	var nodes []NodeInfo
	if err := json.Unmarshal(data, &nodes); err != nil {
		log.Printf("[registry] Failed to parse %s: %v", r.dataFile, err)
		return
	}
	for _, n := range nodes {
		r.nodes[n.ID] = DeepCopyNodeInfo(n)
	}
}

func (r *FileNodeRegistry) persist() error {
	nodes := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		nodes = append(nodes, DeepCopyNodeInfo(n))
	}
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal failed: %w", err)
	}
	tmp := r.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp failed: %w", err)
	}
	if err := os.Rename(tmp, r.dataFile); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}
	return nil
}

func (r *FileNodeRegistry) Register(ctx context.Context, n NodeInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n.RegisteredAt = time.Now()
	r.nodes[n.ID] = DeepCopyNodeInfo(n)
	if err := r.persist(); err != nil {
		log.Printf("[registry] persist error: %v", err)
		return err
	}
	return nil
}

func (r *FileNodeRegistry) Deregister(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return nil
	}
	delete(r.nodes, id)
	if err := r.persist(); err != nil {
		log.Printf("[registry] persist error: %v", err)
		return err
	}
	return nil
}

func (r *FileNodeRegistry) List(ctx context.Context) ([]NodeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, DeepCopyNodeInfo(n))
	}
	return out, nil
}
