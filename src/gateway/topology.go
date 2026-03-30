package main

import (
	"context"
	"log"
	"time"
)

// ============================================================
// Namespace Topology
// ============================================================

// refreshTopology rebuilds the in-memory namespace->nodeIDs map from the registry.
func (s *Server) refreshTopology() {
	nodes, err := s.reg.List(context.Background())
	if err != nil {
		log.Printf("[topology] refresh failed: %v", err)
		return
	}
	topo := make(map[string][]string)
	for _, n := range nodes {
		for _, ns := range n.Namespaces {
			topo[ns] = append(topo[ns], n.ID)
		}
	}
	s.topoMu.Lock()
	s.nsTopology = topo
	s.topoMu.Unlock()
	topologyNamespacesKnown.Set(float64(len(topo)))
	topologyNodesKnown.Set(float64(len(nodes)))
	log.Printf("[topology] Refreshed: %d namespaces across %d nodes", len(topo), len(nodes))
	s.sdWriter.Write(nodes)
}

// knownNamespaces returns all namespace strings from the topology map.
func (s *Server) knownNamespaces() []string {
	s.topoMu.RLock()
	defer s.topoMu.RUnlock()
	out := make([]string, 0, len(s.nsTopology))
	for ns := range s.nsTopology {
		out = append(out, ns)
	}
	return out
}

func (s *Server) startTopologyLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.refreshTopology()
			case <-s.topologyRefreshCh:
				// Drain any burst of signals, then wait 2s to coalesce.
				time.Sleep(2 * time.Second)
				for {
					select {
					case <-s.topologyRefreshCh:
					default:
						goto debounceDone
					}
				}
			debounceDone:
				s.refreshTopology()
			case <-s.stopTopology:
				return
			}
		}
	}()
}
