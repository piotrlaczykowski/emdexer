import main
from fastapi.testclient import TestClient

client = TestClient(main.app)

def test_health_returns_ok():
    r = client.get("/health")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}


def test_eval_empty_samples_returns_400():
    r = client.post("/v1/eval/ragas", json={"samples": [], "metrics": ["context_recall"]})
    assert r.status_code == 400
    assert "samples" in r.json()["error"].lower()

def test_eval_missing_ground_truth_returns_422():
    body = {
        "samples": [{
            "question": "q",
            "answer": "a",
            "contexts": ["c"],
            # ground_truth is missing intentionally
        }],
        "metrics": ["context_recall"],
    }
    r = client.post("/v1/eval/ragas", json=body)
    assert r.status_code == 422

def test_eval_metrics_filter_only_returns_requested(monkeypatch):
    def fake_score(samples, metrics):
        return {
            "context_recall": 0.9,
            "faithfulness": 0.8,
            "per_sample": [{"question": s["question"], "context_recall": 0.9, "faithfulness": 0.8} for s in samples],
        }
    monkeypatch.setattr(main, "_score_samples", fake_score)
    body = {
        "samples": [{"question": "q", "answer": "a", "contexts": ["c"], "ground_truth": "gt"}],
        "metrics": ["context_recall"],
    }
    r = client.post("/v1/eval/ragas", json=body)
    assert r.status_code == 200
    js = r.json()
    assert "context_recall" in js
    assert "faithfulness" not in js

def test_eval_returns_per_sample_breakdown(monkeypatch):
    monkeypatch.setattr(main, "_score_samples", lambda s, m: {
        "context_recall": 0.5, "faithfulness": 0.5,
        "per_sample": [{"question": "q", "context_recall": 0.5, "faithfulness": 0.5}],
    })
    body = {
        "samples": [{"question": "q", "answer": "a", "contexts": ["c"], "ground_truth": "gt"}],
        "metrics": ["context_recall", "faithfulness"],
    }
    r = client.post("/v1/eval/ragas", json=body)
    assert r.status_code == 200
    assert len(r.json()["per_sample"]) == 1
