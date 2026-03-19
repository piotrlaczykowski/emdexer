package search

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

// FanOutSearch runs parallel Qdrant searches across multiple namespaces and merges via RRF.
// It returns partial results even when some namespaces fail (partial failures preferred over 504).
// failedNS lists the namespaces that returned errors so callers can surface them to the client.
func FanOutSearch(ctx context.Context, pc qdrant.PointsClient, collection string, vector []float32, namespaces []string, limit uint64, timeout time.Duration) (results []Result, failedNS []string, err error) {
	if timeout == 0 {
		timeout = 500 * time.Millisecond
	}

	// Shared deadline for all per-namespace goroutines.
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var mu sync.Mutex
	perNS := make(map[string][]Result)

	var wg sync.WaitGroup
	for _, ns := range namespaces {
		ns := ns
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each goroutine uses the shared timeout context. A slow or hung node
			// is cancelled when the overall deadline fires, unblocking the WaitGroup.
			nsResults, nsErr := SearchQdrant(tctx, pc, collection, vector, limit, ns)
			mu.Lock()
			defer mu.Unlock()
			if nsErr != nil {
				log.Printf("[search] namespace %q fan-out error: %v", ns, nsErr)
				failedNS = append(failedNS, ns)
				return
			}
			perNS[ns] = nsResults
		}()
	}
	wg.Wait()

	return MergeRRF(perNS, int(limit)), failedNS, nil
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
