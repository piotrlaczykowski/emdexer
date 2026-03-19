package search

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/qdrant/go-client/qdrant"
	"golang.org/x/sync/errgroup"
)

// FanOutSearch runs parallel Qdrant searches across multiple namespaces and merges via RRF.
func FanOutSearch(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, namespaces []string, limit uint64, timeout time.Duration) ([]Result, error) {
	if timeout == 0 {
		timeout = 500 * time.Millisecond
	}
	g, gctx := errgroup.WithContext(ctx)
	gctx, cancel := context.WithTimeout(gctx, timeout)
	defer cancel()

	var mu sync.Mutex
	perNS := make(map[string][]Result)

	for _, ns := range namespaces {
		ns := ns
		g.Go(func() error {
			results, err := SearchQdrant(gctx, pc, collection, vector, limit, ns)
			if err != nil {
				log.Printf("[search] namespace %q fan-out error: %v", ns, err)
				return nil
			}
			mu.Lock()
			perNS[ns] = results
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	return MergeRRF(perNS, int(limit)), nil
}

// ResolveNamespaces returns the list of namespaces to search.
// For "*" or "__global__", it intersects known namespaces with the user's allowed list.
func ResolveNamespaces(requested string, allowed []string, known []string) []string {
	if requested != "*" && requested != "__global__" {
		return []string{requested}
	}
	isWildcard := false
	allowedSet := make(map[string]bool)
	for _, ns := range allowed {
		if ns == "*" {
			isWildcard = true
			break
		}
		allowedSet[ns] = true
	}
	if len(known) == 0 {
		return []string{""}
	}
	if isWildcard {
		return known
	}
	var result []string
	for _, ns := range known {
		if allowedSet[ns] {
			result = append(result, ns)
		}
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}
