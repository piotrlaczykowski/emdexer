# Graph-RAG (Phase 24)

Graph-RAG enriches search results with **structurally related documents** — not just semantically similar ones. Instead of relying solely on vector similarity or keyword overlap, the gateway builds a lightweight in-memory knowledge graph from import and link relations extracted during indexing, then expands each query with chunks from adjacent files.

---

## How it works

### 1. Relation extraction (Node side)

When the node indexes a file, the pipeline extracts structural relations from the full file text and stores them in the Qdrant point payload under the `relations` key — **chunk 0 only**.

Supported relation types by file type:

| Extension | `imports` | `links_to` | `defines` |
|-----------|-----------|------------|-----------|
| `.go` | `import "pkg/path"` | — | Exported `func`/`type` names |
| `.py` | `import x`, `from x import` | — | `def` / `class` names |
| `.js` `.ts` | `from 'x'`, `require('x')` | — | Exported `function`/`class`/`const` |
| `.c` `.cpp` `.h` | `#include <x>` | — | — |
| `.md` `.rst` | — | `[text](relative/path)` | — |

**Payload format** (stored as a JSON string):

```json
[
  {"type": "imports", "target": "src/pkg/auth/middleware.go"},
  {"type": "defines", "name": "ValidateJWT"},
  {"type": "links_to", "target": "../getting-started.md"}
]
```

Only `imports` and `links_to` relations create graph edges. `defines` is stored for future cross-file identifier lookup.

### 2. In-memory knowledge graph (Gateway side)

`src/pkg/graph/graph.go` implements a lazy, cached, namespace-scoped directed graph:

- **`BuildGraph(ctx, qdrantClient, collection, namespace)`** — scrolls all chunk-0 points in the namespace, parses `relations` payloads, and builds an adjacency map: `file → []related_files`. Only edges where `type` is `imports` or `links_to` are followed.
- **`Neighbors(ctx, client, collection, namespace, file, depth)`** — returns files reachable within `depth` BFS hops. Depth is clamped to `[1, 3]`.
- The graph is **cached per namespace** with a **5-minute TTL**. Cache misses trigger a Qdrant scroll; the operation is non-blocking and failures silently skip expansion.

### 3. Graph-augmented retrieval (Gateway side)

After the initial `HybridSearch` returns results, the gateway:

1. Collects unique source file paths from the result payloads.
2. Calls `Neighbors(file, depth=EMDEX_GRAPH_DEPTH)` for each source file.
3. Issues a follow-up `HybridSearchByPaths` restricted to the neighbour files.
4. Merges neighbour results with the original results using `MergeRRFWeighted`:
   - Primary (initial) results use RRF weight **1.0**.
   - Neighbour results use RRF weight **0.7** so direct matches always rank higher.

Graph expansion is applied **twice**:
- Once to the initial search results in `handleChatCompletions`.
- Once inside the agentic loop's `searchFn`, so every follow-up hop also expands neighbours.

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_GRAPH_ENABLED` | `true` | Set to `false` to disable graph expansion. |
| `EMDEX_GRAPH_DEPTH` | `1` | BFS hop depth `[1–3]`. Depth 1 = direct imports/links. |
| `EMDEX_GRAPH_MIGRATION` | `auto` | Migration mode: `auto`, `skip`, or `force` (node only). |

Add to your `.env`:

```bash
EMDEX_GRAPH_ENABLED=true
EMDEX_GRAPH_DEPTH=1
EMDEX_GRAPH_MIGRATION=auto
```

---

## Prometheus metrics

| Metric | Type | Description |
|--------|------|-------------|
| `emdexer_gateway_graph_expansions_total` | Counter | Graph expansions triggered after initial search |
| `emdexer_gateway_graph_neighbors_found` | Histogram | Neighbour files found per expansion |
| `emdexer_gateway_graph_cache_hits_total` | Counter | Graph served from cache without Qdrant scroll |

---

## Alert rules

Two alert rules are defined in `deploy/monitoring/prometheus/alerts-search.yml`:

| Alert | Condition | Severity |
|-------|-----------|----------|
| `GraphExpansionNoNeighbors` | Expansions triggered but avg neighbours == 0 for 10 min | info |
| `GraphCacheMissRate` | Cache hit rate < 50% for 5 min | info |

`GraphExpansionNoNeighbors` typically fires when the collection was indexed before Phase 24 was deployed and no `relations` fields exist yet. The node will detect this automatically on startup and trigger a full re-index (see below).

---

## Automatic migration from pre-Phase-24 collections

Phase 24 adds a new `relations` payload field to chunk-0 points. Existing points in Qdrant do **not** have this field; graph expansion silently returns 0 neighbours for them.

On every node startup, the node samples up to 50 chunk-0 points from its namespace. If fewer than 20% of them carry a non-empty `relations` field, a full re-index is triggered automatically:

- **Poller-based nodes** (SMB, SFTP, NFS, S3): the metadata cache (`emdex_cache.db`) is deleted so the first poll treats every file as new and re-indexes it with relation extraction.
- **Local nodes**: the startup walk re-processes all files unconditionally.

Control this behaviour with `EMDEX_GRAPH_MIGRATION`:

| Value | Behaviour |
|-------|-----------|
| `auto` (default) | Detect and trigger re-index if `relations` coverage < 20% |
| `skip` | Never auto-migrate; manual re-index only |
| `force` | Always trigger a full re-index on startup |

A Prometheus counter `emdexer_node_graph_migration_triggered_total` increments each time an automatic migration is started.

### Manual re-index (override)

To force a re-index without restarting (e.g. after `EMDEX_GRAPH_MIGRATION=skip`):

```bash
# Delete the metadata cache so all files are re-processed on next poll
rm -f "${EMDEX_CACHE_DIR:-cache}/emdex_cache.db"

# Restart the node
systemctl restart emdex-node
# or: docker compose restart node
```

---

## Architecture diagram

```
  ┌─────────── Node ────────────┐      ┌──────── Qdrant ─────────────┐
  │                             │      │                             │
  │  Walk file ──► Extract text │      │  point {                    │
  │       │                     │ ──►  │    path: "src/auth.go"      │
  │  ExtractRelations(path,text)│      │    chunk: 0                 │
  │       │                     │      │    relations: "[{...}]"     │
  │  Embed chunk 0…N            │      │    text: "..."              │
  │       │                     │      │  }                          │
  └───────┴─────────────────────┘      └─────────────────────────────┘
                                                      │
  ┌─────────── Gateway ─────────────────────────────┐ │
  │                                                 │ │
  │  POST /v1/chat/completions                      │ │
  │    │                                            │ │
  │    ├─► HybridSearch ──────────────────────────►─┘ │
  │    │       │ results                               │
  │    ├─► graphExpandResults                         │
  │    │       ├─► Graph.Neighbors (BFS, depth=1)      │
  │    │       │       cached / BuildGraph ──────────►─┘
  │    │       └─► HybridSearchByPaths (neighbour files)
  │    │               │ neighbour results             │
  │    │       MergeRRFWeighted(1.0 / 0.7)             │
  │    │               │ merged results                │
  │    ├─► RunAgenticLoop (searchFn also graph-expanded)
  │    └─► BuildContext ──► CallGemini ──► response    │
  └─────────────────────────────────────────────────┘
```

---

## Limitations

- **Regex-based extraction**: Relations are extracted with simple regexes, not a full AST parser. Aliases, dynamic imports, and generated code may be missed or produce noise.
- **Path normalisation**: Import paths are stored as-is. If a Go import `"github.com/user/repo/pkg"` and the indexed file path `src/pkg/file.go` do not share the same string, no edge is created. This is intentional for the initial implementation.
- **Depth cap**: Maximum BFS depth is 3 to prevent expensive expansions on highly connected graphs.
- **Single namespace**: Graph expansion is only applied to single-namespace requests. Global fan-out (`namespace=*`) does not trigger graph expansion.
