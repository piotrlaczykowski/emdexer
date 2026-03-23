# Extractor Plugins

Emdexer supports **extractor plugins** — Python scripts dropped into a directory that are automatically discovered at node startup. Plugins extend the indexing pipeline to handle file formats not covered by the built-in extractors (Extractous, Whisper) without recompiling Emdexer.

---

## How it works

Emdexer supports two plugin execution modes. Both modes use the same plugin contract.

### Sidecar mode (recommended for Docker/Kubernetes)

```
plugins/
└── csv_extractor.py   ← volume-mounted into plugin-sidecar at /plugins

plugin-sidecar (port 8003)
  └── loads plugins at startup via importlib
  └── exposes POST /extract

Node
  └── EMDEX_PLUGIN_SIDECAR_URL=http://plugin-sidecar:8003/extract
        └── GET /plugins  → discovers registered plugins
        └── POST /extract → uploads file bytes as multipart form
```

### Subprocess mode (dev/local only — requires Python on PATH)

```
plugins/
└── my_extractor.py   ← discovered automatically on startup

Node startup
  └── plugin.LoadPlugins("./plugins")
        └── spawn python my_extractor.py (subprocess)
              stdin:  base64(JSON({"filename":..., "data": base64(bytes)}))
              stdout: JSON({"text":..., "relations":[...]})
```

When a file with a matching extension is indexed (both modes):

1. The node routes the file to the matching plugin (by extension).
2. Raw file bytes are forwarded to the plugin, either via HTTP multipart (sidecar) or stdin (subprocess).
3. The plugin returns `{"text": "...", "relations": [...]}`.
4. Emdexer chunks and embeds the returned text exactly like any other file.
5. If the plugin provides `relations`, those are stored on chunk 0; otherwise Emdexer derives relations from the extracted text as usual.

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_PLUGIN_ENABLED` | `true` | Set to `false` to disable the plugin system entirely. |
| `EMDEX_PLUGIN_SIDECAR_URL` | _(unset)_ | When set, use the sidecar HTTP service instead of subprocesses. Format: `http://host:port/extract`. |
| `EMDEX_PLUGIN_DIR` | `./plugins` | Directory scanned for `*.py` files. Passed to the sidecar as `EMDEX_PLUGIN_DIR` or used directly in subprocess mode. |
| `EMDEX_PLUGIN_TIMEOUT` | `10s` | Maximum time per plugin call (subprocess mode only). Go duration syntax. |

**Docker/Kubernetes** (sidecar mode):

```bash
EMDEX_PLUGIN_ENABLED=true
EMDEX_PLUGIN_SIDECAR_URL=http://plugin-sidecar:8003/extract
```

**Local dev** (subprocess mode — requires `python3` or `python` on PATH):

```bash
EMDEX_PLUGIN_ENABLED=true
EMDEX_PLUGIN_DIR=./plugins
# EMDEX_PLUGIN_TIMEOUT=10s
```

---

## Writing a plugin

A plugin is a single `.py` file with two requirements:

### 1. Metadata comments (top of file)

```python
# name: My Extractor
# extensions: .xyz,.xyzx
```

Both comments are required. Extensions must be lowercase and include the leading dot.

### 2. `extract` function + `__main__` block

```python
def extract(filename: str, data: bytes) -> dict:
    """Return {"text": str, "relations": list[dict]}."""
    ...

if __name__ == '__main__':
    import json, sys, base64
    payload = json.loads(base64.b64decode(sys.stdin.read()))
    result = extract(payload['filename'], base64.b64decode(payload['data']))
    print(json.dumps(result))
```

The `relations` list is optional. Each relation dict mirrors the indexer schema:

| Key | Values | When to use |
|-----|--------|-------------|
| `type` | `"imports"`, `"links_to"`, `"defines"` | Required |
| `target` | path or module name | For `imports` / `links_to` |
| `name` | identifier name | For `defines` |

---

## Example: CSV Extractor

See `plugins/csv_extractor.py` for a complete, commented example that converts CSV rows to plain text.

The file is in the `plugins/` root — it is picked up automatically by the plugin-sidecar (volume-mounted) and by the subprocess runner in local dev mode.

---

## Plugin discovery rules

- Only `*.py` files directly inside the plugin directory are loaded (non-recursive).
- Files missing `# name:` or `# extensions:` metadata are skipped with a log warning.
- If two plugins declare the same extension, **last-loaded wins** (alphabetical order within the directory) and a warning is logged.
- **Sidecar mode**: discovery happens at sidecar startup via `importlib`. Plugins with no `extract()` function are skipped.
- **Subprocess mode**: if Python (`python3` or `python`) is not found on `PATH`, the plugin system is silently skipped — no error, no startup failure.
- If the plugin directory does not exist, the sidecar logs a warning and starts with 0 plugins.

---

## Prometheus metrics

### Node (both modes)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `emdexer_node_plugin_calls_total` | Counter | `plugin`, `extension`, `status` | Total plugin calls from the node. `status` ∈ `{ok, error, timeout}` |
| `emdexer_node_plugin_duration_ms` | Histogram | `plugin` | Per-call wall-clock duration (ms) including network round-trip in sidecar mode |

### Plugin sidecar (sidecar mode only)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `emdexer_plugin_sidecar_requests_total` | Counter | `plugin`, `status` | Requests received by the sidecar. `status` ∈ `{ok, error}` |

Sidecar metrics are exposed at `http://plugin-sidecar:8003/metrics` (Prometheus format).

---

## Priority over built-in extractors

Plugins take priority over the Extractous and Whisper sidecars for any extension they claim. The built-in extraction path is used as a fallback when:

- No plugin is loaded for the file's extension.
- The plugin is matched but the file content is empty/unavailable (e.g. streaming from a remote VFS).

---

## Limitations

- **Subprocess mode**: plugins run in a separate Python subprocess per file — no persistent process. For large-scale indexing, subprocess overhead adds up. Use sidecar mode in production.
- **Sidecar mode**: the sidecar loads plugins once at startup. Adding or removing a plugin requires restarting the sidecar.
- Only Python plugins are supported. Native Go (`.so`) plugin support is not planned.
- **Subprocess mode**: plugin stdout must be valid JSON. Any extra print statements or tracebacks written to stdout will break parsing — use `sys.stderr` for debug output.
- The `EMDEX_PLUGIN_TIMEOUT` applies per file in subprocess mode; in sidecar mode the Go HTTP client uses a 30s default timeout.
- The `__main__` block is required for subprocess mode but harmless in sidecar mode (the sidecar imports the module without executing `__main__`).
