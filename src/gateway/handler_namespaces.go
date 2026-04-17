package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/qdrant/go-client/qdrant"
	"go.opentelemetry.io/otel"
	"google.golang.org/protobuf/proto"
)

// NamespaceStat aggregates per-namespace visibility for monitoring and UIs.
type NamespaceStat struct {
	Namespace     string     `json:"namespace"`
	NodeIDs       []string   `json:"node_ids"`
	VectorCount   uint64     `json:"vector_count"`
	LastIndexedAt *time.Time `json:"last_indexed_at,omitempty"`
}

// handleNamespaceStats aggregates per-namespace stats from the registry and
// Qdrant. Partial failures (Qdrant count, PG last_heartbeat) degrade gracefully
// — the endpoint always returns 200 with best-effort data and never null.
func (s *Server) handleNamespaceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.namespaces.stats")
	defer span.End()

	nodes, err := s.reg.List(ctx)
	if err != nil {
		log.Printf("[namespaces] registry list error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// namespace -> deduplicated node IDs
	nsNodes := make(map[string]map[string]struct{})
	for _, n := range nodes {
		for _, ns := range n.Namespaces {
			if ns == "" {
				continue
			}
			if _, ok := nsNodes[ns]; !ok {
				nsNodes[ns] = make(map[string]struct{})
			}
			nsNodes[ns][n.ID] = struct{}{}
		}
	}

	dbReg, _ := s.reg.(*registry.DBNodeRegistry)

	stats := []NamespaceStat{}
	for ns, idSet := range nsNodes {
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		stat := NamespaceStat{Namespace: ns, NodeIDs: ids}

		// Qdrant vector count — approximate; partial failures degrade to 0.
		count, cerr := s.namespaceVectorCount(ctx, ns)
		if cerr != nil {
			log.Printf("[namespaces] qdrant count error ns=%q: %v", ns, cerr)
		} else {
			stat.VectorCount = count
		}
		namespaceVectorCount.WithLabelValues(ns).Set(float64(stat.VectorCount))

		// Last indexed — only when PG registry is in use.
		if dbReg != nil {
			if t, derr := queryLastIndexed(ctx, dbReg, ns); derr != nil {
				log.Printf("[namespaces] last_indexed query error ns=%q: %v", ns, derr)
			} else if !t.IsZero() {
				stat.LastIndexedAt = &t
			}
		}

		stats = append(stats, stat)
	}

	sort.Slice(stats, func(i, j int) bool { return stats[i].Namespace < stats[j].Namespace })
	s.writeJSON(w, http.StatusOK, stats)
}

// namespaceVectorCount asks Qdrant for an approximate count of points whose
// payload field "namespace" matches ns.
func (s *Server) namespaceVectorCount(ctx context.Context, ns string) (uint64, error) {
	filter := &qdrant.Filter{
		Must: []*qdrant.Condition{
			{
				ConditionOneOf: &qdrant.Condition_Field{
					Field: &qdrant.FieldCondition{
						Key: "namespace",
						Match: &qdrant.Match{
							MatchValue: &qdrant.Match_Keyword{Keyword: ns},
						},
					},
				},
			},
		},
	}
	resp, err := s.pointsClient.Count(ctx, &qdrant.CountPoints{
		CollectionName: s.collection,
		Filter:         filter,
		Exact:          proto.Bool(false),
	})
	if err != nil {
		return 0, err
	}
	return resp.GetResult().GetCount(), nil
}

// queryLastIndexed returns MAX(last_heartbeat) for any node whose namespaces
// JSONB array contains ns.
func queryLastIndexed(ctx context.Context, r *registry.DBNodeRegistry, ns string) (time.Time, error) {
	var t time.Time
	err := r.DB().QueryRowContext(ctx,
		`SELECT MAX(last_heartbeat) FROM registered_nodes WHERE namespaces @> $1::jsonb`,
		fmt.Sprintf(`[%q]`, ns),
	).Scan(&t)
	return t, err
}
