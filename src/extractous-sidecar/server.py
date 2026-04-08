"""
Extractous sidecar — thin HTTP wrapper around the `extractous` Python library.

Endpoints
---------
GET  /health                  — liveness check
POST /extract                 — extract text from uploaded file
POST /extract?ocr=true        — same, with Tesseract OCR enabled

Request (both extract endpoints)
---------------------------------
Content-Type: multipart/form-data
Field:        file  (the file to extract text from)

Response
--------
{
  "text":     "<extracted text>",
  "metadata": { ... }          # best-effort; empty dict on failure
}

HTTP status codes
-----------------
200  success
400  missing or unreadable file field
500  extraction error
"""

import io
import logging
from typing import Optional

from extractous import Extractor, TesseractOcrConfig
from fastapi import FastAPI, File, HTTPException, Query, UploadFile
from fastapi.responses import JSONResponse

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

app = FastAPI(title="extractous-sidecar", docs_url=None, redoc_url=None)

# Build extractors once at module load — construction is not free.
_extractor_plain = Extractor()
_extractor_ocr = Extractor().set_ocr_config(
    TesseractOcrConfig().set_language("eng")
)


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/extract")
async def extract(
    file: UploadFile = File(...),
    ocr: Optional[bool] = Query(default=False),
):
    content = await file.read()
    if not content:
        raise HTTPException(status_code=400, detail="empty file")

    extractor = _extractor_ocr if ocr else _extractor_plain

    try:
        reader, metadata = extractor.extract_bytes(content, "")
        chunks = []
        for chunk in reader:
            chunks.append(chunk)
        text = "".join(chunks)
    except Exception as exc:
        logger.error("extraction failed for %s: %s", file.filename, exc)
        raise HTTPException(status_code=500, detail=str(exc)) from exc

    # Normalise metadata: extractous returns a Java-style dict; convert values to str
    # so the JSON response is always serialisable.
    safe_meta: dict = {}
    if metadata:
        try:
            safe_meta = {k: str(v) for k, v in metadata.items()}
        except Exception:
            pass

    logger.info(
        "extracted %d chars from %s (ocr=%s)",
        len(text),
        file.filename,
        ocr,
    )
    return JSONResponse({"text": text, "metadata": safe_meta})
