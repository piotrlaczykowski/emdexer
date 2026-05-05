---
name: emdexer
description: >
  Search, query, and manage a self-hosted Emdexer RAG system over MCP.
  Use when the user asks to search files, documents, or codebases
  semantically or by keyword; retrieve graph-augmented context; fetch
  files or their structural relations; check indexing status, node
  health, or available namespaces; list installed connector plugins;
  or run quality eval (RAGAS context_recall and faithfulness scores).
  Supports modes semantic, keyword, hybrid (default), and graph.
  Requires GATEWAY_URL and EMDEX_AUTH_KEY env vars set on the MCP server.
---

# Emdexer Skill

Self-hosted RAG system with semantic, keyword, hybrid, and graph-augmented search exposed over MCP.

## Setup

1. Start the MCP server alongside the gateway:
   - Docker: `docker compose up -d mcp gateway`
   - Local: `python src/mcp/main.py --transport stdio`
2. Set env vars on the MCP process:
   - `GATEWAY_URL` — gateway base URL (e.g. `http://localhost:8080`, default: `http://gateway:7700`)
   - `EMDEX_AUTH_KEY` — Bearer token (must match the gateway's `EMDEX_AUTH_KEY`)
   - `EMDEX_RAGAS_URL` *(optional)* — only needed for `eval_ragas`
3. Connect a client:
   - **Claude Desktop:** add to `claude_desktop_config.json` under `mcpServers`
   - **OpenClaw:** point at the MCP HTTP/SSE endpoint (`--transport sse --port 8002`)

## When to use this skill

Trigger this skill when the user asks to:
- search code / docs / files semantically, by keyword, or hybrid (default)
- traverse the knowledge graph from a file (graph search, related files)
- fetch a single file by path
- check gateway health, active nodes, or namespaces
- list installed connector plugins
- run a RAGAS evaluation against an indexed namespace

## Search modes

Hybrid is the default — it fuses vector and BM25 with Reciprocal Rank Fusion. Pick a non-default mode only when the user asks for it explicitly or the query shape demands it.

| Mode | Tool | When |
|---|---|---|
| Hybrid | `search_hybrid` | Default — most queries |
| Semantic | `search_semantic` | Conceptual / synonym-heavy queries |
| Keyword | `search_keyword` | Exact identifiers, error strings, code symbols |
| Graph | `search_graph` | "Find related files / what calls X" — set `depth` 1-3 |
| Files-only | `search_files` | Locate by path or filename |

All search tools accept `query` (string), `namespace` (string, default `"default"`), and may accept extras like `depth`. Use `namespace="*"` for cross-namespace searches.

## Other tools

- `get_file(path)` — fetch a single file's content
- `get_file_relations(path, namespace, depth)` — structural neighbours from the Graph-RAG knowledge graph
- `list_plugins()` — installed connector plugins
- `system_status()` — gateway health, active nodes, namespaces, reranker status
- `eval_ragas(namespace, questions_file, ground_truth_file, threshold)` — RAGAS context_recall + faithfulness against an indexed namespace; requires `EMDEX_RAGAS_URL` and the `ragas-sidecar` service running

## Rate limits

Cap search calls at ~10/minute per namespace to avoid Qdrant overload during interactive sessions.

## MCP tool reference

See [references/tools.md](references/tools.md) for full tool schemas, all parameters with types and defaults, example requests, and example responses.
