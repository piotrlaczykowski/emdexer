package registry

import (
	"context"
	"time"
)

type NodeInfo struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Collections   []string  `json:"collections"`
	Namespaces    []string  `json:"namespaces"`
	Protocol      string    `json:"protocol"`
	HealthStatus  string    `json:"health_status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	RegisteredAt  time.Time `json:"registered_at"`
}

func DeepCopyNodeInfo(n NodeInfo) NodeInfo {
	cols := make([]string, len(n.Collections))
	copy(cols, n.Collections)
	ns := make([]string, len(n.Namespaces))
	copy(ns, n.Namespaces)
	return NodeInfo{
		ID:            n.ID,
		URL:           n.URL,
		Collections:   cols,
		Namespaces:    ns,
		Protocol:      n.Protocol,
		HealthStatus:  n.HealthStatus,
		LastHeartbeat: n.LastHeartbeat,
		RegisteredAt:  n.RegisteredAt,
	}
}

// NodeRegistry is the interface that all registry backends must implement.
type NodeRegistry interface {
	Register(ctx context.Context, n NodeInfo) error
	Deregister(ctx context.Context, id string) error
	List(ctx context.Context) ([]NodeInfo, error)
}
