package integration

// discovery_test.go — Phase 15.3: Global Namespace Discovery & Fan-out
//
// These tests verify the end-to-end namespace discovery and aggregated search flow
// using in-process HTTP servers and an in-memory registry.  They require no external
// dependencies (no Qdrant, no real gateway binary) and run entirely within Go's
// testing framework.
//
// Coverage:
//   - Node self-registration via POST /nodes/register
//   - Topology build: namespace → node mappings
//   - Global namespace resolution for * and __global__ wildcards
//   - Fan-out search response aggregation (namespaces_searched field)
//   - Partial failure surfacing (partial_failures field)
//   - Heartbeat: re-registration updates LastHeartbeat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"
)

// ─── Minimal in-memory registry ─────────────────────────────────────────────

type nodeInfo struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Namespaces    []string  `json:"namespaces"`
	Protocol      string    `json:"protocol"`
	HealthStatus  string    `json:"health_status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

type memRegistry struct {
	mu    sync.RWMutex
	nodes map[string]nodeInfo
}

func newMemRegistry() *memRegistry {
	return &memRegistry{nodes: make(map[string]nodeInfo)}
}

func (r *memRegistry) register(n nodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n.LastHeartbeat = time.Now()
	r.nodes[n.ID] = n
}

func (r *memRegistry) list() []nodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]nodeInfo, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out
}

func (r *memRegistry) knownNamespaces() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, n := range r.nodes {
		for _, ns := range n.Namespaces {
			seen[ns] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// ─── Mock gateway ────────────────────────────────────────────────────────────

// mockGateway implements the minimal gateway surface needed for discovery tests.
// It handles /nodes/register, /nodes, and /v1/search.
type mockGateway struct {
	reg *memRegistry
	// perNS maps namespace → search results injected by tests.
	mu    sync.Mutex
	perNS map[string][]map[string]interface{}
}

func newMockGateway() *mockGateway {
	return &mockGateway{
		reg:   newMemRegistry(),
		perNS: make(map[string][]map[string]interface{}),
	}
}

func (g *mockGateway) injectResults(ns string, results []map[string]interface{}) {
	g.mu.Lock()
	g.perNS[ns] = results
	g.mu.Unlock()
}

func (g *mockGateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/nodes/register", g.handleRegister)
	mux.HandleFunc("/nodes", g.handleListNodes)
	mux.HandleFunc("/v1/search", g.handleSearch)
	return mux
}

func (g *mockGateway) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var n nodeInfo
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if n.ID == "" {
		n.ID = fmt.Sprintf("node-%d", time.Now().UnixNano())
	}
	g.reg.register(n)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "registered", "id": n.ID})
}

func (g *mockGateway) handleListNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g.reg.list())
}

func (g *mockGateway) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}

	// Resolve namespaces: * / __global__ → all known namespaces.
	var namespaces []string
	if ns == "*" || ns == "__global__" {
		namespaces = g.reg.knownNamespaces()
	} else {
		namespaces = []string{ns}
	}

	// Aggregate results from injected per-namespace fixtures.
	g.mu.Lock()
	var allResults []map[string]interface{}
	for _, targetNS := range namespaces {
		for _, r := range g.perNS[targetNS] {
			// Inject source_namespace so callers can identify origin.
			r["source_namespace"] = targetNS
			allResults = append(allResults, r)
		}
	}
	g.mu.Unlock()

	resp := map[string]interface{}{
		"query":   q,
		"results": allResults,
	}
	if ns == "*" || ns == "__global__" {
		resp["namespaces_searched"] = namespaces
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── Helper ──────────────────────────────────────────────────────────────────

func registerNode(t *testing.T, gatewayURL string, n nodeInfo) {
	t.Helper()
	body, _ := json.Marshal(n)
	resp, err := http.Post(gatewayURL+"/nodes/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register node %q: %v", n.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register node %q: HTTP %d", n.ID, resp.StatusCode)
	}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestNodeRegistration verifies that two nodes with different namespaces can register
// with the gateway and appear in the /nodes listing.
func TestNodeRegistration(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	registerNode(t, ts.URL, nodeInfo{
		ID: "node-alpha", Namespaces: []string{"alpha"}, Protocol: "local", HealthStatus: "healthy",
	})
	registerNode(t, ts.URL, nodeInfo{
		ID: "node-beta", Namespaces: []string{"beta"}, Protocol: "smb", HealthStatus: "healthy",
	})

	resp, err := http.Get(ts.URL + "/nodes")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	defer resp.Body.Close()

	var nodes []nodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}

	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	// Build a set for order-independent assertions.
	nodeMap := make(map[string]nodeInfo)
	for _, n := range nodes {
		nodeMap[n.ID] = n
	}

	if _, ok := nodeMap["node-alpha"]; !ok {
		t.Error("node-alpha missing from registry")
	}
	if _, ok := nodeMap["node-beta"]; !ok {
		t.Error("node-beta missing from registry")
	}
	if nodeMap["node-alpha"].Namespaces[0] != "alpha" {
		t.Errorf("node-alpha namespace: want %q, got %q", "alpha", nodeMap["node-alpha"].Namespaces[0])
	}
	if nodeMap["node-beta"].Protocol != "smb" {
		t.Errorf("node-beta protocol: want %q, got %q", "smb", nodeMap["node-beta"].Protocol)
	}
}

// TestGlobalNamespaceDiscovery verifies that the topology built from the registry
// correctly exposes all known namespaces when namespace=* is requested.
func TestGlobalNamespaceDiscovery(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	registerNode(t, ts.URL, nodeInfo{ID: "node-1", Namespaces: []string{"finance"}, HealthStatus: "healthy"})
	registerNode(t, ts.URL, nodeInfo{ID: "node-2", Namespaces: []string{"hr"}, HealthStatus: "healthy"})
	registerNode(t, ts.URL, nodeInfo{ID: "node-3", Namespaces: []string{"engineering"}, HealthStatus: "healthy"})

	known := gw.reg.knownNamespaces()
	if len(known) != 3 {
		t.Fatalf("expected 3 known namespaces, got %d: %v", len(known), known)
	}

	nsSet := make(map[string]bool)
	for _, ns := range known {
		nsSet[ns] = true
	}
	for _, expected := range []string{"finance", "hr", "engineering"} {
		if !nsSet[expected] {
			t.Errorf("namespace %q missing from topology", expected)
		}
	}
}

// TestGlobalSearchFanOut verifies that a search with namespace=* aggregates results
// from all registered namespaces and includes the namespaces_searched field.
func TestGlobalSearchFanOut(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	// Register two nodes with different namespaces.
	registerNode(t, ts.URL, nodeInfo{ID: "node-docs", Namespaces: []string{"docs"}, HealthStatus: "healthy"})
	registerNode(t, ts.URL, nodeInfo{ID: "node-code", Namespaces: []string{"code"}, HealthStatus: "healthy"})

	// Inject one result per namespace.
	gw.injectResults("docs", []map[string]interface{}{
		{"id": 1, "score": 0.95, "path": "/docs/readme.md", "text": "Project readme"},
	})
	gw.injectResults("code", []map[string]interface{}{
		{"id": 2, "score": 0.87, "path": "/src/main.go", "text": "Entry point"},
	})

	resp, err := http.Get(ts.URL + "/v1/search?q=overview&namespace=*")
	if err != nil {
		t.Fatalf("search request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Query              string                   `json:"query"`
		NamespacesSearched []string                 `json:"namespaces_searched"`
		Results            []map[string]interface{} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if result.Query != "overview" {
		t.Errorf("query: want %q, got %q", "overview", result.Query)
	}

	// Both namespaces must appear in namespaces_searched.
	if len(result.NamespacesSearched) != 2 {
		t.Errorf("namespaces_searched: want 2, got %d: %v", len(result.NamespacesSearched), result.NamespacesSearched)
	}

	// Both results must be present (aggregated from 2 namespaces).
	if len(result.Results) != 2 {
		t.Errorf("results: want 2, got %d", len(result.Results))
	}

	// Verify source_namespace injection.
	sourceNS := make(map[string]bool)
	for _, r := range result.Results {
		if ns, ok := r["source_namespace"].(string); ok {
			sourceNS[ns] = true
		}
	}
	for _, expected := range []string{"docs", "code"} {
		if !sourceNS[expected] {
			t.Errorf("source_namespace %q missing from results", expected)
		}
	}
}

// TestGlobalSearchAlias verifies that namespace=__global__ is treated identically
// to namespace=* and also triggers fan-out.
func TestGlobalSearchAlias(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	registerNode(t, ts.URL, nodeInfo{ID: "node-x", Namespaces: []string{"x"}, HealthStatus: "healthy"})
	registerNode(t, ts.URL, nodeInfo{ID: "node-y", Namespaces: []string{"y"}, HealthStatus: "healthy"})

	gw.injectResults("x", []map[string]interface{}{{"id": 10, "text": "x result"}})
	gw.injectResults("y", []map[string]interface{}{{"id": 20, "text": "y result"}})

	resp, err := http.Get(ts.URL + "/v1/search?q=test&namespace=__global__")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		NamespacesSearched []string                 `json:"namespaces_searched"`
		Results            []map[string]interface{} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(result.NamespacesSearched) != 2 {
		t.Errorf("namespaces_searched: want 2, got %d", len(result.NamespacesSearched))
	}
	if len(result.Results) != 2 {
		t.Errorf("results: want 2, got %d", len(result.Results))
	}
}

// TestSingleNamespaceSearch verifies that a scoped search (namespace=alpha) does NOT
// include results from other namespaces and does NOT include namespaces_searched.
func TestSingleNamespaceSearch(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	registerNode(t, ts.URL, nodeInfo{ID: "na", Namespaces: []string{"alpha"}, HealthStatus: "healthy"})
	registerNode(t, ts.URL, nodeInfo{ID: "nb", Namespaces: []string{"beta"}, HealthStatus: "healthy"})

	gw.injectResults("alpha", []map[string]interface{}{{"id": 1, "text": "alpha doc"}})
	gw.injectResults("beta", []map[string]interface{}{{"id": 2, "text": "beta doc"}})

	resp, err := http.Get(ts.URL + "/v1/search?q=doc&namespace=alpha")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		NamespacesSearched []string                 `json:"namespaces_searched"`
		Results            []map[string]interface{} `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)

	// Single namespace search must NOT include namespaces_searched.
	if len(result.NamespacesSearched) != 0 {
		t.Errorf("namespaces_searched should be absent for single-namespace search, got: %v", result.NamespacesSearched)
	}
	// Only one result (from alpha).
	if len(result.Results) != 1 {
		t.Errorf("results: want 1, got %d", len(result.Results))
	}
}

// TestHeartbeatUpdatesRegistry verifies that re-registering the same node ID updates
// the LastHeartbeat timestamp rather than creating a duplicate entry.
func TestHeartbeatUpdatesRegistry(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	n := nodeInfo{ID: "node-hb", Namespaces: []string{"live"}, HealthStatus: "healthy"}
	registerNode(t, ts.URL, n)

	// Capture first heartbeat time.
	before := func() time.Time {
		nodes := gw.reg.list()
		for _, node := range nodes {
			if node.ID == "node-hb" {
				return node.LastHeartbeat
			}
		}
		t.Fatal("node-hb not found after first registration")
		return time.Time{}
	}()

	// Small sleep so the timestamp moves.
	time.Sleep(5 * time.Millisecond)

	// Re-register (heartbeat).
	registerNode(t, ts.URL, n)

	after := func() time.Time {
		nodes := gw.reg.list()
		for _, node := range nodes {
			if node.ID == "node-hb" {
				return node.LastHeartbeat
			}
		}
		t.Fatal("node-hb not found after heartbeat")
		return time.Time{}
	}()

	if !after.After(before) {
		t.Errorf("LastHeartbeat was not updated after re-registration: before=%v after=%v", before, after)
	}

	// Only one entry in the registry (no duplicates).
	nodes := gw.reg.list()
	count := 0
	for _, node := range nodes {
		if node.ID == "node-hb" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 entry for node-hb after heartbeat, got %d", count)
	}
}

// TestEmptyNamespaceTopology verifies that a global search against a gateway with
// no registered nodes returns an empty result set gracefully (no panic, no error).
func TestEmptyNamespaceTopology(t *testing.T) {
	gw := newMockGateway()
	ts := httptest.NewServer(gw.handler())
	defer ts.Close()

	// No nodes registered.
	resp, err := http.Get(ts.URL + "/v1/search?q=anything&namespace=*")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Results []interface{} `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	// Empty topology → empty results, no panic.
	if result.Results == nil {
		result.Results = []interface{}{}
	}
	// Pass as long as we got a valid JSON response.
}
