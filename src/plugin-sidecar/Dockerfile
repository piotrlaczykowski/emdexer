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
    fastapi==0.161.1 \
    uvicorn[standard]==0.54.0 \
    prometheus-client==0.26.0 \
    python-multipart==0.0.32

COPY server.py .

# Fixed numeric uid/gid: Kubernetes' runAsNonRoot can only verify a numeric USER.
RUN addgroup --system --gid 10001 emdexer && adduser --system --uid 10001 --ingroup emdexer emdexer
USER 10001:10001

# The repo's plugins/ are baked in from the named build context `plugins`
# (compose: additional_contexts; CI: build-contexts). docker-compose also mounts
# ../../plugins over /plugins so local edits apply without a rebuild.
COPY --from=plugins . /plugins

EXPOSE 8003

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8003"]
