# Configuration Reference

All environment variables for the Emdexer gateway and node, grouped by feature area. Copy `.env.example` to `.env` or set them in your Docker Compose / Helm values.

---

## Gateway

| Variable | Default | Description |
|----------|---------|-------------|
| `GOOGLE_API_KEY` | — | Google Gemini API key (embedding + LLM) |
| `QDRANT_HOST` | `localhost:6334` | Qdrant gRPC endpoint |
| `EMDEX_QDRANT_COLLECTION` | `emdexer_v1` | Qdrant collection name |
| `EMDEX_AUTH_KEY` | — | Static API key for gateway auth |
| `EMDEX_API_KEYS` | — | Comma-separated additional API keys |
| `EMDEX_PORT` | `7700` | Gateway HTTP listen port |
| `EMDEX_GEMINI_MODEL` | `gemini-embedding-2-preview` | Embedding model |
| `EMDEX_LLM_MODEL` | `gemini-3-flash-preview` | LLM model for chat / agentic hops |
| `EMDEX_SEARCH_LIMIT` | `10` | Max results per search |
| `EMDEX_CHAT_LIMIT` | `5` | Max chunks used in chat context |
| `EMDEX_GLOBAL_SEARCH_TIMEOUT` | `500` | Global fan-out timeout (ms) |
| `EMDEX_AUDIT_LOG_FILE` | `logs/audit.json` | Path for structured audit log |

---

## Qdrant mTLS

All Qdrant TLS settings are opt-in. The default is plaintext gRPC (unchanged behaviour).

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_QDRANT_TLS` | `false` | Enable TLS for Qdrant gRPC connection |
| `EMDEX_QDRANT_TLS_CA` | — | CA cert PEM path — enables server certificate verification |
| `EMDEX_QDRANT_TLS_CERT` | — | Client cert PEM path — enables full mTLS |
| `EMDEX_QDRANT_TLS_KEY` | — | Client key PEM path — required with `EMDEX_QDRANT_TLS_CERT` |

### Decision table

| `EMDEX_QDRANT_TLS` | CA cert | Client cert | Result |
|-------------------|---------|-------------|--------|
| unset / `false` | any | any | Plaintext gRPC (default) |
| `true` | not set | not set | TLS, `InsecureSkipVerify=true` |
| `true` | set | not set | TLS with server verification |
| `true` | set | set | Full mTLS |

---

## Hybrid Search (BM25 + Vector)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_BM25_ENABLED` | `true` | Enable hybrid BM25+vector search |
| `EMDEX_RRF_K` | `60` | RRF rank-smoothing constant |
| `EMDEX_RRF_VECTOR_WEIGHT` | `1.0` | Score multiplier for vector leg [0–10] |
| `EMDEX_RRF_BM25_WEIGHT` | `1.0` | Score multiplier for BM25 leg [0–10] |

---

## Agentic Multi-Hop RAG

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_AGENTIC_ENABLED` | `true` | Enable iterative retrieval loop |
| `EMDEX_MAX_HOPS` | `3` | Maximum retrieval hops (1–5) |
| `EMDEX_HOP_CONFIDENCE_THRESHOLD` | `0.7` | LLM confidence at which loop stops early |

---

## Graph-RAG

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_GRAPH_ENABLED` | `true` | Enable knowledge-graph neighbour expansion |
| `EMDEX_GRAPH_DEPTH` | `1` | BFS hop depth for neighbour expansion (1–3) |
| `EMDEX_GRAPH_MIGRATION` | `auto` | Re-index trigger: `auto` \| `skip` \| `force` |

---

## OIDC / JWT Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `OIDC_ISSUER` | — | OIDC provider URL — enables JWT validation |
| `OIDC_CLIENT_ID` | — | Expected `aud` claim |
| `OIDC_GROUPS_CLAIM` | `groups` | JWT claim that carries group membership |
| `EMDEX_GROUP_ACL` | — | JSON map of group → namespace list, e.g. `{"hr": ["hr","hiring"]}` |

---

## High Availability (PostgreSQL Registry)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_HA_MODE` | `false` | Enforce PostgreSQL registry — fatal if `POSTGRES_URL` missing |
| `POSTGRES_URL` | — | Primary PostgreSQL DSN |
| `EMDEX_PG_REPLICA_URL` | — | Optional read replica DSN — `List` queries routed here; fallback to primary on error |

---

## Distributed Tracing (OpenTelemetry)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_OTEL_ENDPOINT` | — | OTLP/gRPC collector address — unset = disabled |
| `EMDEX_OTEL_SERVICE_NAME` | `emdex-gateway` | `service.name` resource attribute |
| `EMDEX_OTEL_SAMPLING_RATIO` | `1.0` | Head-based sampling ratio [0.0–1.0] |

---

## Node

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_GATEWAY_URL` | — | Gateway URL for node registration |
| `EMDEX_GATEWAY_AUTH_KEY` | — | Auth key matching gateway's `EMDEX_AUTH_KEY` |
| `EMDEX_NAMESPACE` | `default` | Namespace tag applied to all indexed vectors |
| `NODE_TYPE` | `local` | VFS backend: `local` \| `smb` \| `sftp` \| `nfs` \| `s3` |
| `NODE_ROOT` | — | Root path for local/NFS/SMB VFS |
| `NODE_HEALTH_PORT` | `8081` | Node health endpoint port |
| `EMDEX_EXTRACTOUS_URL` | `http://localhost:8000/extract` | Extractous sidecar URL |
| `EMDEX_BATCH_SIZE` | `500` | Qdrant upsert flush threshold (points per batch) |
| `EMDEX_INDEX_WORKERS` | `1` | Parallel workers for startup walk (1–16); increase for faster bulk indexing with local Ollama |
| `EMDEX_MAX_FILE_SIZE` | `50MB` | Skip files larger than this |
| `EMDEX_MAX_ARCHIVE_ENTRY_SIZE` | `10MB` | Skip archive entries larger than this |
| `EMDEX_DELTA_ENABLED` | `1` | Enable checksum-based delta detection |
| `EMDEX_FULL_HASH` | `0` | Enable full XXH3 hash (Stage 3) for maximum accuracy |
| `EMDEX_EMBEDDING_DIMS` | `3072` | Embedding vector dimensions |

---

## Multi-Modal Hardening (Phase 26)

See [docs/reference/multimodal.md](multimodal.md) for full details on all three tracks.

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_WHISPER_ENABLED` | `false` | Track 1: enable audio/video transcription |
| `EMDEX_WHISPER_URL` | `http://whisper:8080` | Whisper sidecar address |
| `EMDEX_WHISPER_MODEL` | `base` | Whisper model: `base` \| `small` \| `medium` \| `large` |
| `EMDEX_WHISPER_MIN_CHARS` | `50` | Discard transcripts shorter than N characters |
| `EMDEX_WHISPER_LANGUAGE` | — | Optional language hint (e.g. `en`) |
| `EMDEX_ENABLE_OCR` | `true` | Enable Tesseract OCR via Extractous |
| `EMDEX_VISION_ENABLED` | `false` | Track 2: enable Gemini Vision image captioning |
| `EMDEX_VISION_MAX_SIZE_MB` | `10` | Skip images larger than this (MB) |
| `EMDEX_FRAME_ENABLED` | `false` | Track 3: enable FFmpeg video frame extraction |
| `EMDEX_FFMPEG_URL` | — | FFmpeg sidecar address |
| `EMDEX_FRAME_INTERVAL_SEC` | `30` | Seconds between extracted frames |
| `EMDEX_MAX_FRAMES` | `10` | Maximum frames per video |
| `EMDEX_EXCLUDE_PATHS` | — | Comma-separated dir names or glob patterns skipped before any file I/O (e.g. `#recycle,.Trash,.DS_Store`) |

---

## Query Embedding Cache & Timeout

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_EMBED_CACHE_SIZE` | `1000` | Number of query-embedding entries held in the gateway LRU+TTL cache (TTL: 5 min). Set to `0` to disable. |
| `EMDEX_EMBED_TIMEOUT` | `30000` | Timeout (ms) for each embedding call in search, chat, and graph-search handlers. |

---

## Chunking (Phase 31 / Phase 35)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_CHUNK_SIZE` | `512` | Words per chunk (fixed strategy) |
| `EMDEX_CHUNK_OVERLAP` | `50` | Overlapping words between adjacent chunks; clamped to `< EMDEX_CHUNK_SIZE` |
| `EMDEX_CHUNK_STRATEGY` | `fixed` | Chunking strategy: `fixed` (word-count) or `semantic` (sentence-embedding grouping) |
| `EMDEX_SEMANTIC_CHUNK_THRESHOLD` | `0.7` | Cosine similarity threshold for sentence grouping [0.0–1.0]; only used when strategy is `semantic` |
| `EMDEX_SEMANTIC_CHUNK_MAX_SIZE` | `512` | Max words per semantic chunk; only used when strategy is `semantic` |

**Semantic chunking** groups sentences by embedding similarity instead of word count. Produces more coherent chunks at the cost of ~1 embedding call per sentence during indexing. Falls back to fixed chunking if the embedder fails or the document has ≤ 3 sentences.

---

## Ollama LLM Provider (Phase 40)

Set `EMDEX_OLLAMA_URL` to switch the gateway's LLM backend from Gemini to a local or remote Ollama instance. When set, `GOOGLE_API_KEY` is no longer used for LLM calls (embedding is unaffected).

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_OLLAMA_URL` | — | Base URL of the Ollama server (e.g. `http://192.168.0.157:11434`). Unset = use Gemini. |
| `EMDEX_OLLAMA_MODEL` | `gemma4:26b` | Ollama model tag to use for chat completions (e.g. `llama3.2`, `mistral`). |

**Example** — air-gap / local setup:

```env
EMDEX_OLLAMA_URL=http://192.168.0.157:11434
EMDEX_OLLAMA_MODEL=gemma4:26b
```

The gateway logs which provider is active at startup:

```
[gateway] LLM provider: ollama url=http://192.168.0.157:11434 model=gemma4:26b
```

---

## Contextual Retrieval (Phase 32)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_CONTEXTUAL_RETRIEVAL` | `false` | Prepend LLM-generated document context to each chunk before embedding |
| `EMDEX_CONTEXT_MODEL` | _(uses `EMDEX_LLM_MODEL`)_ | Override model for context generation |

---

## Extractor Plugins (Phase 25)

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_PLUGIN_ENABLED` | `true` | Enable the Python plugin system |
| `EMDEX_PLUGIN_DIR` | `./plugins` | Directory scanned for `*.py` plugin files |
| `EMDEX_PLUGIN_TIMEOUT` | `10s` | Max execution time per plugin |
| `EMDEX_PLUGIN_SIDECAR_URL` | — | Plugin sidecar URL (avoids Python on node host) |
