import os
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


def _build_llm():
    provider = os.getenv("RAGAS_LLM_PROVIDER", "openai").lower()
    model = os.getenv("RAGAS_MODEL", "")
    if provider == "google":
        from langchain_google_genai import ChatGoogleGenerativeAI
        return ChatGoogleGenerativeAI(model=model or "gemini-2.0-flash")
    from langchain_openai import ChatOpenAI
    return ChatOpenAI(model=model or "gpt-4o-mini")


def _score_samples(samples: list, metrics: list) -> dict:
    from datasets import Dataset
    from ragas import evaluate
    from ragas.metrics import context_recall, faithfulness as faithfulness_metric

    ds = Dataset.from_list([
        {
            "question": s["question"],
            "answer": s["answer"],
            "contexts": s["contexts"],
            "ground_truth": s["ground_truth"],
        }
        for s in samples
    ])

    _METRIC_REGISTRY = {
        "context_recall": context_recall,
        "faithfulness": faithfulness_metric,
    }
    llm = _build_llm()
    metric_objs = [_METRIC_REGISTRY[m] for m in metrics if m in _METRIC_REGISTRY]
    result = evaluate(ds, metrics=metric_objs, llm=llm)
    df = result.to_pandas()

    out: dict = {}
    if "context_recall" in metrics:
        out["context_recall"] = float(df["context_recall"].mean())
    if "faithfulness" in metrics:
        out["faithfulness"] = float(df["faithfulness"].mean())

    out["per_sample"] = []
    for _, row in df.iterrows():
        entry = {"question": row["question"]}
        if "context_recall" in metrics:
            entry["context_recall"] = float(row["context_recall"])
        if "faithfulness" in metrics:
            entry["faithfulness"] = float(row["faithfulness"])
        out["per_sample"].append(entry)
    return out


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
