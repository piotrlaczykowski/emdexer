package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/qdrant/go-client/qdrant"
)

var graphMigrationTriggeredTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "emdexer_node_graph_migration_triggered_total",
	Help: "Number of times the automatic graph-relation migration was triggered on node startup",
})

// graphMigrationMode controls how the node reacts to a pre-Phase-24 collection.
type graphMigrationMode string

const (
	migrationAuto  graphMigrationMode = "auto"  // detect and migrate (default)
	migrationSkip  graphMigrationMode = "skip"  // never auto-migrate
	migrationForce graphMigrationMode = "force" // always re-index on startup
)

func parseGraphMigrationMode(s string) graphMigrationMode {
	switch s {
	case "skip":
		return migrationSkip
	case "force":
		return migrationForce
	default:
		return migrationAuto
	}
}

// checkRelationsMigration samples up to 50 chunk-0 points from the namespace to
// determine whether the collection needs a full re-index to populate the `relations`
// payload field introduced in Phase 24 (Graph-RAG).
//
// Decision rule (mode=auto): if fewer than 20% of sampled points carry a non-empty
// `relations` field AND the namespace is non-empty, a re-index is scheduled.
//
// The function is fully non-blocking — any Qdrant error silently skips migration.
// It enforces a 2-second deadline on the Qdrant scroll so startup is never delayed.
//
// Returns true when re-indexing was scheduled (caller should treat it as a signal
// that the next full walk will populate relations on all pre-Phase-24 points).
func checkRelationsMigration(
	ctx context.Context,
	pc qdrant.PointsClient,
	collection, namespace string,
	cacheDir string,
	nodeType string,
	mode graphMigrationMode,
) bool {
	if mode == migrationSkip {
		log.Printf("[graph] migration mode=skip — skipping relations check")
		return false
	}

	// Hard deadline: migration check must not block startup for more than 2 s.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	const sampleSize = 50

	// Filter: namespace (if set) AND chunk == 0 (only chunk-0 carries relations).
	var mustConds []*qdrant.Condition
	if namespace != "" {
		mustConds = append(mustConds, &qdrant.Condition{
			ConditionOneOf: &qdrant.Condition_Field{
				Field: &qdrant.FieldCondition{
					Key: "namespace",
					Match: &qdrant.Match{
						MatchValue: &qdrant.Match_Keyword{Keyword: namespace},
					},
				},
			},
		})
	}
	mustConds = append(mustConds, &qdrant.Condition{
		ConditionOneOf: &qdrant.Condition_Field{
			Field: &qdrant.FieldCondition{
				Key: "chunk",
				Match: &qdrant.Match{
					MatchValue: &qdrant.Match_Integer{Integer: 0},
				},
			},
		},
	})

	lim := uint32(sampleSize)
	resp, err := pc.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: collection,
		Filter:         &qdrant.Filter{Must: mustConds},
		Limit:          &lim,
		WithPayload: &qdrant.WithPayloadSelector{
			SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true},
		},
	})
	if err != nil {
		log.Printf("[graph] migration check skipped (scroll error): %v", err)
		return false
	}

	pts := resp.GetResult()
	total := len(pts)
	if total == 0 {
		// Empty namespace — nothing to migrate.
		return false
	}

	var withRelations int
	for _, pt := range pts {
		if v, ok := pt.Payload["relations"]; ok {
			if sv, ok := v.Kind.(*qdrant.Value_StringValue); ok && sv.StringValue != "" {
				withRelations++
			}
		}
	}

	ratio := float64(withRelations) / float64(total)
	migrationNeeded := mode == migrationForce || ratio < 0.2

	if !migrationNeeded {
		log.Printf("[graph] migration check: %d/%d sampled points have relations (%.0f%%) — no migration needed",
			withRelations, total, ratio*100)
		return false
	}

	missing := total - withRelations
	if mode == migrationForce {
		log.Printf("[graph] migration mode=force — triggering full re-index for graph relation extraction")
	} else {
		log.Printf("[graph] relations field missing on %d/%d sampled points — scheduling full re-index for graph relation extraction",
			missing, total)
	}

	// For poller-based (non-local) nodes: delete the metadata cache so the initial
	// p.poll() call in Poller.Start() treats every file as new and re-indexes it.
	if nodeType != "local" && cacheDir != "" {
		cachePath := cacheDir + "/emdex_cache.db"
		if rmErr := os.Remove(cachePath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("[graph] migration: failed to delete metadata cache %s: %v", cachePath, rmErr)
		} else {
			log.Printf("[graph] re-index triggered — deleted %s, all files will be re-processed to extract relations", cachePath)
		}
	} else if nodeType == "local" {
		// Local nodes: startup walk processes all files unconditionally — no cache to clear.
		log.Printf("[graph] re-index triggered — startup walk will re-process all files to extract relations")
	}

	graphMigrationTriggeredTotal.Inc()
	return true
}
