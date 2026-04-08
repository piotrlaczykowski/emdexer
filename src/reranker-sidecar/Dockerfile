# ─────────────────────────────────────────────────────────────────────────────
# BGE Reranker Sidecar
#
# Serves a cross-encoder reranker model over HTTP for late-interaction
# reranking of search results retrieved by the emdex-gateway (Phase 30).
#
#   POST /rerank   — score query–document pairs, return sorted scores
#   GET  /health   — liveness check (returns 200 OK once model is loaded)
#
# Port: 8005
# Model: cross-encoder/ms-marco-MiniLM-L-6-v2  (~90 MB, CPU-friendly)
# Override with RERANKER_MODEL env var.
# ─────────────────────────────────────────────────────────────────────────────

FROM python:3.13-slim

WORKDIR /app

# sentence-transformers pulls in torch (CPU-only wheel kept small via index-url).
RUN pip install --no-cache-dir \
    --extra-index-url https://download.pytorch.org/whl/cpu \
    torch==2.6.0+cpu \
    sentence-transformers==3.4.1 \
    fastapi==0.115.12 \
    uvicorn[standard]==0.34.3 \
    pydantic==2.11.3

# Create non-root user and set up cache directory before model download.
RUN adduser --disabled-password --gecos "" emdexer
ENV HF_HOME=/app/.cache/huggingface
RUN mkdir -p /app/.cache/huggingface && chown -R emdexer:emdexer /app/.cache/huggingface
USER emdexer

# Pre-download the model at build time so startup is instant at runtime.
ARG RERANKER_MODEL=cross-encoder/ms-marco-MiniLM-L-6-v2
ENV RERANKER_MODEL=${RERANKER_MODEL}
COPY preload.py .
RUN RERANKER_MODEL=${RERANKER_MODEL} python preload.py

COPY server.py .

EXPOSE 8005

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8005/health')"

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8005"]
