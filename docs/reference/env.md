# Environment Variables Reference

This document lists all environment variables used by Emdexer and related services.

## Core Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_AUTH_KEY` | API authentication token | (required) |
| `EMDEX_PORT` | Gateway listen port | `7700` |
| `EMDEX_QDRANT_COLLECTION` | Qdrant collection name | `emdexer_v1` |

## Embedding Providers

| Variable | Description | Default |
|----------|-------------|---------|
| `EMBED_PROVIDER` | Embedding backend: `gemini`, `ollama`, or `openai` | `gemini` |
| `GOOGLE_API_KEY` | Google API key for Gemini embeddings | (optional) |
| `EMDEX_GEMINI_MODEL` | Gemini embedding model | `gemini-embedding-2-preview` |
| `OPENAI_API_KEY` | OpenAI API key | (optional) |
| `OPENAI_EMBED_MODEL` | OpenAI embedding model | `text-embedding-3-small` |
| `OLLAMA_HOST` | Ollama server URL | `http://localhost:11434` |
| `OLLAMA_EMBED_MODEL` | Ollama embedding model | `nomic-embed-text:v2` |

## Prometheus & Service Discovery

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_SD_FILE` | Prometheus file_sd targets file path | (optional) |
| `EMDEX_SD_HOST_OVERRIDE` | Host IP for Prometheus file_sd | (optional) |
| `EMDEX_NODE_URL_NODE` | Node URL advertised to gateway | `http://<host-ip>:8083` |

## Caching (P47)

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_CACHE_ENABLED` | Enable query cache | `true` |
| `EMDEX_CACHE_BACKEND` | Cache backend: `redis` or `memory` | `redis` |
| `EMDEX_CACHE_REDIS_URL` | Redis connection URL | `redis://redis:6379` |
| `EMDEX_CACHE_TTL` | Query cache TTL | `5m` |
| `EMDEX_CACHE_MAX_ENTRIES` | Max cached queries | `1000` |

## Delta-Indexing Cache (P47)

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_CACHE_DIR` | SQLite delta-index cache directory | `/app/cache` |
| `EMDEX_CACHE_RETENTION_DAYS` | Days before purging deleted files from cache | `30` |
| `EMDEX_CACHE_VACUUM_INTERVAL_HOURS` | Hours between SQLite VACUUM cycles | `24` |

## Webhooks (P44)

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_WEBHOOK_URL` | Webhook URL called after namespace indexing | (optional) |

## Extraction Cache (P50)

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_EXTRACT_CACHE_ENABLED` | Enable extraction result caching | `false` |
| `EMDEX_EXTRACT_CACHE_REDIS_URL` | Redis URL for extraction cache | `redis://redis:6379` |
| `EMDEX_EXTRACT_CACHE_TTL` | Extraction cache TTL | `720h` |

## RAGAS Eval Sidecar

| Variable | Description | Default |
|----------|-------------|---------|
| `EMDEX_RAGAS_URL` | RAGAS sidecar URL used by `emdex eval --ragas` | `http://localhost:8006` |
| `RAGAS_LLM_PROVIDER` | Sidecar LLM judge provider: `openai` or `google` | `openai` |
| `RAGAS_MODEL` | LLM model used by RAGAS judge | `gpt-4o-mini` (openai) / `gemini-2.0-flash` (google) |
| `RAGAS_PORT` | RAGAS sidecar listen port | `8006` |
