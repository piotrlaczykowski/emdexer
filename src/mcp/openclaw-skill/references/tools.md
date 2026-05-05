# OpenClaw MCP Tool Reference

Full parameter schemas for all 10 tools exposed by the emdexer MCP server.
Load this reference when you need exact parameter names, types, or defaults.

---

### `search_semantic`

Semantic/vector search — finds conceptually similar files even without exact keyword
matches. Best for: "what is X?", "explain Y", natural language questions, paraphrases.
Use namespace='*' for global search across all authorized namespaces.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| query | str | — | Search query string |
| namespace | str | `"default"` | Namespace to search; use `*` for all authorized namespaces |

**Example request**
```json
{"name": "search_semantic", "arguments": {"query": "authentication flow", "namespace": "default"}}
```

**Example response (truncated)**
```
### Semantic results for **authentication flow** in `default`

| # | Path | Score | Preview |
|---|---|---|---|
| 1 | `src/gateway/auth/middleware.go` | 0.9312 | JWT validation and bearer token extraction... |
| 2 | `src/pkg/oidc/provider.go` | 0.8847 | OIDC provider configuration and token verif... |
```

---

### `search_keyword`

Keyword/BM25 search — finds files containing specific terms, identifiers, or exact
phrases. Best for: function names, error codes, config keys, exact strings, code symbols.
Results are reranked by cross-encoder when EMDEX_RERANK_ENABLED=true (recommended for precision).
Use namespace='*' for global search across all authorized namespaces.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| query | str | — | Search query string |
| namespace | str | `"default"` | Namespace to search; use `*` for all authorized namespaces |

**Example request**
```json
{"name": "search_keyword", "arguments": {"query": "EMDEX_AUTH_KEY", "namespace": "default"}}
```

**Example response (truncated)**
```
### Keyword results for **EMDEX_AUTH_KEY** in `default`

| # | Path | Score | Preview |
|---|---|---|---|
| 1 | `src/mcp/main.py` | 0.9871 | BEARER_TOKEN = os.getenv("EMDEX_AUTH_KEY", "")... |
| 2 | `.env.example` | 0.9410 | EMDEX_AUTH_KEY=your-secret-key-here... |
```

---

### `search_hybrid`

Hybrid search combining semantic and keyword matching via Reciprocal Rank Fusion.
Best for: general queries where both conceptual similarity and keyword presence matter.
Default choice when uncertain which mode fits.
Results are reranked by cross-encoder when EMDEX_RERANK_ENABLED=true (recommended for precision).
Use namespace='*' for global search across all authorized namespaces.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| query | str | — | Search query string |
| namespace | str | `"default"` | Namespace to search; use `*` for all authorized namespaces |

**Example request**
```json
{"name": "search_hybrid", "arguments": {"query": "plugin extractor PDF", "namespace": "default"}}
```

**Example response (truncated)**
```
### Hybrid results for **plugin extractor PDF** in `default`

| # | Path | Score | Preview |
|---|---|---|---|
| 1 | `src/plugin-sidecar/plugins/pdf_extractor.py` | 0.9123 | [PDF] Extracts text content from PDF files... |
| 2 | `src/plugin-sidecar/README.md` | 0.8654 | Plugin sidecar handles PDF, image, and audio... |
```

---

### `search_files`

Alias for search_hybrid — kept for backward compatibility with older clients.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| query | str | — | Search query string |
| namespace | str | `"default"` | Namespace to search; use `*` for all authorized namespaces |

**Example request**
```json
{"name": "search_files", "arguments": {"query": "docker compose gateway", "namespace": "default"}}
```

**Example response (truncated)**
```
### Hybrid results for **docker compose gateway** in `default`

| # | Path | Score | Preview |
|---|---|---|---|
| 1 | `deploy/docker/docker-compose.yml` | 0.9201 | gateway service configuration with port 7700... |
| 2 | `src/gateway/main.go` | 0.8732 | Gateway entrypoint, listens on configured port... |
```

---

### `search_graph`

Graph-augmented search — follows file relationships (imports, links) to find
structurally connected files. Best for: "what imports X?", "find all files related
to Y", dependency analysis, blast-radius questions.

Prefer search_semantic/search_keyword/search_hybrid for content-based queries.
depth controls BFS hop depth [1-3]. namespace='*' is not supported on this endpoint.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| query | str | — | Search query string |
| namespace | str | `"default"` | Namespace to search (namespace='*' not supported) |
| depth | int | `1` | BFS hop depth for graph traversal; clamped to [1-3] |

**Example request**
```json
{"name": "search_graph", "arguments": {"query": "search handler", "namespace": "default", "depth": 2}}
```

**Example response (truncated)**
```
### Graph search results for **search handler** in `default` (depth=2, 42ms)

| # | Path | Score | Preview |
|---|---|---|---|
| 1 | `src/gateway/search/handler.go` | 0.9441 | HTTP handler for /v1/search endpoint... |
| 2 | `src/gateway/search/reranker.go` | 0.8890 | Cross-encoder reranker integration... |

**Graph nodes explored:** 14
**Graph edges found:** 9
```

---

### `get_file`

Retrieve file content from EMDEX.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| path | str | — | Path to the file as indexed in EMDEX |

**Example request**
```json
{"name": "get_file", "arguments": {"path": "src/gateway/main.go"}}
```

**Example response (truncated)**
```
package main

import (
    "log"
    "os"
    "github.com/example/emdexer/src/gateway/server"
)
```

---

### `get_file_relations`

Return structurally related files for a given path using the Graph-RAG knowledge graph.
Searches for files that import or link to the target path and files that the target imports.
depth controls BFS hop depth (1-3). Use namespace='*' for global search.

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| path | str | — | Path to the file as indexed in EMDEX |
| namespace | str | `"default"` | Namespace to search; use `*` for all authorized namespaces |
| depth | int | `1` | BFS hop depth for relation traversal; range 1-3 |

**Example request**
```json
{"name": "get_file_relations", "arguments": {"path": "src/gateway/search/handler.go", "namespace": "default", "depth": 1}}
```

**Example response (truncated)**
```
### Relations for `src/gateway/search/handler.go` (namespace `default`)

**Imports / Links to** (3):
- `src/gateway/search/reranker.go`
- `src/pkg/qdrant/client.go`
- `src/gateway/auth/middleware.go`

**Defines** (2):
- `SearchHandler`
- `handleSearch`
```

---

### `list_plugins`

List all active extractor plugins registered on the node, including the file extensions they handle.

**Parameters**

No parameters.

**Example request**
```json
{"name": "list_plugins", "arguments": {}}
```

**Example response (truncated)**
```
### Active Extractor Plugins

| Plugin | Extensions | File |
|--------|-----------|------|
| PDF Extractor | `.pdf` | `pdf_extractor.py` |
| Image OCR | `.png, .jpg, .jpeg` | `image_ocr.py` |

Plugin directory: `./plugins`
```

---

### `system_status`

Display EMDEX gateway health and active nodes.

**Parameters**

No parameters.

**Example request**
```json
{"name": "system_status", "arguments": {}}
```

**Example response (truncated)**
```
### EMDEX System Status
**Gateway:** healthy
**Active Nodes:** 2

| Node ID | Namespaces | Protocol | Health |
|---|---|---|---|
| `node-a1b2` | default, prod | grpc | healthy |
| `node-c3d4` | default | grpc | healthy |
```

---

### `eval_ragas`

Run RAGAS evaluation against the specified namespace.
Returns context_recall and faithfulness scores.
Requires ragas-sidecar to be running (EMDEX_RAGAS_URL).

**Parameters**
| Name | Type | Default | Description |
|---|---|---|---|
| namespace | str | `"default"` | Namespace to evaluate against |
| questions_file | str | `"src/ragas-sidecar/fixtures/emdexer-eval-20q.json"` | Path to JSON file containing evaluation questions |
| ground_truth_file | str | `"src/ragas-sidecar/fixtures/emdexer-eval-20q.json"` | Path to JSON file containing ground truth answers |
| threshold | float | `0.75` | Minimum passing score for context_recall and faithfulness |

**Example request**
```json
{"name": "eval_ragas", "arguments": {"namespace": "default", "threshold": 0.80}}
```

**Example response (truncated)**
```json
{
  "context_recall": 0.83,
  "faithfulness": 0.81,
  "passed": true,
  "questions_evaluated": 20
}
```

---

## Versioning

This reference tracks the tool surface as of P50 (2026-05-05). When adding,
removing, or renaming a tool in `src/mcp/main.py`, update this file in the
same PR.
