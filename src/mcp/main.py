import os
import requests
from fastmcp import FastMCP, Context
from prefab_ui.app import PrefabApp
from prefab_ui.components import DataTable, DataTableColumn, Text, Dashboard, DashboardItem

# Configuration
GATEWAY_URL = os.getenv("GATEWAY_URL", "http://gateway:7700")
# Supports both static API keys (EMDEX_AUTH_KEY) and OIDC JWT tokens (EMDEX_BEARER_TOKEN).
# EMDEX_BEARER_TOKEN takes precedence if set — use it when the gateway is OIDC-protected.
BEARER_TOKEN = os.getenv("EMDEX_BEARER_TOKEN", "") or os.getenv("EMDEX_AUTH_KEY", "")
if not BEARER_TOKEN:
    raise RuntimeError("EMDEX_AUTH_KEY or EMDEX_BEARER_TOKEN environment variable is required.")

# GUI_CLIENTS: client_id substrings that support PrefabApp rendering.
# All other clients (OpenClaw, mcporter, curl, etc.) receive plain markdown.
GUI_CLIENTS = {"claude-desktop", "claude", "anthropic"}

mcp = FastMCP("emdexer")

def get_headers():
    return {"Authorization": f"Bearer {BEARER_TOKEN}"}

def is_gui(ctx: Context) -> bool:
    """Return True if the connected client supports PrefabApp GUI rendering."""
    if ctx is None:
        return False
    client_id = (ctx.client_id or "").lower()
    # Only render GUI for known Claude Desktop client IDs.
    # Unknown/empty client_id → safe default is plain text.
    if not client_id:
        return False
    return any(name in client_id for name in GUI_CLIENTS)

# Map file extensions to media type tags.
IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp"}
AUDIO_VIDEO_EXTS = {".mp3", ".wav", ".mp4", ".mkv", ".m4a", ".ogg", ".flac", ".webm"}

def media_tag(path: str) -> str:
    ext = os.path.splitext(path)[1].lower()
    if ext in IMAGE_EXTS:
        return "[Image] "
    if ext in AUDIO_VIDEO_EXTS:
        return "[Video] " if ext in {".mp4", ".mkv", ".webm"} else "[Audio] "
    if ext == ".pdf":
        return "[PDF] "
    return ""


def _render_results(results, title, namespace, ctx, extra_lines=None):
    """Shared renderer for search tools. Returns PrefabApp for GUI clients, markdown otherwise.

    Args:
        results: list of {"score": float, "payload": {"path": str, "text": str}} dicts.
        title: human-readable title for the markdown header.
        namespace: shown in the empty-results message and header.
        ctx: FastMCP Context (may be None).
        extra_lines: optional list of extra markdown lines appended after the table.
    """
    if not results:
        msg = f"No results found for **{title}** in namespace `{namespace}`."
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    table_data = []
    for r in results:
        payload = r.get("payload", {})
        path = payload.get("path", "N/A")
        tag = media_tag(path)
        preview = payload.get("text", "")[:100] + "..." if payload.get("text") else ""
        table_data.append({
            "Path": path,
            "Score": round(float(r.get("score", 0)), 4),
            "Preview": tag + preview,
        })

    if is_gui(ctx):
        return PrefabApp(
            children=[
                DataTable(
                    columns=[
                        DataTableColumn(key="Path", header="Path"),
                        DataTableColumn(key="Score", header="Score"),
                        DataTableColumn(key="Preview", header="Preview"),
                    ],
                    rows=table_data,
                )
            ]
        )

    lines = [f"### {title} in `{namespace}`\n"]
    lines.append("| # | Path | Score | Preview |")
    lines.append("|---|---|---|---|")
    for i, row in enumerate(table_data, 1):
        lines.append(f"| {i} | `{row['Path']}` | {row['Score']} | {row['Preview']} |")
    if extra_lines:
        lines.extend(extra_lines)
    return "\n".join(lines)


def _search_call(query: str, namespace: str, mode: str, ctx, title: str):
    """HTTP GET /v1/search?mode=... and render via _render_results."""
    url = f"{GATEWAY_URL}/v1/search"
    params = {"q": query, "namespace": namespace, "mode": mode}
    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        results = resp.json().get("results", [])
    except Exception as e:
        msg = f"Error searching EMDEX ({mode}): {str(e)}"
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg
    return _render_results(results, title=title, namespace=namespace, ctx=ctx)


@mcp.tool()
def search_semantic(query: str, namespace: str = "default", ctx: Context = None) -> str | PrefabApp:
    """Semantic/vector search — finds conceptually similar files even without exact keyword
    matches. Best for: "what is X?", "explain Y", natural language questions, paraphrases.
    Use namespace='*' for global search across all authorized namespaces."""
    return _search_call(query, namespace, mode="semantic", ctx=ctx, title=f"Semantic results for **{query}**")


@mcp.tool()
def search_keyword(query: str, namespace: str = "default", ctx: Context = None) -> str | PrefabApp:
    """Keyword/BM25 search — finds files containing specific terms, identifiers, or exact
    phrases. Best for: function names, error codes, config keys, exact strings, code symbols.
    Use namespace='*' for global search across all authorized namespaces."""
    return _search_call(query, namespace, mode="keyword", ctx=ctx, title=f"Keyword results for **{query}**")


@mcp.tool()
def search_hybrid(query: str, namespace: str = "default", ctx: Context = None) -> str | PrefabApp:
    """Hybrid search combining semantic and keyword matching via Reciprocal Rank Fusion.
    Best for: general queries where both conceptual similarity and keyword presence matter.
    Default choice when uncertain which mode fits.
    Use namespace='*' for global search across all authorized namespaces."""
    return _search_call(query, namespace, mode="hybrid", ctx=ctx, title=f"Hybrid results for **{query}**")


@mcp.tool()
def search_files(query: str, namespace: str = "default", ctx: Context = None) -> str | PrefabApp:
    """Alias for search_hybrid — kept for backward compatibility with older clients."""
    return search_hybrid(query, namespace, ctx)


@mcp.tool()
def search_graph(query: str, namespace: str = "default", depth: int = 1, ctx: Context = None) -> str | PrefabApp:
    """Search files using graph-augmented RAG. Returns results enriched with knowledge-graph
    nodes and edges showing structural relationships between files.
    depth controls BFS hop depth [1-3]. namespace='*' is not supported on this endpoint."""
    url = f"{GATEWAY_URL}/v1/search/graph"
    params = {"q": query, "namespace": namespace, "depth": max(1, min(3, depth))}

    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=15)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])
        graph_nodes = data.get("graph_nodes", [])
        graph_edges = data.get("graph_edges", [])
        query_time_ms = data.get("query_time_ms", 0)
    except Exception as e:
        msg = f"Error in graph search: {str(e)}"
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    if not results:
        msg = f"No results found for **{query}** in namespace `{namespace}` (depth={depth})."
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    table_data = []
    for r in results:
        payload = r.get("payload", {})
        path = payload.get("path", "N/A")
        tag = media_tag(path)
        preview = payload.get("text", "")[:100] + "..." if payload.get("text") else ""
        table_data.append({
            "Path": path,
            "Score": round(float(r.get("score", 0)), 4),
            "Preview": tag + preview,
        })

    if is_gui(ctx):
        return PrefabApp(
            children=[
                DataTable(
                    columns=[
                        DataTableColumn(key="Path", header="Path"),
                        DataTableColumn(key="Score", header="Score"),
                        DataTableColumn(key="Preview", header="Preview"),
                    ],
                    rows=table_data,
                )
            ]
        )

    lines = [f"### Graph search results for **{query}** in `{namespace}` (depth={depth}, {query_time_ms}ms)\n"]
    lines.append("| # | Path | Score | Preview |")
    lines.append("|---|---|---|---|")
    for i, row in enumerate(table_data, 1):
        lines.append(f"| {i} | `{row['Path']}` | {row['Score']} | {row['Preview']} |")
    if graph_nodes:
        lines.append(f"\n**Graph nodes explored:** {len(graph_nodes)}")
    if graph_edges:
        lines.append(f"**Graph edges found:** {len(graph_edges)}")
    return "\n".join(lines)


@mcp.tool()
def get_file(path: str, ctx: Context = None) -> str | PrefabApp:
    """Retrieve file content from EMDEX."""
    url = f"{GATEWAY_URL}/v1/search"
    params = {"q": f"file path: {path}", "limit": 1}

    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])
        content = results[0].get("payload", {}).get("text", "No content found.") if results else f"File `{path}` not found in index."
    except Exception as e:
        content = f"Error fetching file: {str(e)}"

    return PrefabApp(children=[Text(content=content)]) if is_gui(ctx) else content


@mcp.tool()
def get_file_relations(path: str, namespace: str = "default", depth: int = 1, ctx: Context = None) -> str | PrefabApp:
    """Return structurally related files for a given path using the Graph-RAG knowledge graph.
    Searches for files that import or link to the target path and files that the target imports.
    depth controls BFS hop depth (1-3). Use namespace='*' for global search."""
    url = f"{GATEWAY_URL}/v1/search"
    # Search for the file by path to get its chunk-0 payload (which contains relations)
    params = {"q": f"path:{path}", "namespace": namespace, "limit": 1}

    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])
    except Exception as e:
        msg = f"Error fetching relations for `{path}`: {str(e)}"
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    # Extract the relations field from the payload of the matched chunk-0 point
    import json as _json

    relations = []
    for r in results:
        payload = r.get("payload", {})
        if payload.get("path") != path:
            continue
        raw = payload.get("relations", "")
        if raw:
            try:
                relations = _json.loads(raw)
            except Exception:
                pass
        break

    if not relations:
        msg = f"No structural relations found for `{path}` in namespace `{namespace}`.\n" \
              "This file may not have been indexed with Phase 24 Graph-RAG enabled, " \
              "or it has no import/link statements."
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    imports = [r["target"] for r in relations if r.get("type") in ("imports", "links_to") and r.get("target")]
    defines = [r["name"] for r in relations if r.get("type") == "defines" and r.get("name")]

    if is_gui(ctx):
        rows = [{"Type": r.get("type", ""), "Target/Name": r.get("target") or r.get("name", "")} for r in relations]
        return PrefabApp(
            children=[
                DataTable(
                    columns=[
                        DataTableColumn(key="Type", header="Relation Type"),
                        DataTableColumn(key="Target/Name", header="Target / Name"),
                    ],
                    rows=rows,
                )
            ]
        )

    lines = [f"### Relations for `{path}` (namespace `{namespace}`)"]
    if imports:
        lines.append(f"\n**Imports / Links to** ({len(imports)}):")
        for t in imports:
            lines.append(f"- `{t}`")
    if defines:
        lines.append(f"\n**Defines** ({len(defines)}):")
        for d in defines:
            lines.append(f"- `{d}`")
    return "\n".join(lines)


@mcp.tool()
def list_plugins(ctx: Context = None) -> str | PrefabApp:
    """List all active extractor plugins registered on the node, including the file extensions they handle."""
    plugin_enabled = os.getenv("EMDEX_PLUGIN_ENABLED", "true").lower()

    if plugin_enabled == "false":
        msg = "Plugin system is disabled (EMDEX_PLUGIN_ENABLED=false)."
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    sidecar_url = os.getenv("EMDEX_PLUGIN_SIDECAR_URL", "")
    plugins = []

    if sidecar_url:
        # Sidecar mode: query GET /plugins on the plugin-sidecar HTTP service.
        base_url = sidecar_url.rstrip("/extract").rstrip("/")
        try:
            resp = requests.get(f"{base_url}/plugins", timeout=5)
            resp.raise_for_status()
            for entry in resp.json():
                plugins.append({
                    "file": "(sidecar)",
                    "name": entry.get("name", ""),
                    "extensions": ", ".join(entry.get("extensions", [])),
                })
        except Exception as e:
            msg = f"Error querying plugin sidecar at `{base_url}`: {str(e)}"
            return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg
    else:
        # Subprocess mode: scan plugin directory for *.py metadata.
        plugin_dir = os.getenv("EMDEX_PLUGIN_DIR", "./plugins")
        if not os.path.isdir(plugin_dir):
            msg = f"Plugin directory not found: `{plugin_dir}`"
            return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

        for fname in sorted(os.listdir(plugin_dir)):
            if not fname.endswith(".py"):
                continue
            fpath = os.path.join(plugin_dir, fname)
            name, exts = "", []
            try:
                with open(fpath, encoding="utf-8", errors="replace") as f:
                    for i, line in enumerate(f):
                        if i >= 30:
                            break
                        line = line.strip()
                        if not line.startswith("#"):
                            continue
                        if line.startswith("# name:"):
                            name = line[len("# name:"):].strip()
                        elif line.startswith("# extensions:"):
                            exts = [e.strip() for e in line[len("# extensions:"):].split(",") if e.strip()]
            except OSError:
                continue
            if name and exts:
                plugins.append({"file": fname, "name": name, "extensions": ", ".join(exts)})

    if not plugins:
        msg = f"No valid plugins found in `{plugin_dir}`."
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    if is_gui(ctx):
        return PrefabApp(
            children=[
                DataTable(
                    columns=[
                        DataTableColumn(key="name", label="Plugin"),
                        DataTableColumn(key="extensions", label="Extensions"),
                        DataTableColumn(key="file", label="File"),
                    ],
                    data=plugins,
                )
            ]
        )

    lines = ["### Active Extractor Plugins", "", "| Plugin | Extensions | File |", "|--------|-----------|------|"]
    for p in plugins:
        lines.append(f"| {p['name']} | `{p['extensions']}` | `{p['file']}` |")
    lines.append(f"\nPlugin directory: `{plugin_dir}`")
    return "\n".join(lines)


@mcp.tool()
def system_status(ctx: Context = None) -> str | PrefabApp:
    """Display EMDEX gateway health and active nodes."""
    try:
        resp = requests.get(f"{GATEWAY_URL}/health", timeout=5)
        health = resp.json()
        resp_nodes = requests.get(f"{GATEWAY_URL}/nodes", headers=get_headers(), timeout=5)
        nodes = resp_nodes.json()
    except Exception as e:
        msg = f"Error fetching status: {str(e)}"
        return PrefabApp(children=[Text(content=msg)]) if is_gui(ctx) else msg

    if is_gui(ctx):
        return PrefabApp(
            children=[
                Dashboard(
                    children=[
                        DashboardItem(
                            title="Status",
                            children=[
                                Text(content=f"**Gateway:** {health.get('status', 'Unknown')}\n**Active Nodes:** {len(nodes)}")
                            ],
                        ),
                    ]
                )
            ]
        )

    lines = [
        "### EMDEX System Status",
        f"**Gateway:** {health.get('status', 'Unknown')}",
        f"**Active Nodes:** {len(nodes)}",
        "",
        "| Node ID | Namespaces | Protocol | Health |",
        "|---|---|---|---|",
    ]
    for n in nodes:
        lines.append(
            f"| `{n.get('id', '?')}` | {', '.join(n.get('namespaces', []))} "
            f"| {n.get('protocol', '?')} | {n.get('health_status', '?')} |"
        )
    return "\n".join(lines)


if __name__ == "__main__":
    import sys

    transport = "stdio"
    if "--transport" in sys.argv:
        idx = sys.argv.index("--transport")
        if idx + 1 < len(sys.argv):
            transport = sys.argv[idx + 1]

    port = 8002
    if "--port" in sys.argv:
        idx = sys.argv.index("--port")
        if idx + 1 < len(sys.argv):
            port = int(sys.argv[idx + 1])

    if transport == "sse":
        mcp.run(transport="sse", host="0.0.0.0", port=port)
    else:
        mcp.run(transport="stdio")
