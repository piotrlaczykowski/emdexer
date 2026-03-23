# Extractor Plugins

Emdexer supports **extractor plugins** — Python scripts dropped into a directory that are automatically discovered at node startup. Plugins extend the indexing pipeline to handle file formats not covered by the built-in extractors (Extractous, Whisper) without recompiling Emdexer.

---

## How it works

```
plugins/
└── my_extractor.py   ← discovered automatically on startup

Node startup
  └── plugin.LoadPlugins("./plugins")
        └── spawn python my_extractor.py (subprocess)
              stdin:  base64(JSON({"filename":..., "data": base64(bytes)}))
              stdout: JSON({"text":..., "relations":[...]})
```

When a file with a matching extension is indexed:

1. The node spawns the plugin script as a Python subprocess.
2. Raw file bytes are sent to stdin as `base64(JSON({filename, data}))`.
3. The plugin writes `{"text": "...", "relations": [...]}` to stdout.
4. Emdexer chunks and embeds the returned text exactly like any other file.
5. If the plugin provides `relations`, those are stored on chunk 0; otherwise Emdexer derives relations from the extracted text as usual.

---

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_PLUGIN_ENABLED` | `true` | Set to `false` to disable the plugin system entirely. |
| `EMDEX_PLUGIN_DIR` | `./plugins` | Directory scanned for `*.py` plugin files at startup. |
| `EMDEX_PLUGIN_TIMEOUT` | `10s` | Maximum time allowed per plugin call. Go duration syntax (`10s`, `30s`). |

Add to your `.env`:

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

See `plugins/examples/csv_extractor.py` for a complete, commented example that converts CSV rows to plain text.

To activate it:

```bash
cp plugins/examples/csv_extractor.py plugins/
# Restart the node — it will be picked up automatically.
```

---

## Plugin discovery rules

- Only `*.py` files directly inside `EMDEX_PLUGIN_DIR` are loaded (non-recursive).
- Files missing `# name:` or `# extensions:` metadata are skipped with a log warning.
- If two plugins declare the same extension, **last-loaded wins** (alphabetical order within the directory) and a warning is logged.
- If Python (`python3` or `python`) is not found on `PATH`, the plugin system is silently skipped — no error, no startup failure.
- If `EMDEX_PLUGIN_DIR` does not exist, the plugin system is silently skipped.

---

## Prometheus metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `emdexer_node_plugin_calls_total` | Counter | `plugin`, `extension`, `status` | Total plugin calls. `status` ∈ `{ok, error, timeout}` |
| `emdexer_node_plugin_duration_ms` | Histogram | `plugin` | Per-call wall-clock duration in milliseconds |

---

## Priority over built-in extractors

Plugins take priority over the Extractous and Whisper sidecars for any extension they claim. The built-in extraction path is used as a fallback when:

- No plugin is loaded for the file's extension.
- The plugin is matched but the file content is empty/unavailable (e.g. streaming from a remote VFS).

---

## Limitations

- Plugins run in a separate Python subprocess for each file — there is no persistent plugin process. For large-scale indexing of many small files, the subprocess overhead adds up. Consider the Extractous sidecar for high-throughput scenarios.
- Only Python plugins are supported. Native Go (`.so`) plugin support is not planned.
- Plugin stdout must be valid JSON. Any extra print statements or tracebacks written to stdout will break parsing — use `sys.stderr` for debug output.
- The `EMDEX_PLUGIN_TIMEOUT` applies per file, not per indexing run.
