# Binary File Organization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split three oversized `main.go` files into focused, single-responsibility files without changing any behavior.

**Architecture:** Pure file-split within `package main` — no new packages, no interface changes, no logic changes. Each binary's directory gets additional `.go` files; all existing imports stay. The only structural additions are: `otelShutdown func()` field on gateway `Server`, `App` struct in node, and refactored `doSearch`/`doChat` sub-functions in the CLI.

**Tech Stack:** Go (multi-module: `src/gateway/go.mod`, `src/node/go.mod`, `src/cmd/emdex/go.mod`)

---

## Pre-Flight

- [ ] Confirm you are on branch `docs/p38-binary-file-organization`
- [ ] Confirm `go build ./...` passes in all three modules before touching anything

```bash
git branch --show-current   # expect: docs/p38-binary-file-organization
cd src/gateway && go build ./... && cd ../node && go build ./... && cd ../cmd/emdex && go build ./... && cd ../../..
```

---

## GATEWAY (`src/gateway/`)

Work order: extract leaves first (metrics → types → handlers → topology), then server.go, then slim main.go last.

---

### Task 1: Extract `metrics.go`

**Files:**
- Create: `src/gateway/metrics.go`
- Modify: `src/gateway/main.go` (remove lines 43–56)

- [ ] **Step 1: Create `src/gateway/metrics.go`**

```go
package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var searchEmptyResults = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "emdexer_gateway_search_empty_results_total",
	Help: "Number of search requests that returned zero results",
}, []string{"namespace", "mode"})

var topologyNamespacesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_namespaces_known",
	Help: "Number of namespaces currently known from the node registry",
})

var topologyNodesKnown = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "emdexer_gateway_topology_nodes_known",
	Help: "Number of nodes currently known from the node registry",
})
```

- [ ] **Step 2: Delete lines 43–56 from `src/gateway/main.go`** (the three `var` blocks; leave everything else intact)

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add src/gateway/metrics.go src/gateway/main.go
git commit -m "refactor(gateway): extract metrics vars to metrics.go"
```

---

### Task 2: Extract `types.go` (gateway)

**Files:**
- Create: `src/gateway/types.go`
- Modify: `src/gateway/main.go` (remove `GraphConfig` struct, lines 97–101)

- [ ] **Step 1: Create `src/gateway/types.go`**

```go
package main

// GraphConfig holds feature-flag settings for the knowledge-graph expansion.
type GraphConfig struct {
	Enabled bool
	Depth   int // BFS depth: 1–3
}
```

- [ ] **Step 2: Remove the `GraphConfig` struct block from `src/gateway/main.go`** (lines 97–101 in the original, starting with the comment `// GraphConfig holds...`)

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/types.go src/gateway/main.go
git commit -m "refactor(gateway): extract GraphConfig to types.go"
```

---

### Task 3: Extract `handler_registry.go`

**Files:**
- Create: `src/gateway/handler_registry.go`
- Modify: `src/gateway/main.go` (remove the three functions)

Functions: `handleRegisterNode` (line 147), `handleListNodes` (line 169), `handleDeregisterNode` (line 178).

- [ ] **Step 1: Create `src/gateway/handler_registry.go`**

Copy the three functions verbatim from `main.go`. The import block is whatever the functions actually use — let `goimports` or the compiler tell you what's needed. Minimum:

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/piotrlaczykowski/emdexer/registry"
)

// handleRegisterNode, handleListNodes, handleDeregisterNode — paste verbatim from main.go
```

- [ ] **Step 2: Remove those three functions from `src/gateway/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/handler_registry.go src/gateway/main.go
git commit -m "refactor(gateway): extract registry handlers to handler_registry.go"
```

---

### Task 4: Extract `handler_health.go`

**Files:**
- Create: `src/gateway/handler_health.go`
- Modify: `src/gateway/main.go` (remove six functions)

Functions at lines 694–784: `handleHealth`, `handleLiveness`, `handleReadiness`, `handleStartup`, `handleQdrantHealth`, `handleWhoami`.

- [ ] **Step 1: Create `src/gateway/handler_health.go`**

```go
package main

import (
	"encoding/json"
	"net/http"

	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/version"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// handleHealth, handleLiveness, handleReadiness, handleStartup,
// handleQdrantHealth, handleWhoami — paste verbatim from main.go lines 694–784
```

- [ ] **Step 2: Remove the six functions from `src/gateway/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/handler_health.go src/gateway/main.go
git commit -m "refactor(gateway): extract health handlers to handler_health.go"
```

---

### Task 5: Extract `handler_search.go`

**Files:**
- Create: `src/gateway/handler_search.go`
- Modify: `src/gateway/main.go` (remove three functions)

Functions: `uniquePaths` (203), `graphExpandResults` (220), `handleSearch` (261). ~230 lines total.

- [ ] **Step 1: Create `src/gateway/handler_search.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/graph"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/search"
	"go.opentelemetry.io/otel"
)

// uniquePaths, graphExpandResults, handleSearch — paste verbatim from main.go lines 203–432
```

- [ ] **Step 2: Remove the three functions from `src/gateway/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/handler_search.go src/gateway/main.go
git commit -m "refactor(gateway): extract search handlers to handler_search.go"
```

---

### Task 6: Extract `handler_chat.go`

**Files:**
- Create: `src/gateway/handler_chat.go`
- Modify: `src/gateway/main.go` (remove function)

Function: `handleChatCompletions` (line 433) — ~260 lines.

- [ ] **Step 1: Create `src/gateway/handler_chat.go`**

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/piotrlaczykowski/emdexer/llm"
	"github.com/piotrlaczykowski/emdexer/openai"
	"github.com/piotrlaczykowski/emdexer/rag"
	"go.opentelemetry.io/otel"
)

// handleChatCompletions — paste verbatim from main.go lines 433–693
```

- [ ] **Step 2: Remove `handleChatCompletions` from `src/gateway/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/handler_chat.go src/gateway/main.go
git commit -m "refactor(gateway): extract chat handler to handler_chat.go"
```

---

### Task 7: Extract `topology.go` (gateway)

**Files:**
- Create: `src/gateway/topology.go`
- Modify: `src/gateway/main.go` (remove `refreshTopology`, `knownNamespaces`, AND the inline ticker goroutine at lines 993–1004)

`startTopologyLoop()` is a new method that wraps the goroutine that currently lives inline in `main()`.

- [ ] **Step 1: Create `src/gateway/topology.go`**

```go
package main

import (
	"context"
	"log"
	"time"
)

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

// startTopologyLoop launches the background 30-second topology refresh ticker.
// Returns immediately; goroutine runs until s.stopTopology is closed.
func (s *Server) startTopologyLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.refreshTopology()
			case <-s.stopTopology:
				return
			}
		}
	}()
}
```

- [ ] **Step 2: Remove from `src/gateway/main.go`:**
  1. `refreshTopology` function (lines 116–134)
  2. `knownNamespaces` function (lines 137–145)
  3. The inline ticker goroutine block in `main()` (lines 993–1004) — the `go func() { ticker := time.NewTicker... }()` block. This goroutine is now replaced by `srv.startTopologyLoop()` which will be called from `Run()` in Task 8.

- [ ] **Step 3: Build**

```bash
cd src/gateway && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/gateway/topology.go src/gateway/main.go
git commit -m "refactor(gateway): extract topology methods to topology.go"
```

---

### Task 8: Create `server.go` and slim `main.go` (gateway)

**Files:**
- Create: `src/gateway/server.go`
- Rewrite: `src/gateway/main.go`

`server.go` gets: `Server` struct (with new `otelShutdown func()` field), `writeJSON`, `newServer()` (absorbs all wiring from current `main()`), and `Run()` (routes + HTTP listen + graceful shutdown). `main.go` becomes a 3-line entrypoint.

- [ ] **Step 1: Create `src/gateway/server.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/piotrlaczykowski/emdexer/audit"
	"github.com/piotrlaczykowski/emdexer/auth"
	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/graph"
	"github.com/piotrlaczykowski/emdexer/middleware"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/rag"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/rerank"
	"github.com/piotrlaczykowski/emdexer/telemetry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/qdrant/go-client/qdrant"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Server holds all gateway state.
type Server struct {
	reg          registry.NodeRegistry
	qdrantConn   *grpc.ClientConn
	pointsClient qdrant.PointsClient
	healthClient grpc_health_v1.HealthClient
	embedder     embed.EmbedProvider
	collection   string
	apiKey       string
	authCfg      *auth.Config
	port         string
	startTime    time.Time

	topoMu     sync.RWMutex
	nsTopology map[string][]string

	globalSearchTimeout time.Duration
	bm25Enabled         bool
	agenticCfg          rag.AgenticConfig

	graphCfg       GraphConfig
	knowledgeGraph *graph.Graph

	reranker        rerank.Reranker
	rerankTopK      int
	rerankThreshold float64

	stopTopology  chan struct{}
	events        *eventBus
	streamEnabled bool

	// otelShutdown is the teardown function returned by telemetry.InitTracer.
	// telemetry.InitTracer returns func(), set here and called from Run() on shutdown.
	otelShutdown func()
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// newServer reads all environment variables, constructs dependencies, and returns
// a fully initialized Server. Copy the full body from main() lines 787–991 verbatim,
// replacing the final `srv := &Server{...}` literal with the one below (add otelShutdown field).
func newServer() *Server {
	cwd, _ := os.Getwd()
	config.LoadEnv(filepath.Join(cwd, ".env"))

	// OpenTelemetry — no-op when EMDEX_OTEL_ENDPOINT is unset.
	otelServiceName := os.Getenv("EMDEX_OTEL_SERVICE_NAME")
	if otelServiceName == "" {
		otelServiceName = "emdex-gateway"
	}
	otelShutdown, err := telemetry.InitTracer(otelServiceName, os.Getenv("EMDEX_OTEL_ENDPOINT"))
	if err != nil {
		log.Printf("[gateway] WARN: OpenTelemetry init failed: %v — tracing disabled", err)
		otelShutdown = func() {}
	} else if ep := os.Getenv("EMDEX_OTEL_ENDPOINT"); ep != "" {
		log.Printf("[gateway] OpenTelemetry tracing enabled: endpoint=%s service=%s", ep, otelServiceName)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	// [paste remaining env-var parsing and dependency construction from main() lines 803–965 verbatim]
	// apiKey, qdrantHost, port, collection, authKey, apiKeys, oidcVerifier, groupACL,
	// qdrantDialOpt, conn, reg, embedder, globalSearchTimeout, bm25Enabled,
	// agenticCfg, graphCfg, streamEnabled, rerankEnabled/URL/Token/TopK/Threshold, reranker

	srv := &Server{
		// [paste the &Server{...} literal from main() lines 966–987 verbatim]
		// Add one new field:
		otelShutdown: otelShutdown,
	}
	srv.stopTopology = make(chan struct{})
	srv.events = newEventBus()
	return srv
}

// Run performs the initial topology refresh, starts background services,
// registers HTTP routes, listens for connections, and blocks until SIGTERM/SIGINT.
func (s *Server) Run() {
	s.refreshTopology()
	s.startTopologyLoop()

	// Metrics server on :9090
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:        ":9090",
		Handler:     metricsMux,
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[gateway] metrics server error: %v", err)
		}
	}()

	// Route registration — copy verbatim from main() lines 1021–1035
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz/liveness", s.handleLiveness)
	mux.HandleFunc("/healthz/readiness", s.handleReadiness)
	mux.HandleFunc("/healthz/startup", s.handleStartup)
	mux.HandleFunc("/healthz/qdrant", s.handleQdrantHealth)
	mux.HandleFunc("/nodes/register", middleware.Instrument("/nodes/register", s.authCfg.Middleware(s.handleRegisterNode)))
	mux.HandleFunc("/nodes/deregister/", middleware.Instrument("/nodes/deregister", s.authCfg.Middleware(s.handleDeregisterNode)))
	mux.HandleFunc("/nodes", middleware.Instrument("/nodes", s.authCfg.Middleware(s.handleListNodes)))
	mux.HandleFunc("/v1/search", middleware.Instrument("/v1/search", s.authCfg.Middleware(s.handleSearch)))
	mux.HandleFunc("/v1/chat/completions", middleware.Instrument("/v1/chat/completions", s.authCfg.Middleware(s.handleChatCompletions)))
	mux.HandleFunc("/v1/whoami", middleware.Instrument("/v1/whoami", s.authCfg.Middleware(s.handleWhoami)))
	mux.HandleFunc("/v1/events/indexing", middleware.Instrument("/v1/events/indexing", s.authCfg.Middleware(s.handleIndexingEvents)))
	mux.HandleFunc("/v1/eval", middleware.Instrument("/v1/eval", s.authCfg.Middleware(s.handleEval)))
	mux.HandleFunc("/v1/nodes/", middleware.Instrument("/v1/nodes/", s.authCfg.Middleware(s.handleNodeIndexed)))

	addr := ":" + s.port
	log.Printf("Gateway starting on %s", addr)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gateway server error: %v", err)
		}
	}()

	// Block until signal, then graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Printf("[gateway] Shutting down...")

	close(s.stopTopology)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("[gateway] HTTP shutdown error: %v", err)
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		log.Printf("[gateway] metrics server shutdown error: %v", err)
	}
	audit.Shutdown()
	s.otelShutdown()
	log.Printf("[gateway] Shutdown complete")
}
```

- [ ] **Step 2: Rewrite `src/gateway/main.go`**

```go
package main

func main() {
	srv := newServer()
	srv.Run()
}
```

- [ ] **Step 3: Remove from `main.go`**: the old `Server` struct definition, `writeJSON`, and the full old `main()` body. Also ensure the `defer conn.Close()` from old `main()` is removed — connection lifetime is now managed by `Run()` returning (the gateway process exits after `Run()` returns).

- [ ] **Step 4: Build**

```bash
cd src/gateway && go build ./...
```

The compiler will report any unused imports or missing references. Fix them. All legitimate references to functions/types now exist in other files in the same package.

- [ ] **Step 5: Run tests**

```bash
cd src/gateway && go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add src/gateway/server.go src/gateway/main.go
git commit -m "refactor(gateway): extract server wiring to server.go, slim main.go to 4 lines"
```

---

## NODE (`src/node/`)

Work order: types → helpers → vfs → indexing → app → slim main.go

---

### Task 9: Extract `types.go` (node)

**Files:**
- Create: `src/node/types.go`
- Modify: `src/node/main.go` (remove Config struct lines 43–101 and constants lines 37–39)

- [ ] **Step 1: Create `src/node/types.go`**

Copy the two constants and the `Config` struct verbatim from `node/main.go` lines 36–101:

```go
package main

const (
	DefaultEmbedDims = 3072
	CollectionName   = "emdexer_v1"
)

// Config holds all runtime configuration for the node, populated from environment variables.
type Config struct {
	// paste the full struct body from main.go lines 44–101 verbatim
}
```

- [ ] **Step 2: Remove the constants block (lines 36–39) and the full `Config` struct (lines 43–101) from `src/node/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/node && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/node/types.go src/node/main.go
git commit -m "refactor(node): extract Config struct and constants to types.go"
```

---

### Task 10: Extract `helpers.go` (node)

**Files:**
- Create: `src/node/helpers.go`
- Modify: `src/node/main.go` (remove `contextModel`, `parseIntEnv`, `parseFloatEnv` — lines 517–547)

- [ ] **Step 1: Create `src/node/helpers.go`**

```go
package main

import (
	"os"
	"strconv"
)

// contextModel returns the model to use for contextual retrieval context generation.
// Priority: EMDEX_CONTEXT_MODEL → EMDEX_LLM_MODEL → gemini-3-flash-preview.
func contextModel() string {
	if m := os.Getenv("EMDEX_CONTEXT_MODEL"); m != "" {
		return m
	}
	if m := os.Getenv("EMDEX_LLM_MODEL"); m != "" {
		return m
	}
	return "gemini-3-flash-preview"
}

// parseIntEnv parses an environment variable as an integer, returning def if unset or invalid.
func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// parseFloatEnv parses an environment variable as a float64, returning def if unset or invalid.
func parseFloatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}
```

- [ ] **Step 2: Remove `contextModel`, `parseIntEnv`, `parseFloatEnv` from `src/node/main.go`** (lines 517–547)

- [ ] **Step 3: Build**

```bash
cd src/node && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/node/helpers.go src/node/main.go
git commit -m "refactor(node): extract utility helpers to helpers.go"
```

---

### Task 11: Extract `vfs.go` (node)

**Files:**
- Create: `src/node/vfs.go`
- Modify: `src/node/main.go` (remove `initVFS` — lines 549–577)

- [ ] **Step 1: Create `src/node/vfs.go`**

```go
package main

import (
	"log"

	"github.com/piotrlaczykowski/emdexer/vfs"
)

// initVFS sets globalFS based on globalCfg.NodeType.
// Paste initVFS verbatim from main.go lines 549–577.
```

- [ ] **Step 2: Remove `initVFS` from `src/node/main.go`**

- [ ] **Step 3: Build**

```bash
cd src/node && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/node/vfs.go src/node/main.go
git commit -m "refactor(node): extract VFS initialization to vfs.go"
```

---

### Task 12: Extract `indexing.go` (node)

**Files:**
- Create: `src/node/indexing.go`
- Modify: `src/node/main.go` (remove queue setup, watcher/poller setup, and startup walk goroutine)

Wrap the queue setup (lines ~222–230), watcher/poller block (lines ~387–441), and startup walk goroutine (lines 444–497) into a single function `startIndexing(root, cwd string, pipelineCfg indexer.PipelineConfig)`.

- [ ] **Step 1: Create `src/node/indexing.go`**

```go
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/plugin"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/search"
	"github.com/piotrlaczykowski/emdexer/watcher"
	"github.com/qdrant/go-client/qdrant"
)

// startIndexing sets up the indexing queue, file watcher/poller, plugin loader,
// and launches the startup walk goroutine. It returns immediately.
func startIndexing(root, cwd string, pipelineCfg indexer.PipelineConfig) {
	// 1. Queue setup — paste lines 222–230 verbatim
	queuePath := os.Getenv("EMDEX_QUEUE_DB")
	if queuePath == "" {
		queuePath = filepath.Join(cwd, "cache", "queue.db")
	}
	_ = os.MkdirAll(filepath.Dir(queuePath), 0700)
	var qErr error
	globalQueue, qErr = queue.NewPersistentQueue(queuePath)
	if qErr == nil {
		go queue.StartWorker(globalQueue, globalPointsClient, globalCfg.CollectionName, globalCtx)
	}

	// 2. Plugin loader — paste lines 331–343 verbatim
	if os.Getenv("EMDEX_PLUGIN_ENABLED") != "false" {
		pluginDir := os.Getenv("EMDEX_PLUGIN_DIR")
		if pluginDir == "" {
			pluginDir = filepath.Join(cwd, "plugins")
		}
		if plugins, loadErr := plugin.LoadPlugins(pluginDir); loadErr != nil {
			log.Printf("[plugin] Load error from %s: %v", pluginDir, loadErr)
		} else if len(plugins) > 0 {
			pipelineCfg.Plugins = plugins
			log.Printf("[plugin] %d plugin(s) active for indexing", len(plugins))
		}
	}

	// 3. deletePoints closure — paste lines 383–385 verbatim
	deletePoints := func(path string) error {
		return search.DeletePointsByPath(globalCtx, globalPointsClient, globalCfg.CollectionName, path)
	}

	cacheDir := os.Getenv("EMDEX_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = filepath.Join(cwd, "cache")
	}

	// 4. Watcher (local) or poller (remote VFS) — paste lines 387–441 verbatim
	if globalCfg.NodeType == "local" {
		w, _ := watcher.New(root, func(ev watcher.FileEvent) error {
			if walkComplete.Load() == 0 {
				if _, alreadySeen := walkSeen.Load(ev.Path); alreadySeen {
					log.Printf("[node] Watcher skipping %s (already indexed by startup walk)", ev.Path)
					return nil
				}
			}
			content, _ := os.ReadFile(ev.Path)
			points := indexer.IndexDataToPoints(ev.Path, content, pipelineCfg)
			if len(points) > 0 {
				_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
					CollectionName: globalCfg.CollectionName,
					Points:         points,
				})
				if err != nil && globalQueue != nil {
					_ = globalQueue.Enqueue(points)
				}
				return err
			}
			return nil
		}, deletePoints)
		if w != nil {
			globalWorkerHeartbeat = w.Heartbeat
			go w.Start()
		}
	} else {
		cache, _ := watcher.NewMetadataCache(filepath.Join(cacheDir, "emdex_cache.db"))
		if cache != nil {
			p := watcher.NewPoller(
				globalFS,
				root,
				cache,
				60*time.Second,
				func(path string, content []byte) error {
					points := indexer.IndexDataToPoints(path, content, pipelineCfg)
					if len(points) > 0 {
						_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
							CollectionName: globalCfg.CollectionName,
							Points:         points,
						})
						if err != nil && globalQueue != nil {
							_ = globalQueue.Enqueue(points)
						}
						return err
					}
					return nil
				},
				deletePoints,
			)
			globalWorkerHeartbeat = p.Heartbeat
			go p.Start()
		}
	}

	// 5. Startup walk goroutine — paste lines 444–497 verbatim
	go func() {
		log.Printf("[node] Startup walk starting: root=%s vfs=%s", root, globalCfg.NodeType)
		idx := indexer.NewIndexer(globalFS)
		var batch []*qdrant.PointStruct
		flush := func() {
			if len(batch) == 0 {
				return
			}
			_, err := globalPointsClient.Upsert(globalCtx, &qdrant.UpsertPoints{
				CollectionName: globalCfg.CollectionName,
				Points:         batch,
			})
			if err != nil {
				if globalQueue != nil {
					if qerr := globalQueue.Enqueue(batch); qerr != nil {
						log.Printf("[node] Walk flush: Qdrant upsert and queue both failed: upsert=%v queue=%v", err, qerr)
					} else {
						log.Printf("[node] Walk flush: Qdrant upsert failed, queued: err=%v", err)
					}
				} else {
					log.Printf("[node] Walk flush: Qdrant upsert failed, no queue: err=%v", err)
				}
			}
			batch = nil
		}

		stats, walkErr := idx.Walk(root, func(path string, isDir bool, content []byte) error {
			walkSeen.Store(path, struct{}{})
			points := indexer.IndexDataToPoints(path, content, pipelineCfg)
			for _, p := range points {
				batch = append(batch, p)
				if len(batch) >= 100 {
					flush()
				}
			}
			return nil
		})
		flush()

		if walkErr != nil {
			log.Printf("[node] Startup walk failed: root=%s err=%v", root, walkErr)
		}
		walkComplete.Store(1)
		walkSeen.Range(func(key, _ any) bool {
			walkSeen.Delete(key)
			return true
		})
		log.Printf("[node] Startup walk complete: root=%s indexed=%d skipped=%d dirs_skipped=%d",
			root, stats.FilesIndexed, stats.FilesSkipped, stats.DirsSkipped)
		go reportIndexingComplete(globalCfg.GatewayURL, globalCfg.NodeID,
			globalCfg.Namespace, globalCfg.GatewayAuthKey, stats)
	}()
}
```

- [ ] **Step 2: Remove the corresponding blocks from `src/node/main.go`**: queue setup, plugin loader, deletePoints closure, watcher/poller block, and startup walk goroutine

- [ ] **Step 3: Build**

```bash
cd src/node && go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add src/node/indexing.go src/node/main.go
git commit -m "refactor(node): extract indexing pipeline to indexing.go"
```

---

### Task 13: Create `app.go` and slim `main.go` (node)

**Files:**
- Create: `src/node/app.go`
- Rewrite: `src/node/main.go`

`app.go` holds all package-level globals, the `App` struct, `newApp()` (absorbs remaining `main()` wiring), and `Run()` (health server goroutine + signal wait).

- [ ] **Step 1: Create `src/node/app.go`**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/piotrlaczykowski/emdexer/config"
	"github.com/piotrlaczykowski/emdexer/embed"
	"github.com/piotrlaczykowski/emdexer/extract"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/health"
	"github.com/piotrlaczykowski/emdexer/indexer"
	"github.com/piotrlaczykowski/emdexer/nodereg"
	"github.com/piotrlaczykowski/emdexer/qdrantcreds"
	"github.com/piotrlaczykowski/emdexer/queue"
	"github.com/piotrlaczykowski/emdexer/registry"
	"github.com/piotrlaczykowski/emdexer/safenet"
	"github.com/piotrlaczykowski/emdexer/vfs"
	"github.com/piotrlaczykowski/emdexer/watcher"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

// Package-level globals — accessed by indexing.go, vfs.go, events.go, gemini.go.
// Types match src/node/main.go lines 87–101 exactly.
var EmbeddingDims uint64 = DefaultEmbedDims
var globalPointsClient qdrant.PointsClient
var globalQueue *queue.PersistentQueue
var globalCB *extractor.CircuitBreaker
var globalCfg Config
var globalCtx context.Context
var globalFS vfs.FileSystem
var globalEmbedder embed.EmbedProvider
var globalWorkerHeartbeat *watcher.Heartbeat
var walkSeen sync.Map
var walkComplete atomic.Int32

// App holds top-level infrastructure for the node's lifetime.
type App struct {
	conn *grpc.ClientConn
	root string
	cwd  string
}

// newApp reads environment, constructs all infrastructure, starts indexing,
// and returns a ready App. The returned App's Run() blocks until shutdown.
//
// Copy the body of main() (lines 103–514 of the original) verbatim here,
// with the following two changes:
//   1. Remove `defer conn.Close()` and `defer globalFS.Close()` — they move to Run().
//   2. Replace the final `health.StartServer(...)` call with `return &App{conn: conn, root: root, cwd: cwd}`.
//   3. Replace the queue/watcher/plugin/walk block with `startIndexing(root, cwd, pipelineCfg)`.
func newApp() *App {
	// [paste main() body here with the changes described above]
	// The function ends with:
	//   return &App{conn: conn, root: root, cwd: cwd}
	panic("not implemented — replace with actual body")
}

// Run starts the health server as a non-blocking goroutine, then blocks until
// SIGTERM or SIGINT. On signal it performs cleanup and returns.
//
// NOTE: In the original source, health.StartServer was the final blocking call.
// The refactor makes it a goroutine and adds an explicit signal-wait, which is
// a deliberate behavior change documented in the spec.
func (a *App) Run() {
	defer func() { _ = a.conn.Close() }()
	defer func() { _ = globalFS.Close() }()

	go health.StartServer(health.ServerConfig{
		QdrantConn:      a.conn,
		WorkerHeartbeat: globalWorkerHeartbeat,
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Printf("[node] Shutting down")
}
```

- [ ] **Step 2: Rewrite `src/node/main.go`**

```go
package main

import (
	"os"

	"github.com/piotrlaczykowski/emdexer/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		version.Print()
		return
	}
	app := newApp()
	app.Run()
}
```

- [ ] **Step 3: Build**

```bash
cd src/node && go build ./...
```

Fix any compiler errors. Common issues: unused imports in `app.go` (remove them), missing imports (add them). The `strconv`, `strings`, `fmt` etc. imports in `app.go` depend on what the `newApp()` body actually uses — adjust to match.

- [ ] **Step 4: Run tests**

```bash
cd src/node && go test ./...
```

- [ ] **Step 5: Commit**

```bash
git add src/node/app.go src/node/main.go
git commit -m "refactor(node): extract app wiring to app.go, slim main.go to 10 lines"
```

---

## CLI (`src/cmd/emdex/`)

Work order: types+usage → command files → search+chat split → slim main.go

---

### Task 14: Extract `types.go` and `usage.go` (CLI)

**Files:**
- Create: `src/cmd/emdex/types.go`
- Create: `src/cmd/emdex/usage.go`
- Modify: `src/cmd/emdex/main.go` (remove `workerResult` and `printUsage`)

- [ ] **Step 1: Create `src/cmd/emdex/types.go`**

`workerResult` is at lines 204–207 of `main.go`:

```go
package main

// workerResult holds the outcome of a concurrent status check.
type workerResult struct {
	emoji  string
	detail string
}
```

- [ ] **Step 2: Create `src/cmd/emdex/usage.go`**

```go
package main

import (
	"fmt"

	"github.com/piotrlaczykowski/emdexer/ui"
	"github.com/piotrlaczykowski/emdexer/version"
)

// printUsage — paste verbatim from main.go lines 55–74
```

- [ ] **Step 3: Remove `workerResult` (lines 204–207) and `printUsage` (lines 55–74) from `src/cmd/emdex/main.go`**

- [ ] **Step 4: Build**

```bash
cd src/cmd/emdex && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add src/cmd/emdex/types.go src/cmd/emdex/usage.go src/cmd/emdex/main.go
git commit -m "refactor(cli): extract types and usage to dedicated files"
```

---

### Task 15: Extract individual command files (CLI)

**Files created:** `cmd_init.go`, `cmd_start.go`, `cmd_status.go`, `cmd_nodes.go`, `cmd_whoami.go`

For each step: create file with correct `package main` header, paste the function(s) verbatim, remove from `main.go`, build.

- [ ] **Step 1: Create `src/cmd/emdex/cmd_init.go`** — `cmdInit` (lines 76–138)

Remove `cmdInit` from `main.go`. Build: `cd src/cmd/emdex && go build ./...`

- [ ] **Step 2: Create `src/cmd/emdex/cmd_start.go`** — `cmdStart` (lines 140–158)

Remove from `main.go`. Build.

- [ ] **Step 3: Create `src/cmd/emdex/cmd_status.go`** — `cmdStatus`, `printStatusLine`, `checkHealth`, `checkWorker`, `checkRegistry` (lines 160–301)

Remove from `main.go`. Build.

- [ ] **Step 4: Create `src/cmd/emdex/cmd_nodes.go`** — `cmdNodes` (lines 303–375)

Remove from `main.go`. Build.

- [ ] **Step 5: Create `src/cmd/emdex/cmd_whoami.go`** — `cmdWhoami` (lines 640–705)

Remove from `main.go`. Build.

- [ ] **Step 6: Commit all five files**

```bash
git add src/cmd/emdex/cmd_init.go src/cmd/emdex/cmd_start.go \
        src/cmd/emdex/cmd_status.go src/cmd/emdex/cmd_nodes.go \
        src/cmd/emdex/cmd_whoami.go src/cmd/emdex/main.go
git commit -m "refactor(cli): extract cmdInit/Start/Status/Nodes/Whoami to dedicated files"
```

---

### Task 16: Refactor and extract `cmd_search.go` + `cmd_search_display.go`

**Files:**
- Create: `src/cmd/emdex/cmd_search.go`
- Create: `src/cmd/emdex/cmd_search_display.go`
- Modify: `src/cmd/emdex/main.go` (remove old `cmdSearch`)

A Go function cannot be split across files. `cmdSearch` is refactored: the display block becomes `printSearchResults`, and `cmdSearch` calls it.

The result type used internally in `cmdSearch` (the anonymous struct with `query`, `namespaces_searched`, `partial_failures`, `results` fields) becomes a named type in `cmd_search.go` to allow it to cross the file boundary.

- [ ] **Step 1: Create `src/cmd/emdex/cmd_search_display.go`**

```go
package main

import (
	"fmt"
	"strings"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// searchResult is the decoded response from /v1/search.
type searchResult struct {
	Query              string                   `json:"query"`
	NamespacesSearched []string                 `json:"namespaces_searched"`
	PartialFailures    []string                 `json:"partial_failures"`
	Results            []map[string]interface{} `json:"results"`
}

// printSearchResults prints the formatted search output to stdout.
// Paste the display block from the bottom of cmdSearch verbatim here,
// replacing the inline anonymous struct with searchResult.
func printSearchResults(result searchResult) {
	fmt.Printf("\n  %s  %s\n", "🔍", ui.Bold(fmt.Sprintf("Search: %q", result.Query)))
	// ... paste remainder of display block from cmdSearch verbatim
}
```

- [ ] **Step 2: Create `src/cmd/emdex/cmd_search.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdSearch queries /v1/search and displays results.
func cmdSearch() {
	// Paste the flag-parsing and HTTP request portion of the current cmdSearch verbatim.
	// Replace the display block at the bottom with:
	//   printSearchResults(result)
	// Replace the anonymous struct type with searchResult (defined in cmd_search_display.go).
}
```

- [ ] **Step 3: Remove old `cmdSearch` from `src/cmd/emdex/main.go`**

- [ ] **Step 4: Build**

```bash
cd src/cmd/emdex && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add src/cmd/emdex/cmd_search.go src/cmd/emdex/cmd_search_display.go src/cmd/emdex/main.go
git commit -m "refactor(cli): split cmdSearch into request and display files"
```

---

### Task 17: Refactor and extract `cmd_chat.go` + `cmd_chat_display.go`

**Files:**
- Create: `src/cmd/emdex/cmd_chat.go`
- Create: `src/cmd/emdex/cmd_chat_display.go`
- Modify: `src/cmd/emdex/main.go` (remove old `cmdChat`)

- [ ] **Step 1: Create `src/cmd/emdex/cmd_chat_display.go`**

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// printStreamedChatResponse reads an SSE stream and prints tokens as they arrive.
// Paste the `if stream {` branch from cmdChat (the bufio.Scanner loop) verbatim.
func printStreamedChatResponse(body io.Reader) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			fmt.Print(chunk.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}

// printFullChatResponse decodes a non-streaming response and prints the content.
// Paste the `else {` branch from cmdChat verbatim.
func printFullChatResponse(body io.Reader) {
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", "❌", "Invalid response from gateway")
		os.Exit(1)
	}
	if len(result.Choices) > 0 {
		fmt.Printf("  %s\n\n", result.Choices[0].Message.Content)
	}
}
```

- [ ] **Step 2: Create `src/cmd/emdex/cmd_chat.go`**

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/piotrlaczykowski/emdexer/ui"
)

// cmdChat sends a chat completion request and streams or prints the response.
func cmdChat() {
	// Paste flag-parsing and HTTP request portion of cmdChat verbatim.
	// Replace the stream/non-stream display block with:
	//   fmt.Printf("\n  %s  %s\n\n", "💬", ui.Bold("Response"))
	//   if stream {
	//       printStreamedChatResponse(resp.Body)
	//   } else {
	//       printFullChatResponse(resp.Body)
	//   }
}
```

- [ ] **Step 3: Remove old `cmdChat` from `src/cmd/emdex/main.go`**

- [ ] **Step 4: Build**

```bash
cd src/cmd/emdex && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add src/cmd/emdex/cmd_chat.go src/cmd/emdex/cmd_chat_display.go src/cmd/emdex/main.go
git commit -m "refactor(cli): split cmdChat into request and display files"
```

---

### Task 18: Verify and finalize `main.go` (CLI)

At this point `main.go` should contain only `func main()`. Verify it matches the exact dispatch logic from the original (including the `--version` flag path):

- [ ] **Step 1: Verify `src/cmd/emdex/main.go` matches this structure exactly:**

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/piotrlaczykowski/emdexer/version"
)

func main() {
	// Named-command dispatch (args not starting with '-')
	if len(os.Args) > 1 && os.Args[1][0] != '-' {
		switch os.Args[1] {
		case "init":
			cmdInit()
		case "start":
			cmdStart()
		case "status":
			cmdStatus()
		case "nodes":
			cmdNodes()
		case "search":
			cmdSearch()
		case "whoami":
			cmdWhoami()
		case "chat":
			cmdChat()
		default:
			fmt.Fprintf(os.Stderr, "\n  ❌ Unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
		return
	}

	// Flag-based dispatch (--version)
	showVersion := flag.Bool("version", false, "show version")
	flag.Parse()
	if *showVersion {
		fmt.Printf("emdex version %s\n", version.Version)
		os.Exit(0)
	}

	printUsage()
}
```

**Important:** Copy the exact strings from the original `main.go` lines 19–53 — do not alter error message wording.

- [ ] **Step 2: Build**

```bash
cd src/cmd/emdex && go build ./...
```

- [ ] **Step 3: Run tests**

```bash
cd src/cmd/emdex && go test ./...
```

- [ ] **Step 4: Commit if any changes were needed**

```bash
git add src/cmd/emdex/main.go
git commit -m "refactor(cli): finalize main.go as dispatch-only entrypoint"
```

---

## Final Verification

- [ ] **Verify all three `main.go` files are ≤ 20 lines**

```bash
wc -l src/gateway/main.go src/node/main.go src/cmd/emdex/main.go
```

Expected: gateway ~3, node ~10, cli ~30 (including the flag dispatch block).

- [ ] **Verify no new file exceeds 300 lines**

```bash
wc -l src/gateway/*.go src/node/*.go src/cmd/emdex/*.go | sort -rn | head -20
```

- [ ] **Full build check**

```bash
cd src/gateway && go build ./... && cd ../node && go build ./... && cd ../cmd/emdex && go build ./... && cd ../../..
echo "All binaries build successfully"
```

- [ ] **Full test run**

```bash
cd src/gateway && go test ./... && cd ../node && go test ./... && cd ../cmd/emdex && go test ./... && cd ../../..
```

- [ ] **Smoke test: signal handling (node)**

The spec notes that `health.StartServer` was changed from a blocking call to a goroutine with an explicit signal-wait. Verify graceful shutdown still works:

```bash
cd src/node && go build -o /tmp/emdex-node . && /tmp/emdex-node &
NODE_PID=$!
sleep 1
kill -TERM $NODE_PID
wait $NODE_PID
echo "Exit code: $?"
```

Expected: node logs `[node] Shutting down` and exits cleanly (exit code 0 or signal-terminated, not a panic).

- [ ] **Count commits on this branch**

```bash
git log --oneline docs/p38-binary-file-organization ^develop | wc -l
```

Expected: 15–20 focused refactor commits.
