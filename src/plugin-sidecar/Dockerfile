# ─────────────────────────────────────────────────────────────────────────────
# Plugin Runner Sidecar
#
# Thin FastAPI wrapper that hosts Python extractor plugins and exposes them
# to the emdex-node over HTTP so plugins can run in a Python container while
# the node binary stays in its Go/Alpine image.
#
#   GET  /health       — liveness check (returns 200 OK)
#   GET  /plugins      — list registered plugins and their extensions
#   POST /extract      — extract text + relations from an uploaded file
#
# Port: 8003
# ─────────────────────────────────────────────────────────────────────────────

FROM python:3.13-slim

WORKDIR /app

RUN pip install --no-cache-dir \
    fastapi==0.115.12 \
    uvicorn[standard]==0.34.3 \
    prometheus-client==0.21.1 \
    python-multipart==0.0.20

COPY server.py .

RUN addgroup --system emdexer && adduser --system --ingroup emdexer emdexer
USER emdexer

# Plugins are volume-mounted at /plugins by the orchestrator (read-only).
VOLUME ["/plugins"]

EXPOSE 8003

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8003"]
