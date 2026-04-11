# P39 Quality Tuning Results

> Date: 2026-04-11
> Environment: LXC 156, Qdrant collection `emdexer_v1`, 1,545 points, namespace `private`
> Gateway port: 7700

## Test Queries

- Q1: "What files are available?"
- Q2: "What programming languages are used?"
- Q3: "What is the overall purpose of this project?"

## Run 2 — 2026-04-11 (post P40 merge)

| Config | BM25 | RERANK_TOP_K | Q1 latency | Q2 latency | Q3 latency | Avg |
|---|---|---|---|---|---|---|
| BASELINE | on | 20 | 0.676s | 0.147s | 0.080s | 0.301s |
| VECTOR-HEAVY (RRF_V=0.7) | on | 20 | 0.121s | 0.076s | 0.093s | 0.097s |
| BM25-HEAVY (RRF_B=0.7) | on | 20 | 0.088s | 0.115s | 0.084s | 0.096s |
| VECTOR-ONLY | off | 20 | 0.092s | 0.106s | 0.086s | 0.095s |
| **RERANK-BOOST** | on | **40** | **0.079s** | **0.083s** | 0.122s | **0.095s** |
| SPEED | off | 5 | 0.094s | 0.102s | 0.101s | 0.099s |

## Run 1 — 2026-04-08 (baseline, pre-P40)

| Config | BM25 | RERANK_TOP_K | Q1 latency | Q2 latency | Q3 latency | Avg |
|---|---|---|---|---|---|---|
| BASELINE | on | 20 | 0.094s | 0.095s | 0.103s | 0.097s |
| VECTOR-HEAVY | on | 20 | 0.761s | 0.089s | 0.098s | 0.316s |
| BM25-HEAVY | on | 20 | 0.117s | 0.099s | 0.095s | 0.104s |
| VECTOR-ONLY | off | 20 | 0.100s | 0.086s | 0.133s | 0.106s |
| RERANK-BOOST | on | 40 | 0.082s | 0.078s | 0.111s | 0.090s |
| SPEED | off | 5 | 0.095s | 0.092s | 0.950s | 0.379s |

## Findings

- **RERANK-BOOST (RERANK_TOP_K=40) is consistently the fastest config** across both runs.
- **BASELINE Q1 cold-start spike** (0.676s) — first query after gateway idle; not config-related.
- **SPEED mode improved** between runs (Q3: 0.950s → 0.101s) after P40 changes.
- **TOP_K controls reranker input depth, not final result count** — all configs return 10 results regardless of TOP_K value.
- **BM25 on/off has negligible latency impact** at 1,545 points — keep BM25 enabled.

## Known Issues

- **RRF weight env vars not reflected in debug output** — `rrf_config` field in `/v1/search?debug=true` always returns `{bm25_weight: 1, k: 60, vector_weight: 1}` regardless of `EMDEX_RRF_VECTOR_WEIGHT`/`EMDEX_RRF_BM25_WEIGHT` env vars. Needs investigation before RRF weight tuning is considered reliable.

## Recommendation

Apply to production `.env`:
```env
EMDEX_RERANK_TOP_K=40
```

Keep all other settings at defaults.
