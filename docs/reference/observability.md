# Observability

Emdexer ships three complementary observability pillars: **metrics** (Prometheus), **audit logs** (structured JSON), and **distributed tracing** (OpenTelemetry).

---

## Metrics

The gateway and node expose a Prometheus `/metrics` endpoint on their respective ports.

| Metric | Labels | Description |
|--------|--------|-------------|
| `emdexer_gateway_search_vector_duration_ms` | `collection`, `namespace` | Vector search latency (ms) |
| `emdexer_gateway_search_bm25_duration_ms` | `collection`, `namespace` | BM25 keyword search latency (ms) |
| `emdexer_gateway_search_hybrid_total_ms` | `collection`, `namespace` | End-to-end hybrid search latency (ms) |
| `emdexer_gateway_embed_duration_ms` | `provider`, `model` | Embedding API call latency (ms) |
| `emdexer_gateway_llm_duration_ms` | `model` | LLM generation call latency (ms) |
| `emdexer_gateway_agentic_hops_total` | `namespace` | Total additional agentic hops performed |
| `emdexer_gateway_agentic_confidence_score` | `namespace` | LLM-assessed confidence per hop |
| `emdexer_gateway_rrf_top_vector_hits_total` | `collection`, `namespace` | Results from vector leg only |
| `emdexer_gateway_rrf_top_bm25_hits_total` | `collection`, `namespace` | Results from BM25 leg only |

---

## Audit Logs

Every search and chat request is written to `logs/audit.json` (configurable via `EMDEX_AUDIT_LOG_FILE`). Each line is a JSON object:

```json
{
  "timestamp": "2026-03-23T15:04:05Z",
  "action": "chat",
  "user": "user@example.com",
  "query": "how does chunking work?",
  "namespace": "default",
  "results": 5,
  "latency_ms": 342,
  "status": 200
}
```

---

## Distributed Tracing (Phase 27)

Emdexer implements **OpenTelemetry distributed tracing** with a backend-agnostic OTLP/gRPC exporter. It is compatible with Jaeger, Grafana Tempo, Honeycomb, and any OTLP-capable collector.

Tracing is **opt-in**: leave `EMDEX_OTEL_ENDPOINT` unset and the tracer is a no-op with zero runtime overhead.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_OTEL_ENDPOINT` | _(unset = disabled)_ | OTLP/gRPC collector address, e.g. `otel-collector:4317` |
| `EMDEX_OTEL_SERVICE_NAME` | `emdex-gateway` | `service.name` resource attribute |
| `EMDEX_OTEL_SAMPLING_RATIO` | `1.0` | Head-based sampling ratio `[0.0 – 1.0]` |

### Span Hierarchy

A single chat completion request produces the following span tree:

```
emdex.chat                       ← root span; W3C traceparent extracted from HTTP headers
  emdex.embed                    ← question embedding
  emdex.search.hybrid            ← or emdex.search.vector when BM25 is off
    emdex.search.vector
    emdex.search.bm25
  emdex.graph.expand             ← Graph-RAG neighbour fetch (when enabled)
  emdex.agentic.loop             ← multi-hop RAG loop (when enabled)
    emdex.agentic.hop            ← one span per hop
      emdex.llm.structured       ← hop assessment call
      emdex.embed                ← follow-up query embedding
      emdex.search.hybrid
  emdex.llm.generate             ← final answer generation
```

A search request (`GET /v1/search`) produces a shallower tree rooted at `emdex.search`.

### W3C Trace Context Propagation

The gateway reads `traceparent` and `tracestate` headers from incoming HTTP requests. Upstream callers (API gateways, load balancers, or other services) can inject a trace context to correlate emdexer spans within a larger distributed trace.

### Quick Start — Docker Compose

Add an OTLP collector to your `docker-compose.yml` and uncomment the gateway env vars:

```yaml
services:
  gateway:
    environment:
      - EMDEX_OTEL_ENDPOINT=otel-collector:4317
      - EMDEX_OTEL_SAMPLING_RATIO=1.0

  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    volumes:
      - ./otel-collector-config.yaml:/etc/otelcol-contrib/config.yaml
    ports:
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP
```

### Quick Start — Jaeger (all-in-one)

```yaml
  jaeger:
    image: jaegertracing/all-in-one:latest
    ports:
      - "16686:16686"  # Jaeger UI
      - "4317:4317"    # OTLP gRPC ingest
```

Set `EMDEX_OTEL_ENDPOINT=jaeger:4317` in the gateway service.

### Quick Start — Grafana Tempo

```yaml
  tempo:
    image: grafana/tempo:latest
    command: ["-config.file=/etc/tempo.yaml"]
    ports:
      - "4317:4317"    # OTLP gRPC
      - "3200:3200"    # Tempo query API
```

Set `EMDEX_OTEL_ENDPOINT=tempo:4317`.

---

## Alert Rules

Prometheus alert rules for search latency, BM25 health, and agentic loop behaviour are defined in `deploy/monitoring/prometheus/alerts-search.yml`.
