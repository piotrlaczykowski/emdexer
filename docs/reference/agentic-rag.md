# Agentic Multi-Hop RAG

Emdexer's agentic RAG mode extends the standard single-hop chat completions with an iterative retrieval loop. When a single search pass does not yield sufficient context, the gateway automatically runs follow-up queries guided by an LLM confidence assessment, accumulating richer context before generating the final answer.

---

## How It Works

```
User question
     │
     ▼
Hop 1: HybridSearch (vector + BM25)
     │
     ▼
LLM Hop Assessment ──── confidence ≥ threshold? ──── YES ──▶ Final synthesis
     │                                                              │
     │ NO                                                           ▼
     ▼                                                        Stream / JSON response
Generate follow-up queries
     │
     ▼
Hop 2: Embed + HybridSearch each follow-up query
     │
     ▼
Merge results (score-based dedup, top-20 kept)
     │
     └──── repeat until confidence threshold met OR MaxHops reached
```

### Hop Assessment

After each retrieval pass the gateway calls Gemini in **JSON mode** with a structured prompt asking:

- `confidence` (0.0–1.0) — how completely can the question be answered from the current context
- `answer_ready` (bool) — whether the LLM considers the context sufficient
- `follow_up_queries` — 1–3 targeted search queries to fill knowledge gaps
- `reasoning` — one-sentence explanation (logged for observability)

The loop stops early when `answer_ready = true` **or** `confidence ≥ EMDEX_HOP_CONFIDENCE_THRESHOLD`.

### Result Merging

Across hops, results are merged by Qdrant point ID:
- Duplicate IDs retain the higher score.
- The merged set is sorted by score descending and trimmed to the top 20 results.
- The final context string is built from this merged set before the synthesis call.

---

## Configuration

All variables are gateway-side. Set in `/etc/emdexer/gateway.env` (systemd) or `.env` (Docker Compose).

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_AGENTIC_ENABLED` | `true` | Set to `false` to revert to single-hop RAG |
| `EMDEX_MAX_HOPS` | `3` | Maximum retrieval hops (1–5). Hop 1 is always the initial search. |
| `EMDEX_HOP_CONFIDENCE_THRESHOLD` | `0.7` | LLM confidence score at which the loop stops early |

> **Note:** `EMDEX_MAX_HOPS` is capped at 5 regardless of the configured value.

---

## Scope Restrictions

- **Single-namespace only.** Agentic hops are disabled for global (`namespace=*`) requests, which use parallel fan-out across all namespaces instead. All follow-up queries use the same namespace as the original request.
- **Agentic RAG requires a Google API key.** Hop assessment uses `gemini-2.0-flash` in JSON mode.

---

## Observability

### Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `emdexer_gateway_agentic_hops_total` | Counter | `namespace` | Additional hops performed beyond hop 1 |
| `emdexer_gateway_agentic_confidence_score` | Histogram | `namespace` | LLM confidence score observed at each hop |
| `emdexer_gateway_agentic_early_stop_total` | Counter | `namespace` | Loops that stopped early due to sufficient confidence |

### Audit Log

Each hop beyond hop 1 emits an audit entry with `action: "agentic_hop"`:

```json
{
  "action": "agentic_hop",
  "query": "original user question",
  "namespace": "my-datasource",
  "results": 4,
  "metadata": {
    "hop": 2,
    "confidence": 0.45,
    "follow_up_count": 2,
    "reasoning": "context covers the main topic but lacks version-specific details"
  }
}
```

### Example PromQL

```promql
# Average additional hops per minute
rate(emdexer_gateway_agentic_hops_total[5m])

# Early-stop ratio (loops that needed < MaxHops)
rate(emdexer_gateway_agentic_early_stop_total[5m])
  / rate(emdexer_gateway_agentic_hops_total[5m])

# P90 confidence score
histogram_quantile(0.9, rate(emdexer_gateway_agentic_confidence_score_bucket[5m]))
```

---

## Tuning

| Goal | Recommended change |
|------|--------------------|
| Reduce latency (fewer API calls) | Lower `EMDEX_MAX_HOPS` to `2` or disable with `EMDEX_AGENTIC_ENABLED=false` |
| More thorough answers | Raise `EMDEX_HOP_CONFIDENCE_THRESHOLD` to `0.85` |
| Faster early stops | Lower `EMDEX_HOP_CONFIDENCE_THRESHOLD` to `0.5` |
| Cap LLM costs | Set `EMDEX_MAX_HOPS=2` |

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Chat latency doubles | All requests reaching MaxHops | Lower threshold or reduce `EMDEX_MAX_HOPS` |
| Agentic hops counter stays at 0 | `EMDEX_AGENTIC_ENABLED=false` or global namespace | Check env var; agentic is disabled for `namespace=*` |
| `[agentic] hop N assessment error` in logs | Gemini API failure | Check `GOOGLE_API_KEY`; error falls back to accumulated results |
| Follow-up searches returning same results | Queries too similar to hop 1 | Raise `EMDEX_HOP_CONFIDENCE_THRESHOLD` so loop stops sooner |
