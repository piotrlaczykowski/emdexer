"""
Plugin Runner Sidecar — hosts Python extractor plugins over HTTP.

Endpoints
---------
GET  /health              — liveness check
GET  /plugins             — list registered plugins and their extensions
POST /extract             — extract text + relations from an uploaded file
GET  /metrics             — Prometheus metrics

Request (/extract)
------------------
Content-Type: multipart/form-data
Field:        file              (the file to extract; filename must be set)
Query param:  plugin (optional) — target a specific plugin by name

Response (/extract)
-------------------
{
  "text":      "<extracted text>",
  "relations": [{"type": "...", "target": "...", "name": "..."}, ...]
}

HTTP status codes
-----------------
200  success
400  no matching plugin / missing file
500  plugin raised an exception
"""

import base64
import importlib.util
import json
import logging
import os
import sys
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, File, HTTPException, Query, UploadFile
from fastapi.responses import JSONResponse, Response
from prometheus_client import CONTENT_TYPE_LATEST, Counter, generate_latest

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

app = FastAPI(title="plugin-sidecar", docs_url=None, redoc_url=None)

# ─── Prometheus ──────────────────────────────────────────────────────────────

_requests_total = Counter(
    "emdexer_plugin_sidecar_requests_total",
    "Total plugin extract requests partitioned by plugin name and outcome.",
    ["plugin", "status"],
)

# ─── Plugin registry ─────────────────────────────────────────────────────────

_PLUGIN_DIR = Path(os.getenv("EMDEX_PLUGIN_DIR", "/plugins"))

# Each entry: {"name": str, "extensions": [str, ...], "module": <loaded module>}
_registry: list[dict] = []


def _parse_meta(source: str) -> tuple[str, list[str]]:
    """Extract # name: and # extensions: from the first 30 lines of source."""
    name = ""
    exts: list[str] = []
    for i, line in enumerate(source.splitlines()):
        if i >= 30:
            break
        line = line.strip()
        if not line.startswith("#"):
            continue
        if line.startswith("# name:"):
            name = line[len("# name:"):].strip()
        elif line.startswith("# extensions:"):
            raw = line[len("# extensions:"):].strip()
            exts = [e.strip() for e in raw.split(",") if e.strip()]
    return name, exts


def _load_plugins(plugin_dir: Path) -> list[dict]:
    entries = []
    if not plugin_dir.is_dir():
        logger.warning("Plugin directory not found: %s", plugin_dir)
        return entries

    for path in sorted(plugin_dir.glob("*.py")):
        try:
            source = path.read_text(encoding="utf-8", errors="replace")
        except OSError as exc:
            logger.warning("Cannot read plugin %s: %s — skipping", path, exc)
            continue

        name, exts = _parse_meta(source)
        if not name or not exts:
            logger.warning(
                "Skipping %s — missing # name: or # extensions: metadata", path.name
            )
            continue

        spec = importlib.util.spec_from_file_location(f"plugin_{path.stem}", path)
        if spec is None or spec.loader is None:
            logger.warning("Cannot build module spec for %s — skipping", path.name)
            continue

        module = importlib.util.module_from_spec(spec)
        try:
            spec.loader.exec_module(module)  # type: ignore[union-attr]
        except Exception as exc:  # noqa: BLE001
            logger.warning("Error loading plugin %s: %s — skipping", path.name, exc)
            continue

        if not callable(getattr(module, "extract", None)):
            logger.warning(
                "Plugin %s has no callable extract() function — skipping", path.name
            )
            continue

        entries.append({"name": name, "extensions": exts, "module": module})
        logger.info("Loaded plugin: %s for %s", name, exts)

    return entries


# Load plugins at startup.
_registry = _load_plugins(_PLUGIN_DIR)
logger.info("%d plugin(s) registered", len(_registry))


# ─── Endpoints ───────────────────────────────────────────────────────────────


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/metrics")
def metrics():
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.get("/plugins")
def list_plugins():
    return [{"name": p["name"], "extensions": p["extensions"]} for p in _registry]


@app.post("/extract")
async def extract(
    file: UploadFile = File(...),
    plugin: Optional[str] = Query(default=None),
):
    content = await file.read()
    if not content:
        raise HTTPException(status_code=400, detail="empty file")

    filename = file.filename or "unknown"
    ext = Path(filename).suffix.lower()

    # Find the matching plugin: prefer the requested plugin name, else match by extension.
    matched = None
    if plugin:
        for p in _registry:
            if p["name"] == plugin:
                matched = p
                break
        if matched is None:
            raise HTTPException(
                status_code=400,
                detail=f"Plugin '{plugin}' not registered. Available: {[p['name'] for p in _registry]}",
            )
    else:
        for p in _registry:
            if ext in p["extensions"]:
                matched = p
                break
        if matched is None:
            raise HTTPException(
                status_code=400,
                detail=f"No plugin registered for extension '{ext}'",
            )

    plugin_name = matched["name"]
    try:
        result = matched["module"].extract(filename, content)
        _requests_total.labels(plugin=plugin_name, status="ok").inc()
    except Exception as exc:  # noqa: BLE001
        logger.error("Plugin %s failed for %s: %s", plugin_name, filename, exc)
        _requests_total.labels(plugin=plugin_name, status="error").inc()
        raise HTTPException(status_code=500, detail=str(exc)) from exc

    # Normalise result: accept dict or JSON string.
    if isinstance(result, str):
        try:
            result = json.loads(result)
        except json.JSONDecodeError:
            result = {"text": result, "relations": []}

    text = result.get("text", "")
    relations = result.get("relations", [])

    logger.info(
        "extracted %d chars from %s via plugin %s", len(text), filename, plugin_name
    )
    return JSONResponse({"text": text, "relations": relations})
