# Emdexer API Reference

**Version:** v1  
**Base URL:** `http://<gateway-host>:7700`  
**Authentication:** Header `Authorization: Bearer <Emdexer_AUTH_KEY>`

## Endpoints

### 1. Search (Hybrid RAG)
Returns the most relevant chunks from the indexed documents across all or specific namespaces.

- **Method:** `GET`
- **Path:** `/v1/search`
- **Headers:** 
  - `Authorization: Bearer <your-auth-key>`
- **Query Parameters:**
  - `q`: Search query string
  - `namespace`: (Optional) Filter by namespace
  - `limit`: (Optional) Max results to return (overrides `EMDEX_SEARCH_LIMIT`)
- **Response:**
```json
{
  "query": "What is the policy on turkey for Krysia?",
  "results": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "score": 0.98,
      "payload": {
        "text": "Krysia really likes turkey and should be given some every morning.",
        "path": "/opt/emdexer/test_dir/cats.txt",
        "namespace": "alpha"
      }
    }
  ]
}
```

### 2. Chat (OpenAI Compatible)
OpenAI-compatible chat completions with integrated Multi-Hop RAG.

- **Method:** `POST`
- **Path:** `/v1/chat/completions`
- **Headers:** 
  - `Authorization: Bearer <your-auth-key>`
  - `Content-Type: application/json`
- **Request Body:**
```json
{
  "model": "emdexer-v1",
  "messages": [
    {"role": "user", "content": "Tell me about Krysia's diet."}
  ],
  "stream": false,
  "max_context": 5
}
```
> **Note**: Default search and RAG limits are globally configurable via `EMDEX_SEARCH_LIMIT` and `EMDEX_CHAT_LIMIT`. The `max_context` field in chat requests can override the global chat limit for specific calls.
- **Response:**
```json
{
  "id": "chatcmpl-rag",
  "object": "chat.completion",
  "created": 1710408000,
  "model": "emdexer-v1",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Based on the documents in the 'alpha' namespace, Krysia's diet primarily consists of turkey, which she enjoys every morning."
      },
      "finish_reason": "stop"
    }
  ]
}
```

### 3. Daily Delta
Retrieve and summarize files indexed in the last 24 hours.

- **Method:** `GET`
- **Path:** `/v1/daily-delta`
- **Headers:** 
  - `Authorization: Bearer <your-auth-key>`
- **Response:**
```json
{
  "count": 2,
  "files": [{"path": "/docs/invoice_1.pdf"}, {"path": "/docs/manual.md"}],
  "summary": "You added a new invoice for services and a manual for the new server."
}
```

### 4. Observability & Health

#### Metrics (Prometheus)
- **Method:** `GET`
- **Path:** `/metrics`
- **Description:** Exposes search latency, embedding latency, and HTTP request counts in Prometheus format.

#### Liveness
- **Method:** `GET`
- **Path:** `/healthz/liveness`
- **Response:** `{"status": "UP"}`

#### Readiness
- **Method:** `GET`
- **Path:** `/healthz/readiness`
- **Response:** `{"status": "UP"}` (Returns 503 if dependencies like Qdrant are unreachable)

#### Startup
- **Method:** `GET`
- **Path:** `/healthz/startup`
- **Response:** `{"status": "STARTED"}`
