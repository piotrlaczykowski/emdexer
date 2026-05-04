from fastapi import FastAPI

app = FastAPI(title="emdexer-ragas-sidecar")

@app.get("/health")
def health():
    return {"status": "ok"}
