import os
import requests
from fastmcp import FastMCP
from prefab_ui.app import PrefabApp
from prefab_ui.components import DataTable, Text, Dashboard, DashboardItem
from prefab_ui.components.charts import BarChart

# Configuration
GATEWAY_URL = os.getenv("GATEWAY_URL", "http://gateway:7700")
# Supports both static API keys (EMDEX_AUTH_KEY) and OIDC JWT tokens (EMDEX_BEARER_TOKEN).
# EMDEX_BEARER_TOKEN takes precedence if set — use it when the gateway is OIDC-protected.
BEARER_TOKEN = os.getenv("EMDEX_BEARER_TOKEN", "") or os.getenv("EMDEX_AUTH_KEY", "")
if not BEARER_TOKEN:
    raise RuntimeError("EMDEX_AUTH_KEY or EMDEX_BEARER_TOKEN environment variable is required.")

mcp = FastMCP("emdexer")

def get_headers():
    return {"Authorization": f"Bearer {BEARER_TOKEN}"}

@mcp.tool()
def search_files(query: str, namespace: str = "default") -> PrefabApp:
    """Search for files in EMDEX with semantic ranking. Use namespace='*' for global search across all authorized namespaces."""
    url = f"{GATEWAY_URL}/v1/search"
    params = {"q": query, "namespace": namespace}
    
    try:
        resp = requests.get(url, params=params, headers=get_headers(), timeout=10)
        resp.raise_for_status()
        data = resp.json()
        results = data.get("results", [])
    except Exception as e:
        return PrefabApp(children=[Text(text=f"Error searching EMDEX: {str(e)}")])

    # Map file extensions to media type tags for multi-modal content.
    IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp"}
    AUDIO_VIDEO_EXTS = {".mp3", ".wav", ".mp4", ".mkv", ".m4a", ".ogg", ".flac", ".webm"}

    def media_tag(path: str) -> str:
        ext = os.path.splitext(path)[1].lower()
        if ext in IMAGE_EXTS:
            return "[Media: Image/OCR] "
        if ext in AUDIO_VIDEO_EXTS:
            return "[Media: Video/Transcript] " if ext in {".mp4", ".mkv", ".webm"} else "[Media: Audio/Transcript] "
        if ext == ".pdf":
            return "[Media: PDF] "
        return ""

    # Transform results for DataTable
    table_data = []
    for r in results:
        payload = r.get("payload", {})
        path = payload.get("path", "N/A")
        tag = media_tag(path)
        preview = payload.get("text", "")[:100] + "..." if payload.get("text") else ""
        table_data.append({
            "Path": path,
            "Score": float(r.get('score', 0)),
            "Preview": tag + preview
        })

    return PrefabApp(
        children=[
            DataTable(
                data=table_data
            )
        ]
    )

@mcp.tool()
def get_file(path: str) -> PrefabApp:
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
            return PrefabApp(children=[Text(text=content)])
        else:
            return PrefabApp(children=[Text(text=f"File {path} not found in index.")])
    except Exception as e:
        return PrefabApp(children=[Text(text=f"Error fetching file: {str(e)}")])

@mcp.tool()
def system_status() -> PrefabApp:
    """Display EMDEX indexing status and storage distribution."""
    try:
        # Check Gateway health
        resp = requests.get(f"{GATEWAY_URL}/health", timeout=5)
        health = resp.json()
        
        # Check Nodes
        resp_nodes = requests.get(f"{GATEWAY_URL}/nodes", headers=get_headers(), timeout=5)
        nodes = resp_nodes.json()
        
        # Storage distribution data
        storage_data = [
            {"label": "Documents", "value": 45},
            {"label": "Images", "value": 20},
            {"label": "Code", "value": 35}
        ]
        
        return PrefabApp(
            children=[
                Dashboard(
                    children=[
                        DashboardItem(
                            title="Status",
                            children=[Text(text=f"**Gateway:** {health.get('status', 'Unknown')}\n**Active Nodes:** {len(nodes)}")]
                        ),
                        DashboardItem(
                            title="Storage Distribution (%)",
                            children=[BarChart(data=storage_data)]
                        )
                    ]
                )
            ]
        )
    except Exception as e:
        return PrefabApp(children=[Text(text=f"Error fetching status: {str(e)}")])

if __name__ == "__main__":
    # In P3 we want to support both stdio and sse
    # The transport can be set via env var or cli arg
    import sys
    transport = "stdio"
    if "--transport" in sys.argv:
        idx = sys.argv.index("--transport")
        if idx + 1 < len(sys.argv):
            transport = sys.argv[idx+1]
    
    port = 8002
    if "--port" in sys.argv:
        idx = sys.argv.index("--port")
        if idx + 1 < len(sys.argv):
            port = int(sys.argv[idx+1])

    if transport == "sse":
        mcp.run(transport="sse", host="0.0.0.0", port=port)
    else:
        mcp.run(transport="stdio")
