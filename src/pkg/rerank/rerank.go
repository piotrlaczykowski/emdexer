// Package rerank provides a late-interaction reranking layer that refines
// the top-K results from the Hybrid Search (RRF) pipeline before they are
// passed to the LLM. The Reranker interface is intentionally narrow so that
// callers need not import the search package — it works on plain strings and
// returns per-document scores that the gateway maps back to search.Result.
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// ── Prometheus metrics ────────────────────────────────────────────────────────

var rerankDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "emdexer_gateway_search_rerank_duration_ms",
	Help:    "Latency of the reranking sidecar call in milliseconds",
	Buckets: []float64{5, 10, 25, 50, 100, 250, 500, 1000},
}, []string{"namespace"})

var rerankErrors = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_search_rerank_errors_total",
	Help: "Total number of reranking errors (sidecar unreachable or returned non-2xx)",
}, []string{"namespace"})

// ── Interface ─────────────────────────────────────────────────────────────────

// Reranker scores a list of candidate texts against a query. The returned slice
// has one score per input text, in the same order. A higher score means more
// relevant. Callers are responsible for sorting and truncating.
type Reranker interface {
	Rerank(ctx context.Context, query string, texts []string) ([]float32, error)
}

// ── Ranked result ─────────────────────────────────────────────────────────────

// Scored pairs an original slice index with the rerank score it received.
type Scored struct {
	Index int
	Score float32
}

// Rank calls r.Rerank and returns the input indices sorted by descending rerank
// score, capped to topK. If reranking fails the original order is preserved so
// search continues to work without the sidecar.
func Rank(ctx context.Context, r Reranker, query string, texts []string, topK int, namespace string) ([]Scored, error) {
	if r == nil {
		return nil, nil
	}
	if len(texts) == 0 {
		return nil, nil
	}

	start := time.Now()
	scores, err := r.Rerank(ctx, query, texts)
	rerankDuration.WithLabelValues(namespace).Observe(float64(time.Since(start).Milliseconds()))

	if err != nil {
		rerankErrors.WithLabelValues(namespace).Inc()
		return nil, err
	}

	ranked := make([]Scored, len(texts))
	for i, s := range scores {
		ranked[i] = Scored{Index: i, Score: s}
	}
	sort.Slice(ranked, func(a, b int) bool {
		return ranked[a].Score > ranked[b].Score
	})
	if topK > 0 && topK < len(ranked) {
		ranked = ranked[:topK]
	}
	return ranked, nil
}

// ── NoOpReranker ──────────────────────────────────────────────────────────────

// NoOpReranker returns 0.0 for every document. Used when EMDEX_RERANK_ENABLED=false.
type NoOpReranker struct{}

func (NoOpReranker) Rerank(_ context.Context, _ string, texts []string) ([]float32, error) {
	return make([]float32, len(texts)), nil
}

// ── SidecarReranker ───────────────────────────────────────────────────────────

// SidecarReranker calls the BGE-Reranker sidecar over HTTP. The sidecar must
// expose POST /rerank accepting {"query":…, "texts":[…]} and responding with
// {"results":[{"index":N, "score":F},…]}.
type SidecarReranker struct {
	url    string
	token  string
	client *http.Client
}

// NewSidecarReranker creates a SidecarReranker pointed at addr (e.g. "http://reranker:8005").
// token is the shared secret sent as X-Reranker-Token; pass an empty string to disable auth.
func NewSidecarReranker(addr, token string) *SidecarReranker {
	return &SidecarReranker{
		url:   addr + "/rerank",
		token: token,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type rerankRequest struct {
	Query string   `json:"query"`
	Texts []string `json:"texts"`
}

type rerankResponseItem struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
}

type rerankResponse struct {
	Results []rerankResponseItem `json:"results"`
}

func (s *SidecarReranker) Rerank(ctx context.Context, query string, texts []string) ([]float32, error) {
	ctx, span := otel.Tracer("emdexer").Start(ctx, "emdex.search.rerank")
	span.SetAttributes(
		attribute.String("rerank.host", rerankHost(s.url)),
		attribute.Int("rerank.candidates", len(texts)),
	)
	defer span.End()

	body, err := json.Marshal(rerankRequest{Query: query, Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("rerank marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rerank new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("X-Reranker-Token", s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank sidecar unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank sidecar returned HTTP %d", resp.StatusCode)
	}

	const maxRespBytes = 1 << 20 // 1 MiB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("rerank read body: %w", err)
	}

	var rr rerankResponse
	if err := json.Unmarshal(respBody, &rr); err != nil {
		return nil, fmt.Errorf("rerank decode: %w", err)
	}

	scores := make([]float32, len(texts))
	for _, item := range rr.Results {
		if item.Index >= 0 && item.Index < len(scores) {
			scores[item.Index] = item.Score
		}
	}

	runes := []rune(query)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	logQuery := string(runes)
	log.Printf("[rerank] scored %d/%d candidates for query %q", len(rr.Results), len(texts), logQuery)
	return scores, nil
}

// rerankHost extracts the host portion of addr for safe use in OTel span
// attributes, avoiding leaking full URLs including paths or credentials.
func rerankHost(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return addr
	}
	return u.Host
}
