from typing import List
from fastapi import FastAPI
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

app = FastAPI(title="emdexer-ragas-sidecar")

VALID_METRICS = {"context_recall", "faithfulness"}


class Sample(BaseModel):
    question: str
    answer: str
    contexts: List[str]
    ground_truth: str


class EvalRequest(BaseModel):
    samples: List[Sample] = Field(default_factory=list)
    metrics: List[str] = Field(default_factory=lambda: ["context_recall", "faithfulness"])


def _score_samples(samples: list, metrics: list) -> dict:
    """Stub — replaced in Task 3. Tests monkey-patch this."""
    raise NotImplementedError("RAGAS scoring not wired yet")


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/v1/eval/ragas")
def eval_ragas(req: EvalRequest):
    if not req.samples:
        return JSONResponse(status_code=400, content={"error": "samples must be non-empty"})

    requested = [m for m in req.metrics if m in VALID_METRICS]
    if not requested:
        return JSONResponse(status_code=400, content={"error": "no valid metrics requested"})

    try:
        scores = _score_samples([s.model_dump() for s in req.samples], requested)
    except Exception as e:
        return JSONResponse(status_code=500, content={"error": f"scoring failed: {e}"})

    out = {k: v for k, v in scores.items() if k in requested}
    out["per_sample"] = [
        {k: v for k, v in p.items() if k == "question" or k in requested}
        for p in scores.get("per_sample", [])
    ]
    return out
