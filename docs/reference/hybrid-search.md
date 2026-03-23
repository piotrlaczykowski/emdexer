# Hybrid Search (BM25 + Vector RRF)

Emdexer supports hybrid search that combines **dense vector similarity** with **BM25 keyword matching**, merging both result sets via Reciprocal Rank Fusion (RRF). Documents that score well in both legs bubble to the top; pure-vector or pure-keyword hits are still surfaced but ranked lower.

---

## How It Works

```
Query
  ├─► Vector Search  (semantic similarity via embeddings)  ─┐
  └─► BM25 Search    (keyword match via full-text index)   ─┴─► RRF Merge ──► ranked results
```

Both legs run **concurrently** in the gateway. Results are fused with the standard RRF formula:

```
score(d) = Σ  1 / (k + rank_i(d))     k = 60
```

Documents that appear in both legs accumulate score from each, naturally surfacing high-confidence hits. The k=60 constant dampens the advantage of top-ranked documents and is the standard RRF default.

**Fallback:** If BM25 fails (e.g., the full-text index has not been created yet), the gateway automatically falls back to vector-only results and logs a warning. Search always returns a response.

---

## Configuration

### `EMDEX_BM25_ENABLED`

| Value | Behavior |
|-------|----------|
| `true` (default) | Hybrid search: vector + BM25 with RRF merge |
| `false` | Vector-only search |

Set in `.env`:
```env
EMDEX_BM25_ENABLED=true
```

Or in `docker-compose.yml` (already wired):
```yaml
environment:
  EMDEX_BM25_ENABLED: ${EMDEX_BM25_ENABLED:-true}
```

Or in the Helm chart `values.yaml`:
```yaml
gateway:
  bm25Enabled: "true"
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

| Metric | Description |
|--------|-------------|
| `emdexer_gateway_search_latency_ms` | Vector leg latency (labelled by collection) |
| `emdexer_gateway_bm25_latency_ms` | BM25 leg latency (labelled by collection) |

Both are Prometheus histograms. A significantly higher `bm25_latency` relative to `search_latency` may indicate the full-text index is missing and Qdrant is doing a full scan.

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
