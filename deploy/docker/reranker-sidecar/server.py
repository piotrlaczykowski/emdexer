"""
BGE Reranker Sidecar
====================
Thin FastAPI wrapper around the sentence-transformers CrossEncoder for
late-interaction reranking. Used by the emdex-gateway as a post-retrieval
refinement step (Phase 30).

Endpoints
---------
POST /rerank
    Body:  {"query": "...", "texts": ["...", ...]}
    Returns: {"results": [{"index": 0, "score": 0.95}, ...]}
    Results are sorted by score descending.

GET  /health
    Returns 200 OK when the model is loaded and ready.

Environment variables
---------------------
RERANKER_MODEL   Cross-encoder model name (default: cross-encoder/ms-marco-MiniLM-L-6-v2)
RERANKER_DEVICE  Torch device — "cpu" or "cuda" (default: cpu)
MAX_TEXTS        Maximum number of texts accepted per request (default: 100)
"""

import os
import logging
from contextlib import asynccontextmanager
from typing import List

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, field_validator
from sentence_transformers import CrossEncoder

logging.basicConfig(level=logging.INFO)
log = logging.getLogger("reranker")

MODEL_NAME = os.getenv("RERANKER_MODEL", "cross-encoder/ms-marco-MiniLM-L-6-v2")
DEVICE = os.getenv("RERANKER_DEVICE", "cpu")
MAX_TEXTS = int(os.getenv("MAX_TEXTS", "100"))

model: CrossEncoder | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global model
    log.info("Loading reranker model: %s on %s", MODEL_NAME, DEVICE)
    model = CrossEncoder(MODEL_NAME, device=DEVICE)
    log.info("Reranker model loaded")
    yield


app = FastAPI(title="emdex-reranker", lifespan=lifespan)


class RerankRequest(BaseModel):
    query: str
    texts: List[str]

    @field_validator("texts")
    @classmethod
    def limit_texts(cls, v: List[str]) -> List[str]:
        if len(v) > MAX_TEXTS:
            raise ValueError(f"texts length {len(v)} exceeds MAX_TEXTS={MAX_TEXTS}")
        return v


class ScoredItem(BaseModel):
    index: int
    score: float


class RerankResponse(BaseModel):
    results: List[ScoredItem]


@app.post("/rerank", response_model=RerankResponse)
def rerank(req: RerankRequest) -> RerankResponse:
    if not req.texts:
        return RerankResponse(results=[])

    pairs = [[req.query, text] for text in req.texts]
    scores = model.predict(pairs).tolist()  # type: ignore[union-attr]

    results = sorted(
        [ScoredItem(index=i, score=float(s)) for i, s in enumerate(scores)],
        key=lambda x: x.score,
        reverse=True,
    )
    return RerankResponse(results=results)


@app.get("/health")
def health():
    if model is None:
        raise HTTPException(status_code=503, detail="model not loaded")
    return {"status": "ok", "model": MODEL_NAME}
