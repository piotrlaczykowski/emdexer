# Monitoring

## Gateway Metrics

The gateway exposes Prometheus metrics at `http://<gateway-host>:9090/metrics`.

Add to your Prometheus config:

```yaml
- job_name: "emdexer-gateway"
  static_configs:
    - targets: ["<gateway-host>:9090"]
  metrics_path: /metrics
```

## Node Metrics

Each Emdexer node exposes metrics on port 8081 at `/metrics`.

Add to your Prometheus config:

```yaml
- job_name: "emdexer-node"
  static_configs:
    - targets: ["<node-host>:8081"]
  metrics_path: /metrics
```

Key node metrics:

| Metric | Description |
|--------|-------------|
| `emdexer_gateway_embed_duration_ms` | Embedding API latency histogram |
| `emdexer_gateway_embed_errors_total` | Embedding API error count |
| `emdexer_node_audio_skipped_total` | Audio files skipped (no FFmpeg sidecar) |
| `emdexer_node_vision_duration_ms` | Vision extraction latency |

## Indexing Metrics (gateway-side)

When a node completes a walk and POSTs to `/v1/nodes/{id}/indexed`, the gateway
records the following counters:

| Metric | Labels | Description |
|--------|--------|-------------|
| `emdexer_gateway_node_files_indexed_total` | `namespace`, `node_id` | Cumulative files indexed |
| `emdexer_gateway_node_files_skipped_total` | `namespace`, `node_id` | Cumulative files skipped |
| `emdexer_gateway_node_indexing_complete_total` | `namespace`, `node_id`, `status` | Walk completions |
| `emdexer_gateway_node_last_files_indexed` | `namespace`, `node_id` | Files in most recent walk (gauge) |

## Grafana Dashboards

Pre-built dashboards are in `deploy/monitoring/grafana/dashboards/`:

| Dashboard | Description |
|-----------|-------------|
| `emdexer-overview.json` | Topology, search, indexing summary |
| `emdexer-search.json` | Search latency, BM25, reranking |
| `emdexer-rag.json` | RAG pipeline, graph-RAG, eval quality |
