# Changelog

## [1.0.1](https://github.com/piotrlaczykowski/emdexer/compare/v1.0.0...v1.0.1) (2026-03-24)


### Bug Fixes

* **ci:** run builds on main, add workflow_dispatch ([#66](https://github.com/piotrlaczykowski/emdexer/issues/66)) ([b6d0d61](https://github.com/piotrlaczykowski/emdexer/commit/b6d0d610b3519b8e81f73c34e93eb63212e7ce58))

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
