# name: CSV Extractor
# extensions: .csv

"""
Emdexer plugin: CSV Extractor

Converts CSV rows into plain text (one row per line, values space-joined)
so the indexer can embed and search tabular data as natural language.

Usage:
    Copy or symlink this file into your EMDEX_PLUGIN_DIR directory (default: ./plugins/).
    Emdexer will load it automatically on next startup.

Contract (all Emdexer Python plugins must follow this):
    • Declare metadata via top-level comments:  # name: ...  and  # extensions: ...
    • Implement:  extract(filename: str, data: bytes) -> dict
      where dict has keys "text" (str) and "relations" (list of dicts).
    • Handle the __main__ block exactly as shown — Emdexer calls the script as a
      subprocess and communicates via stdin/stdout using base64-encoded JSON.
"""

import csv
import io
import json
import sys
import base64


def extract(filename: str, data: bytes) -> dict:
    """Extract text from a CSV file by joining each row's values with spaces."""
    text_rows = []
    try:
        reader = csv.DictReader(io.StringIO(data.decode("utf-8", errors="replace")))
        for row in reader:
            row_text = " ".join(str(v) for v in row.values() if v)
            if row_text.strip():
                text_rows.append(row_text)
    except Exception as exc:
        # Return whatever we have; the indexer will skip the file if text is too short.
        return {"text": "", "relations": [{"type": "defines", "name": f"error:{exc}"}]}

    return {"text": "\n".join(text_rows), "relations": []}


if __name__ == "__main__":
    # Emdexer sends:  base64( JSON( {"filename": str, "data": base64(bytes)} ) )
    raw = sys.stdin.buffer.read()
    payload = json.loads(base64.b64decode(raw))
    result = extract(payload["filename"], base64.b64decode(payload["data"]))
    print(json.dumps(result))
