# ─────────────────────────────────────────────────────────────────────────────
# Extractous Sidecar
#
# Thin HTTP wrapper around the `extractous` Python library.
# Exposes two endpoints used by emdex-node:
#
#   POST /extract              — extract text from an uploaded file
#   POST /extract?ocr=true     — same, with Tesseract OCR enabled
#   GET  /health               — liveness check (returns 200 OK)
#
# Port: 8000
# ─────────────────────────────────────────────────────────────────────────────

FROM python:3.12-slim

# Tesseract is required for OCR support (extract?ocr=true).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        tesseract-ocr \
        tesseract-ocr-eng \
        libgl1 \
        libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

RUN pip install --no-cache-dir \
    extractous==0.3.0 \
    fastapi==0.115.12 \
    uvicorn[standard]==0.34.3 \
    python-multipart==0.0.20

COPY server.py .

RUN addgroup --system emdexer && adduser --system --ingroup emdexer emdexer
USER emdexer

EXPOSE 8000

CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
