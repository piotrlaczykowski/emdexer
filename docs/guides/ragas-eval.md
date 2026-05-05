# RAGAS Eval Harness

The RAGAS eval harness measures retrieval and generation quality using two metrics:

- **context_recall**: How much of the ground-truth answer is covered by the retrieved contexts.
- **faithfulness**: How faithful the generated answer is to the retrieved contexts.

## Prerequisites

You need either an OpenAI or Google API key:

```bash
export OPENAI_API_KEY=sk-...          # OpenAI (default)
# or
export GOOGLE_API_KEY=AIza...         # Google (set RAGAS_LLM_PROVIDER=google)
```

## Quick Start

Start the ragas-sidecar using the `eval` Docker Compose profile:

```bash
docker compose -f deploy/docker/docker-compose.yml --profile eval up ragas-sidecar -d
```

Verify it's running:

```bash
curl http://localhost:8006/health
# {"status": "ok"}
```

## Running the Evaluation

```bash
emdex eval \
  --ragas \
  --ground-truth src/ragas-sidecar/fixtures/emdexer-eval-20q.json \
  --threshold 0.75
```

This runs 20 questions against your Emdexer instance, scores context_recall and faithfulness via RAGAS, and exits 1 if context_recall drops below 0.75.

## Interpreting Results

| Metric | Threshold | Meaning |
|--------|-----------|---------|
| `context_recall` | ≥ 0.75 | Retrieved contexts cover the ground-truth answer |
| `faithfulness` | ≥ 0.70 | Generated answer is grounded in retrieved contexts |

Both metrics range from 0 to 1. Scores below threshold trigger the Prometheus alert rules.

## CI Integration

Add the `eval` label to any GitHub PR to trigger the `ragas-eval-smoke` CI job. The job:
1. Builds the `emdex` binary
2. Starts the ragas-sidecar Docker container
3. Runs `emdex eval --ragas --threshold 0.0` (pipeline check only)
4. Tears down the container

The job is skipped automatically if `OPENAI_API_KEY` is not set (safe for forks).

## Tuning Tips

**Improving context_recall:**
- Increase `EMDEX_SEARCH_TOP_K` to retrieve more candidates
- Tune chunk size in the node indexer
- Enable reranker (`EMDEX_RERANK_ENABLED=true`)

**Improving faithfulness:**
- Use a stronger LLM for generation
- Reduce top-k to keep context focused
- Enable the reranker to surface more relevant chunks first

## Provider Switching

The sidecar reads `RAGAS_LLM_PROVIDER` from its own environment:

```bash
docker run -e RAGAS_LLM_PROVIDER=google -e GOOGLE_API_KEY=$GOOGLE_API_KEY \
  -e RAGAS_MODEL=gemini-2.0-flash ghcr.io/piotrlaczykowski/emdexer/ragas-sidecar:latest
```
