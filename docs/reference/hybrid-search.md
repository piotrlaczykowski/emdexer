# Hybrid Search (BM25 + Vector RRF)

Emdexer supports hybrid search that combines **dense vector similarity** with **BM25 keyword matching**, merging both result sets via Reciprocal Rank Fusion (RRF). Documents that score well in both legs bubble to the top; pure-vector or pure-keyword hits are still surfaced but ranked lower.

---

## How It Works

```
Query
  ├─► Vector Search  (semantic similarity via embeddings)  ─┐
  └─► BM25 Search    (keyword match via full-text index)   ─┴─► RRF Merge ──► ranked results
```

Both legs run **concurrently** in the gateway. Results are fused with the weighted RRF formula:

```
score(d) = Σ  weight_i × (1 / (K + rank_i(d)))
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `K` | 60 | Rank-smoothing constant. Higher = gentler slope. Standard RRF default. |
| `weight_vector` | 1.0 | Multiplier for the vector leg contribution. |
| `weight_bm25` | 1.0 | Multiplier for the BM25 leg contribution. Set to `0` to run vector-only. |

Documents that appear in both legs accumulate score from each, naturally surfacing high-confidence hits.

**Fallback:** If BM25 fails (e.g., the full-text index has not been created yet), the gateway automatically falls back to vector-only results and logs a warning. Search always returns a response.

---

## Configuration

### `EMDEX_BM25_ENABLED`

| Value | Behavior |
|-------|----------|
| `true` (default) | Hybrid search: vector + BM25 with RRF merge |
| `false` | Vector-only search |

### RRF Tuning (Phase 22)

Fine-tune the fusion balance without redeploying. All parameters are optional — omit them to use the defaults.

| Variable | Default | Range | Description |
|----------|---------|-------|-------------|
| `EMDEX_RRF_K` | `60` | ≥ 1 | Rank-smoothing constant. Higher values reduce the scoring gap between ranks. |
| `EMDEX_RRF_VECTOR_WEIGHT` | `1.0` | 0 – 10 | Score multiplier for the vector leg. Set to `0` for BM25-only mode. |
| `EMDEX_RRF_BM25_WEIGHT` | `1.0` | 0 – 10 | Score multiplier for the BM25 leg. Set to `0` for vector-only mode. |

**Example: boost semantic results for a domain with rich natural language**
```env
EMDEX_RRF_VECTOR_WEIGHT=1.5
EMDEX_RRF_BM25_WEIGHT=0.5
```

**Example: boost keyword precision for a technical code corpus**
```env
EMDEX_RRF_VECTOR_WEIGHT=0.8
EMDEX_RRF_BM25_WEIGHT=1.5
```

**Example: increase rank smoothing to reduce sensitivity to top-1 results**
```env
EMDEX_RRF_K=80
```

In the Helm chart `values.yaml`:
```yaml
config:
  bm25Enabled: "true"
  rrfK: "60"
  rrfVectorWeight: "1.0"
  rrfBm25Weight: "1.0"
```

---

## Full-Text Index Setup

BM25 search requires a Qdrant **full-text payload index** on the `text` field. The node creates this index automatically on startup via `EnsureTextIndexes()` — no manual setup is needed.

If you add a new collection manually (outside of the normal node startup), create the index yourself:

```bash
curl -X PUT http://localhost:6333/collections/{collection}/index \
  -H 'Content-Type: application/json' \
  -d '{"field_name": "text", "field_schema": "text"}'
```

---

## Global Search (Cross-Namespace RRF)

When `namespace=*` (or `namespace=__global__`), the gateway fans out the query to every registered namespace in parallel. Each namespace returns its own hybrid search results, which are then merged via a second RRF pass. Each result includes a `source_namespace` field in the payload.

Partial failures (individual nodes timing out) are reported in `partial_failures` — healthy nodes still contribute results.

---

## Metrics

All metrics are Prometheus histograms or counters, labelled by `{collection, namespace}`.

| Metric | Type | Description |
|--------|------|-------------|
| `emdexer_gateway_search_vector_duration_ms` | Histogram | Vector leg latency |
| `emdexer_gateway_search_bm25_duration_ms` | Histogram | BM25 leg latency |
| `emdexer_gateway_search_hybrid_total_ms` | Histogram | End-to-end hybrid search latency (both legs + RRF merge) |
| `emdexer_gateway_rrf_top_vector_hits_total` | Counter | Returned results sourced exclusively from the vector leg |
| `emdexer_gateway_rrf_top_bm25_hits_total` | Counter | Returned results sourced exclusively from the BM25 leg |
| `emdexer_gateway_rrf_top_both_legs_hits_total` | Counter | Returned results that appeared in both legs (overlap) |
| `emdexer_gateway_bm25_fallback_total` | Counter | Times hybrid fell back to vector-only due to a BM25 failure |
| `emdexer_gateway_bm25_zero_results_total` | Counter | Times BM25 returned 0 results (index may be empty or query too specific) |
| `emdexer_gateway_sse_subscribers` | Gauge | Active SSE subscribers on `/v1/events/indexing` |
| `emdexer_gateway_sse_events_published_total` | Counter | Indexing events published to the SSE bus |
| `emdexer_gateway_sse_events_dropped_total` | Counter | Indexing events dropped due to slow SSE consumers |

**Useful alert rules:**
- High `bm25_duration_ms` relative to `vector_duration_ms` → full-text index may be missing (Qdrant doing a full scan).
- Rising `bm25_fallback_total` → BM25 is failing consistently; check node startup logs.
- High `bm25_zero_results_total` → queries aren't matching the text index; verify documents were indexed with a `text` payload field.
- Low `rrf_top_both_legs_hits_total` / total → legs are returning disjoint results; consider adjusting weights.

---

## Troubleshooting

**BM25 returns no results / falls back to vector-only**

Check the gateway logs for:
```
[search] BM25 failed for collection "..." — falling back to vector-only: ...
```

Common causes:
- Full-text index not created yet — restart the node to trigger `EnsureTextIndexes()`, or create the index manually (see above).
- `EMDEX_BM25_ENABLED=false` — hybrid search is disabled.
- The query contains only stop words or punctuation that the tokenizer drops.

**Results seem worse with hybrid enabled**

- Verify the full-text index exists on the `text` field (not just `namespace`).
- Check that documents were indexed with a `text` payload field. If the field is empty, BM25 will match nothing and RRF falls back to vector ranking.

**High BM25 latency**

- Confirm the `text` index was created with `field_schema: "text"` (not `keyword`). A keyword index does not support full-text matching and forces a full collection scan.
