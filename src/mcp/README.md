# Emdexer MCP Server

Exposes the Emdexer RAG system as Model Context Protocol (MCP) tools so Claude Desktop, OpenClaw, and other MCP clients can search, query, and evaluate the index without HTTP plumbing.

## Available tools

| Tool | Purpose | Key params |
|---|---|---|
| `search_semantic` | Vector similarity search | `query`, `namespace` |
| `search_keyword` | BM25 keyword search | `query`, `namespace` |
| `search_hybrid` | Vector + BM25 RRF fusion (default) | `query`, `namespace` |
| `search_files` | Locate files by path/name | `query`, `namespace` |
| `search_graph` | Graph-RAG BFS search | `query`, `namespace`, `depth` (1-3) |
| `get_file` | Fetch a single file's content | `path` |
| `get_file_relations` | Structural neighbours from the knowledge graph | `path`, `namespace`, `depth` |
| `list_plugins` | List installed connector plugins | — |
| `system_status` | Gateway + node health | — |
| `eval_ragas` | RAGAS eval (`context_recall`, `faithfulness`) — see P49 | `namespace`, `questions_file`, `ground_truth_file`, `threshold` |

Full schemas and example responses live in [`openclaw-skill/references/tools.md`](openclaw-skill/references/tools.md).

## Quick start

### Docker

```bash
docker compose -f deploy/docker/docker-compose.yml up -d gateway mcp
```

### Local

```bash
pip install -r requirements.txt
export EMDEX_URL=http://localhost:8080
export EMDEX_AUTH_KEY=changeme
python src/mcp/main.py --transport stdio
```

For SSE transport (OpenClaw, remote clients):

```bash
python src/mcp/main.py --transport sse --port 8002
```

## Required env vars

| Var | Purpose |
|---|---|
| `EMDEX_URL` | Gateway base URL (e.g. `http://localhost:8080`) |
| `EMDEX_AUTH_KEY` | Bearer token (must match the gateway's value) |
| `EMDEX_RAGAS_URL` | *(optional)* — RAGAS sidecar URL, only needed for `eval_ragas` |

## Claude Desktop config snippet

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "emdexer": {
      "command": "python",
      "args": ["/absolute/path/to/emdexer/src/mcp/main.py", "--transport", "stdio"],
      "env": {
        "EMDEX_URL": "http://localhost:8080",
        "EMDEX_AUTH_KEY": "changeme"
      }
    }
  }
}
```

## OpenClaw skill

The OpenClaw discovery skill lives at [`openclaw-skill/SKILL.md`](openclaw-skill/SKILL.md). Its `references/tools.md` is the authoritative tool reference.

## RAGAS eval (P49)

The `eval_ragas` MCP tool wraps `emdex eval --ragas` and returns `context_recall` and `faithfulness` scores from the `ragas-sidecar` service. Run from the CLI directly:

```bash
emdex eval --ragas \
  --file src/ragas-sidecar/fixtures/emdexer-eval-20q.json \
  --ground-truth src/ragas-sidecar/fixtures/emdexer-eval-20q.json \
  --namespace default \
  --threshold 0.75 \
  --output json
```

## Tests

```bash
pytest src/mcp/ -v
```
