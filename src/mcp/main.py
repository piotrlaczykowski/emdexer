import os
import requests
from fastmcp import FastMCP
from prefab_ui.app import PrefabApp
from prefab_ui.components import DataTable, DataTableColumn, Text, Dashboard, DashboardItem
from prefab_ui.components.charts import BarChart

# Configuration
GATEWAY_URL = os.getenv("GATEWAY_URL", "http://gateway:7700")
# Supports both static API keys (EMDEX_AUTH_KEY) and OIDC JWT tokens (EMDEX_BEARER_TOKEN).
# EMDEX_BEARER_TOKEN takes precedence if set — use it when the gateway is OIDC-protected.
BEARER_TOKEN = os.getenv("EMDEX_BEARER_TOKEN", "") or os.getenv("EMDEX_AUTH_KEY", "")
if not BEARER_TOKEN:
    raise RuntimeError("EMDEX_AUTH_KEY or EMDEX_BEARER_TOKEN environment variable is required.")

# Output mode: "gui" (default, PrefabApp for Claude Desktop) or "text" (markdown for OpenClaw/CLI)
OUTPUT_MODE = os.getenv("EMDEX_OUTPUT_MODE", "gui").lower()

mcp = FastMCP("emdexer")

def get_headers():
    return {"Authorization": f"Bearer {BEARER_TOKEN}"}

def is_text_mode() -> bool:
    return OUTPUT_MODE == "text"

# Map file extensions to media type tags for multi-modal content.
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


@mcp.tool()
def search_files(query: str, namespace: str = "default") -> str | PrefabApp:
    """Search for files in EMDEX with semantic ranking. Use namespace='*' for global search across all authorized namespaces."""
    url = f"{GATEWAY_URL}/v1/search"
    params = {"q": query, "namespace": namespace}

    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])
    except Exception as e:
        msg = f"Error searching EMDEX: {str(e)}"
        return msg if is_text_mode() else PrefabApp(children=[Text(text=msg)])

    if not results:
        msg = f"No results found for **{query}** in namespace `{namespace}`."
        return msg if is_text_mode() else PrefabApp(children=[Text(text=msg)])

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

    if is_text_mode():
        lines = [f"### Search results for **{query}** in `{namespace}`\n"]
        lines.append(f"{'#'} | Path | Score | Preview")
        lines.append("---|---|---|---")
        for i, row in enumerate(table_data, 1):
            lines.append(f"{i} | `{row['Path']}` | {row['Score']} | {row['Preview']}")
        return "\n".join(lines)

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


@mcp.tool()
def get_file(path: str) -> str | PrefabApp:
    """Retrieve file content from EMDEX."""
    url = f"{GATEWAY_URL}/v1/search"
    params = {"q": f"file path: {path}", "limit": 1}

    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])

        if results:
            content = results[0].get("payload", {}).get("text", "No content found.")
        else:
            content = f"File `{path}` not found in index."
    except Exception as e:
        content = f"Error fetching file: {str(e)}"

    return content if is_text_mode() else PrefabApp(children=[Text(text=content)])


@mcp.tool()
def system_status() -> str | PrefabApp:
    """Display EMDEX indexing status and active nodes."""
    try:
        resp = requests.get(f"{GATEWAY_URL}/health", timeout=5)
        health = resp.json()
        resp_nodes = requests.get(f"{GATEWAY_URL}/nodes", headers=get_headers(), timeout=5)
        nodes = resp_nodes.json()
    except Exception as e:
        msg = f"Error fetching status: {str(e)}"
        return msg if is_text_mode() else PrefabApp(children=[Text(text=msg)])

    if is_text_mode():
        lines = [
            f"### EMDEX System Status",
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

    storage_data = [
        {"label": "Documents", "value": 45},
        {"label": "Images", "value": 20},
        {"label": "Code", "value": 35},
    ]
    return PrefabApp(
        children=[
            Dashboard(
                children=[
                    DashboardItem(
                        title="Status",
                        children=[
                            Text(
                                text=f"**Gateway:** {health.get('status', 'Unknown')}\n**Active Nodes:** {len(nodes)}"
                            )
                        ],
                    ),
                    DashboardItem(
                        title="Storage Distribution (%)",
                        children=[BarChart(data=storage_data)],
                    ),
                ]
            )
        ]
    )


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
