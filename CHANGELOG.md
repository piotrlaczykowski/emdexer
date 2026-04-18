# Changelog

## [1.3.0] — 2026-04-18

### Highlights

- Ollama-native streaming LLM backend — self-host any model without touching Gemini (P40)
- Response caching layer with in-process LRU and Redis backends — cut redundant embedding round-trips (P41)
- `GET /v1/namespaces/stats` — per-namespace vector counts and last-indexed timestamps (P42)
- `emdex eval` CLI command — run embedding evaluation suites from the terminal (P43)
- Outbound webhook on namespace index completion — SSRF-safe POST notification to any URL (P44)

### New Features

#### P40 — Ollama-native Streaming (`llm`, `gateway`, `docker`)

`CallOllama` and `CallOllamaStream` added to `src/pkg/llm` using stdlib `net/http` only (no new deps).
When `EMDEX_OLLAMA_URL` is set, the gateway selects Ollama as the LLM backend for both streaming and
non-streaming paths; Gemini remains the default.

New env vars: `EMDEX_OLLAMA_URL`, `EMDEX_OLLAMA_MODEL` (default `gemma4:26b`).

#### P41 — Response Caching Layer (`cache`, `gateway`)

Cache interface with two backends:

- **Memory** — hashicorp `golang-lru` expirable LRU with configurable TTL and atomic generation counters for namespace-scoped invalidation
- **Redis** — drop-in alternative for multi-replica deployments

Cache keys are deterministic SHA-256 hashes of `namespace + generation + model + normalized query` so a generation bump makes all prior entries unreachable cheaply.

#### P42 — Namespace Stats Endpoint (`gateway`)

`GET /v1/namespaces/stats` returns a JSON array of per-namespace visibility aggregated from the node
registry plus Qdrant approximate vector counts. When the registry is PostgreSQL-backed, `last_indexed_at`
is derived from `MAX(last_heartbeat)`. Partial Qdrant failures degrade gracefully.

New Prometheus gauge: `emdexer_gateway_namespace_vector_count`.

#### P43 — `emdex eval` CLI Command (`cmd`)

`emdex eval` loads a YAML/JSON evaluation suite, calls the gateway search API for each query, and
renders pass/fail results with per-query latency and score summaries. Supports truncation for large
result sets and JSON output.

#### P44 — Webhook on Namespace Index Completion (`gateway`, `docker`, `helm`)

SSRF-safe outbound webhook dispatcher fires a `POST` to `EMDEX_WEBHOOK_URL` after a namespace finishes
indexing. Includes exponential backoff retry on 5xx, request timeout, and private-IP blocklist.
Docker-compose and Helm chart expose the new env var.

New env var: `EMDEX_WEBHOOK_URL`.

### Fixed

- **search**: add `custom` indicator to `rrf_config` debug field; make RRF config settings configurable
- **llm/p40**: Prometheus metrics and OTel spans for Ollama backend; CI coverage
- **security**: upgrade Go 1.26.1 → 1.26.2; tidy integration `go.mod`
- **deps**: tidy CLI and test `go.mod` files

### Dependencies

- `golang.org/x/crypto` 0.49.0 → 0.50.0
- `modernc.org/sqlite` 1.48.1 → 1.48.2
- `go.opentelemetry.io/otel/sdk` 1.42.0 → 1.43.0
- `python-multipart` bumped in plugin-sidecar and ffmpeg-sidecar

---

## [1.2.0] — 2026-04-08 — Phase 29: Server-Side RRF

### Highlights

- Hybrid search migrated from client-side Go RRF to Qdrant's Universal Query API — one round-trip instead of two
- Raft cluster health probe (`/healthz/qdrant`) — surfaces split-brain in HA deployments before traffic is routed
- Configurable async audit buffer (`EMDEX_AUDIT_BUFFER_SIZE`) — prevents log back-pressure under bursty search load

### New Features

#### 🔍 Server-Side Hybrid Search via Universal Query API (`shared`, `backend`, `gateway`)

`HybridSearch` and `HybridSearchByPaths` now issue a single `pc.Query()` call with two server-side `PrefetchQuery` legs — a dense-vector prefetch and a text-match-filter prefetch — fused by Qdrant using `Fusion_RRF`. This replaces the previous two-goroutine + client-side `MergeRRFHybrid` approach and halves the number of Qdrant gRPC round-trips per search request.

Backward-compatible: if the Query API is unavailable (Qdrant < 1.10.0), both functions transparently fall back to vector-only `SearchQdrant` and increment the existing `bm25_fallback_total` counter so the `HybridFallbackActive` alert fires.

`MergeRRFHybrid`, `MergeRRF`, and `MergeRRFWeighted` are retained for cross-namespace fan-out merging and graph-RAG secondary leg weighting.

#### 🏥 Raft Cluster Health Probe (`backend`, `gateway`, `docker`, `helm`)

`registry.CheckRaftCluster(ctx, addr)` queries Qdrant's `/cluster` REST endpoint and returns a `ClusterStatus` struct. The gateway exposes this as `GET /healthz/qdrant` alongside the existing liveness/readiness/startup probes. Single-node deployments (cluster disabled) are treated as healthy. The HA docker-compose and Helm chart now consume `QDRANT_HTTP_HOST` to locate the HTTP endpoint.

#### 📋 Configurable Audit Buffer (`backend`, `logging`, `envs`)

`EMDEX_AUDIT_BUFFER_SIZE` (range [100, 100000], default 1000) sets the async channel depth of the audit log writer. Previously hardcoded at 1000; high-throughput HA gateways can now raise this to avoid dropping entries during traffic spikes.

### Changed

#### Observability (`metrics`, `observability`, `logging`)

| Before (Phase 21–22) | After (Phase 29) |
|---|---|
| `emdexer_gateway_search_vector_duration_ms` | Unchanged — still emitted by vector-only `SearchQdrant` path |
| `emdexer_gateway_search_bm25_duration_ms` | **Removed** — BM25 is now a server-side prefetch leg, not a separate client call |
| `emdexer_gateway_rrf_top_vector_hits_total` | **Removed** — individual leg attribution is not observable from server-side RRF |
| `emdexer_gateway_rrf_top_bm25_hits_total` | **Removed** |
| `emdexer_gateway_rrf_top_both_legs_hits_total` | **Removed** |
| `emdexer_gateway_bm25_fallback_total` | Retained — now fires when the Query API itself fails |
| `emdexer_gateway_bm25_zero_results_total` | Retained — now fires when the unified result set is empty |
| _(new)_ `emdexer_gateway_search_unified_query_duration_ms` | Latency histogram for the single `pc.Query` call |

OTel span `emdex.search.hybrid` gains attribute `search.mode=server_rrf`.

#### Prometheus Alerts (`observability`, `pipelines`)

- `HighBM25Latency` **replaced** by `UnifiedSearchHighLatency` (fires when unified query p99 > 1 s)
- `BM25IndexFailure` and `HybridFallbackActive` descriptions updated to reflect server-side RRF semantics

#### Infrastructure (`docker`, `helm`, `envs`)

- HA docker-compose: Qdrant healthcheck now verifies `/cluster` Raft status alongside HTTP liveness
- Both gateway replicas: `QDRANT_HTTP_HOST` and `EMDEX_AUDIT_BUFFER_SIZE` added to environment
- Helm `values.yaml`: `config.qdrantHttpHost` and `config.auditBufferSize` passthrough added
- Helm `deployment.yaml`: `QDRANT_HTTP_HOST` and `EMDEX_AUDIT_BUFFER_SIZE` env blocks added

#### CI/CD (`pipelines`, `tests`)

- `./audit/...` added to the unit test run
- `go build ./search/...` smoke step added to validate Universal Query API compilation on every push

#### MCP (`mcp`)

No changes required. The `search_files` tool calls `GET /v1/search` whose response schema is unchanged.

#### Plugin system (`plugin`)

No changes required. Plugin extraction runs at index time (node); the unified search result schema is structurally identical to the previous one.

#### Node / VFS / Extraction (`node`)

No changes required. `EnsureTextIndexes` still runs at startup and creates the full-text index on `text` and `namespace` fields — these are now consumed by the text-match prefetch leg instead of the scroll-based BM25 call.

### Breaking Changes

None. Callers of `HybridSearch`, `HybridSearchByPaths`, `SearchQdrant`, `MergeRRF`, and `MergeRRFWeighted` are interface-compatible. The function `BM25SearchQdrant` has been removed; it had no callers outside the `search` package.

Qdrant **≥ 1.10.0** is required for the hybrid path. Earlier versions fall back to vector-only automatically.

### Migration Notes

- Grafana dashboards using `emdexer_gateway_search_bm25_duration_ms` or `emdexer_gateway_rrf_top_*` panels should be updated to use `emdexer_gateway_search_unified_query_duration_ms`.
- If `EMDEX_QDRANT_TLS=false` (default), set `QDRANT_HTTP_HOST=<host>:6333` in HA deployments where the gRPC port is not `6334` to ensure `/healthz/qdrant` resolves correctly.

---

## 1.1.1 (2026-03-24)

### Bug Fixes

- **CI**: Build and test workflows now trigger on push/PR to `main` in addition to `develop`; added `workflow_dispatch` so builds can be triggered manually on any branch

---

## 1.1.0 (2026-03-23)

### Highlights

- Hybrid Search — Vector + BM25 with Reciprocal Rank Fusion
- Agentic RAG — multi-hop reasoning loop with LLM confidence assessment
- Graph-RAG — filesystem knowledge graph for structural context expansion
- Universal Plugin System — drop Python scripts in a folder, loaded automatically
- Full Multi-Modal — Whisper transcription, Gemini Vision image captioning, FFmpeg video frames (all opt-in)
- OpenTelemetry — end-to-end distributed tracing, zero overhead when unused
- Security — Qdrant mTLS, PostgreSQL read replica, Grafana dashboards

### New Features

#### 🔍 Hybrid Search with RRF (Phase 21–22)

BM25 keyword search and vector search run concurrently; results merged via configurable Reciprocal Rank Fusion. Automatic full-text index creation on startup. Dynamic tuning without redeployment: `EMDEX_RRF_K`, `EMDEX_RRF_VECTOR_WEIGHT`, `EMDEX_RRF_BM25_WEIGHT`.

#### 🤖 Agentic Multi-Hop RAG (Phase 20)

Gateway iteratively retrieves and refines context — up to `EMDEX_MAX_HOPS` (default: 3) — until the LLM confidence threshold is met. Falls back gracefully on any error. Namespace isolation enforced across all hops.

#### 🕸️ Graph-RAG (Phase 24)

The node extracts `imports`, `links_to`, and `defines` relations during indexing and stores them in Qdrant. The gateway builds a per-namespace knowledge graph and expands search results with structurally related files. Auto-migration detects and re-indexes pre-v1.1.0 collections on startup.

#### 🔌 Plugin System (Phase 25)

Python scripts placed in `EMDEX_PLUGIN_DIR` are loaded automatically at node startup. A dedicated plugin sidecar (`src/plugin-sidecar/`, Python 3.14) serves plugins over HTTP so the Go node stays statically linked. Bundled example: CSV extractor.

#### 🎬 Multi-Modal (Phase 26, all opt-in)

- **Whisper** (`EMDEX_WHISPER_ENABLED`) — audio/video transcription with 503 retry, quality filter, language hint, and timed segment timestamps
- **Gemini Vision** (`EMDEX_VISION_ENABLED`) — image captioning via `gemini-3-flash-preview`, takes priority over OCR
- **FFmpeg** (`EMDEX_FRAME_ENABLED`) — video frame extraction via `src/ffmpeg-sidecar/` (Python 3.14), frames optionally captioned by Vision

#### 🔭 OpenTelemetry Tracing (Phase 27)

OTLP/gRPC exporter compatible with Jaeger, Tempo, and Honeycomb. Traces the full pipeline: search → graph expansion → agentic hops → embed → LLM. Zero overhead when `EMDEX_OTEL_ENDPOINT` is unset. W3C trace context propagation supported.

#### 🔒 Security & Reliability (Phase 28)

- **Qdrant mTLS** — opt-in via `EMDEX_QDRANT_TLS*` env vars; three modes: insecure (default), TLS, full mTLS
- **PostgreSQL read replica** — `EMDEX_PG_REPLICA_URL`; reads routed to replica, writes to primary; non-fatal fallback
- **Grafana dashboards** — 4 drop-in JSON dashboards in `deploy/monitoring/grafana/`: overview, search, rag, multimodal

### Breaking Changes

None. All new features are opt-in. Default behavior is identical to v1.0.0.

### Migration Notes

Three features are enabled by default and trigger a one-time re-index on first startup:

- **Hybrid Search** — node creates a full-text index on the `text` field
- **Agentic RAG** — chat completions now use up to 3 retrieval hops (set `EMDEX_AGENTIC_ENABLED=false` to revert)
- **Graph-RAG** — auto-migration detects missing `relations` fields and re-indexes automatically

---

## 1.0.0 (2026-03-20)


### Features

* initial release ([12a736f](https://github.com/piotrlaczykowski/emdexer/commit/12a736ffd6ff619db7474bb4dfb0c6d20059f2a3))
