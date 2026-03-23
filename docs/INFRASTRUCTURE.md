# Infrastructure Guide

This document covers deployment topology, sidecar services, and operational runbooks for emdexer.

---

## Deployment Topologies

| Compose file | Use case |
|---|---|
| `deploy/docker/docker-compose.yml` | Single-node: 1 gateway + 1 node + sidecars |
| `deploy/docker/docker-compose.multi-node.yml` | Multi-node: 1 gateway + N indexing nodes |
| `deploy/docker/docker-compose.ha.yml` | High-availability: 2 gateways behind nginx + postgres for registry |

All topologies share the same sidecar services (Extractous, Whisper, Qdrant).

---

## Sidecar Services

### Extractous Sidecar

**Purpose:** Text extraction from binary documents (PDF, DOCX, XLSX, PPTX, images).
**Image:** Built from `deploy/docker/extractous-sidecar/Dockerfile` (Python 3.12 + `extractous` library + Tesseract OCR).
**Port:** `8000` (internal only — not exposed to host).
**Network alias:** `extractous`

#### API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Returns `{"status": "ok"}`. Used by Docker healthcheck and readiness gates. |
| `POST` | `/extract` | Extract text from an uploaded file. Multipart form-data, field `file`. |
| `POST` | `/extract?ocr=true` | Same, with Tesseract OCR enabled. Used for images and scanned PDFs. |

Response body (both extract endpoints):
```json
{
  "text": "<extracted text>",
  "metadata": { "Content-Type": "application/pdf", ... }
}
```

#### Health Check

```bash
# From host (if port is mapped for testing):
curl http://localhost:8000/health

# From inside the Docker network:
docker exec <container> python -c \
  "import urllib.request; print(urllib.request.urlopen('http://localhost:8000/health').read())"
```

#### Rebuild After Dependency Update

```bash
docker compose -f deploy/docker/docker-compose.yml build extractous --no-cache
docker compose -f deploy/docker/docker-compose.yml up -d extractous
```

#### Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Node logs `extraction failed` | Sidecar is down | `docker compose logs extractous` |
| OCR returns empty text | Tesseract not installed or language pack missing | Rebuild image without cache |
| `cb open` errors in node logs | Circuit breaker tripped after 5 consecutive failures | Restart sidecar; circuit resets after 5 min |
| Build fails with missing `extractous-sidecar/` | Directory was missing from repo | `git pull` to get latest; it now lives at `deploy/docker/extractous-sidecar/` |

---

### Whisper Sidecar

**Purpose:** Audio/video transcription (MP3, WAV, MP4, MKV, etc.).
**Image:** `ghcr.io/ggml-org/whisper.cpp:main` (pre-built; no local build).
**Port:** `8080` (internal only).
**Network alias:** `whisper`

Enable by setting `EMDEX_WHISPER_URL=http://whisper:8080` in your `.env`. When unset, audio/video files are skipped during indexing.

#### Model Download

The whisper model is downloaded once on first run and cached in the `whisper-models` Docker volume. Model size depends on `EMDEX_WHISPER_MODEL`:

| Model | Size | Accuracy |
|-------|------|---------|
| `base` (default) | ~150 MB | Good for most use cases |
| `small` | ~480 MB | Better accuracy |
| `medium` | ~1.5 GB | High accuracy |

---

### Qdrant

**Purpose:** Vector database for semantic search and BM25 full-text index.
**Image:** `qdrant/qdrant:v1.13.0` (pinned).
**Ports:** `6333` (HTTP), `6334` (gRPC) — internal only.

The BM25 full-text index on the `text` field is created automatically by the node on startup via `EnsureTextIndexes()`. If you add a collection manually, create the index:

```bash
curl -X PUT http://qdrant:6333/collections/emdexer_v1/index \
  -H 'Content-Type: application/json' \
  -d '{"field_name": "text", "field_schema": "text"}'
```

---

## Observability

### Prometheus Alerts

Alert rules live in `deploy/monitoring/prometheus/alerts-search.yml`. Load them by adding to your `prometheus.yml`:

```yaml
rule_files:
  - /etc/prometheus/alerts-search.yml
```

| Alert | Severity | Condition | Action |
|-------|----------|-----------|--------|
| `HighBM25Latency` | warning | BM25 avg > 2× vector avg for 5 min | Check Qdrant full-text index exists |
| `BM25IndexFailure` | warning | >50 zero-result BM25 queries in 5 min | Verify `text` payload field in documents |
| `HybridFallbackActive` | warning | Any BM25 error triggers fallback | Check gateway logs for BM25 failure details |
| `AgenticHighHopRate` | warning | Avg extra hops per request > 2 for 5 min | Lower `EMDEX_HOP_CONFIDENCE_THRESHOLD` or `EMDEX_MAX_HOPS` |
| `AgenticLowEarlyStopRate` | warning | Early-stop ratio < 20% for 10 min | Threshold too high for corpus; lower `EMDEX_HOP_CONFIDENCE_THRESHOLD` |

### Key Metrics

See `docs/reference/hybrid-search.md` and `docs/reference/agentic-rag.md` for full metric lists. Quick reference:

```promql
# BM25 average latency (ms)
rate(emdexer_gateway_search_bm25_duration_ms_sum[5m])
  / rate(emdexer_gateway_search_bm25_duration_ms_count[5m])

# RRF result distribution (vector-only vs BM25-only vs both)
rate(emdexer_gateway_rrf_top_vector_hits_total[5m])
rate(emdexer_gateway_rrf_top_bm25_hits_total[5m])
rate(emdexer_gateway_rrf_top_both_legs_hits_total[5m])

# Fallback rate
rate(emdexer_gateway_bm25_fallback_total[5m])

# Agentic RAG — additional hops per minute
rate(emdexer_gateway_agentic_hops_total[5m])

# Agentic RAG — early-stop rate
rate(emdexer_gateway_agentic_early_stop_total[5m])

# Agentic RAG — P90 confidence score at assessment time
histogram_quantile(0.9, rate(emdexer_gateway_agentic_confidence_score_bucket[5m]))
```

---

## Deployment Checklist

Before running `docker compose up -d`:

1. Copy `.env.example` → `.env` and fill in `GOOGLE_API_KEY`, `EMDEX_AUTH_KEY`.
2. Verify Qdrant storage path is writable: `ls -la deploy/docker/qdrant_storage/` (created on first run).
3. For multi-node: set `DOCS_DATA_PATH` and `CODE_DATA_PATH` in `.env` to your data directories.
4. For HA: set `POSTGRES_PASSWORD` in `.env`.
5. After startup, verify all services are healthy:
   ```bash
   docker compose ps
   # All services should show "healthy" or "running"
   ```
6. Verify the gateway is reachable:
   ```bash
   curl -H "Authorization: Bearer $EMDEX_AUTH_KEY" http://localhost:7700/healthz/readiness
   # → {"status":"UP"}
   ```
7. Verify the extractous sidecar is reachable from the node:
   ```bash
   docker compose exec node wget -qO- http://extractous:8000/health
   # → {"status":"ok"}
   ```
