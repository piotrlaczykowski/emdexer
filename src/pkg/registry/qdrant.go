package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// ClusterStatus holds the health state of the Qdrant Raft cluster as returned
// by the /cluster HTTP endpoint.
type ClusterStatus struct {
	Status      string `json:"status"`        // "enabled" | "disabled"
	PeerID      uint64 `json:"peer_id"`
	NodeCount   int    `json:"peers_count"`
	LeaderID    uint64 `json:"leader_peer_id,omitempty"`
	CommitIndex uint64 `json:"commit_index,omitempty"`
	RaftReady   bool   `json:"raft_ready"`
}

// CheckRaftCluster queries the Qdrant HTTP REST endpoint /cluster to verify that
// the Raft cluster is healthy. qdrantHTTPAddr must be in host:port form
// (e.g., "qdrant:6333").
//
// Returns an error if the cluster is unreachable, the status is not "enabled",
// or the Raft consensus layer is not ready.
func CheckRaftCluster(ctx context.Context, qdrantHTTPAddr string) (*ClusterStatus, error) {
	url := fmt.Sprintf("http://%s/cluster", qdrantHTTPAddr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build cluster request: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cluster request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cluster endpoint returned HTTP %d", resp.StatusCode)
	}

	// Qdrant wraps the cluster payload in {"result": {...}, "status": "ok"}.
	var envelope struct {
		Result struct {
			Status string `json:"status"`
			PeerID uint64 `json:"peer_id"`
			Peers  map[string]struct {
				URI string `json:"uri"`
			} `json:"peers"`
			RaftInfo struct {
				CommitIndex  uint64 `json:"commit_index"`
				LeaderPeerID uint64 `json:"leader_peer_id"`
				Term         uint64 `json:"term"`
				IsVoter      bool   `json:"is_voter"`
				Role         string `json:"role"`
			} `json:"raft_info"`
		} `json:"result"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode cluster response: %w", err)
	}
	if envelope.Status != "ok" {
		return nil, fmt.Errorf("qdrant returned status %q", envelope.Status)
	}

	r := envelope.Result
	cs := &ClusterStatus{
		Status:      r.Status,
		PeerID:      r.PeerID,
		NodeCount:   len(r.Peers),
		LeaderID:    r.RaftInfo.LeaderPeerID,
		CommitIndex: r.RaftInfo.CommitIndex,
		RaftReady:   r.Status == "enabled" && r.RaftInfo.LeaderPeerID != 0,
	}

	if r.Status == "disabled" {
		// Single-node deployments have cluster disabled — that's healthy.
		cs.RaftReady = true
	}

	return cs, nil
}

// EnsureTextIndexes creates full-text payload indexes on the "text" and "namespace"
// fields of the given collection. These indexes are required for BM25 keyword search
// in the hybrid search pipeline (Phase 21).
//
// Errors are logged but not fatal — if indexing fails the gateway falls back to
// pure vector search transparently. Index creation is idempotent: re-running on an
// already-indexed collection is a no-op in Qdrant.
func EnsureTextIndexes(ctx context.Context, pc qdrant.PointsClient, collection string) {
	fields := []struct {
		name      string
		tokenizer qdrant.TokenizerType
	}{
		{"text", qdrant.TokenizerType_Word},
		{"namespace", qdrant.TokenizerType_Whitespace},
	}

	fieldType := qdrant.FieldType_FieldTypeText
	for _, f := range fields {
		_, err := pc.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: collection,
			FieldName:      f.name,
			FieldType:      &fieldType,
			FieldIndexParams: &qdrant.PayloadIndexParams{
				IndexParams: &qdrant.PayloadIndexParams_TextIndexParams{
					TextIndexParams: &qdrant.TextIndexParams{
						Tokenizer: f.tokenizer,
					},
				},
			},
		})
		if err != nil {
			log.Printf("[registry] full-text index on %q/%q: %v (BM25 search may degrade to vector-only)", collection, f.name, err)
		} else {
			log.Printf("[registry] ensured full-text index on %q/%q", collection, f.name)
		}
	}
}
