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

FROM python:3.12-slim

WORKDIR /app

# sentence-transformers pulls in torch (CPU-only wheel kept small via index-url).
RUN pip install --no-cache-dir \
    --extra-index-url https://download.pytorch.org/whl/cpu \
    torch==2.6.0+cpu \
    sentence-transformers==3.4.1 \
    fastapi==0.115.12 \
    uvicorn[standard]==0.34.3 \
    pydantic==2.11.3 \
    python-multipart==0.0.20

# Pre-download the model at build time so startup is instant at runtime.
ARG RERANKER_MODEL=cross-encoder/ms-marco-MiniLM-L-6-v2
ENV RERANKER_MODEL=${RERANKER_MODEL}
RUN python -c "from sentence_transformers import CrossEncoder; CrossEncoder('${RERANKER_MODEL}')"

COPY server.py .

RUN addgroup --system emdexer && adduser --system --ingroup emdexer emdexer
USER emdexer

EXPOSE 8005

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8005"]
