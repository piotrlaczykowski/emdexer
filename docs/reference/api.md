# Emdexer API Reference

**Version:** v1
**Base URL:** `http://<gateway-host>:7700`

## Authentication

All endpoints (except `/health*` and `/metrics`) require an `Authorization: Bearer <token>` header.

The gateway supports two authentication methods in priority order:

### 1. OIDC/JWT (when `OIDC_ISSUER` is configured)

Pass a JWT obtained from your OIDC provider (Keycloak, Auth0, Okta, Google, etc.):
```
Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...
```
The gateway validates the JWT signature via JWKS, extracts the user's groups from the `OIDC_GROUPS_CLAIM` (default: `groups`), and maps them to authorized namespaces via `EMDEX_GROUP_ACL`.

### 2. Static API Keys (fallback)

If the token is not a valid JWT, the gateway tries static key matching:
- **Simple mode:** `EMDEX_AUTH_KEY` — single token, wildcard namespace access.
- **Multi-key mode:** `EMDEX_API_KEYS` JSON map — per-key namespace ACL (e.g., `{"sk-hr": ["hr", "legal"]}`).

### Namespace Authorization

Search and chat requests specify a namespace via `?namespace=` or `X-Emdex-Namespace` header. The gateway enforces that the requested namespace is in the caller's authorized list.

**Wildcard / global search:** Pass `namespace=*` (or the alias `namespace=__global__`) to search across all namespaces the caller is authorized for. The gateway fans out the query to every registered node namespace in parallel and merges results using Reciprocal Rank Fusion (RRF). Partial failures (individual nodes that time out or error) are reported in the `partial_failures` field — the response still contains results from all healthy nodes.

---

## Endpoints

### 1. Search (Hybrid RAG)
Returns the most relevant chunks from the indexed documents across all or specific namespaces.

- **Method:** `GET`
- **Path:** `/v1/search`
- **Headers:**
  - `Authorization: Bearer <your-auth-key>`
- **Query Parameters:**
  - `q`: Search query string (Required)
  - `namespace`: Filter by namespace. Use `*` or `__global__` to search all authorized namespaces in parallel. (Required)
  - `limit`: (Optional) Max results to return (overrides `EMDEX_SEARCH_LIMIT`)

#### Example Request
```bash
curl -H "Authorization: Bearer my-secret-token" \
     "http://localhost:7700/v1/search?q=budget&namespace=finance&limit=3"
```

- **Response (single namespace):**
```json
{
  "query": "What is the onboarding process for new employees?",
  "results": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "score": 0.98,
      "payload": {
        "text": "New employees must complete the onboarding checklist within their first week, including setting up VPN access and completing the security training module.",
        "path": "/data/docs/hr/onboarding.md",
        "namespace": "hr"
      }
    }
  ]
}
```

- **Response (global search — `namespace=*`):**
```json
{
  "query": "deployment guide",
  "namespaces_searched": ["docs", "code", "hr"],
  "results": [
    {
      "id": "abc123",
      "score": 0.95,
      "payload": {
        "text": "See INSTALL.md for step-by-step deployment instructions.",
        "path": "/docs/getting-started/installation.md",
        "namespace": "docs",
        "source_namespace": "docs"
      }
    }
  ]
}
```

> **`partial_failures`** — If one or more namespaces fail (timeout, node unreachable), the field is included:
> ```json
> { "partial_failures": ["code"], "namespaces_searched": ["docs", "hr"], "results": [...] }
> ```
> Results from healthy namespaces are still returned. Callers should surface this field to the user when present.

### 2. Chat (OpenAI Compatible)
OpenAI-compatible chat completions with integrated Multi-Hop RAG.

- **Method:** `POST`
- **Path:** `/v1/chat/completions`
- **Headers:**
  - `Authorization: Bearer <your-auth-key>`
  - `X-Emdex-Namespace: <namespace>` (Required)
  - `Content-Type: application/json`

#### Example Request
```bash
curl -X POST "http://localhost:7700/v1/chat/completions" \
     -H "Authorization: Bearer my-secret-token" \
     -H "X-Emdex-Namespace: docs" \
     -H "Content-Type: application/json" \
     -d '{
       "model": "emdexer-v1",
       "messages": [{"role": "user", "content": "How do I install emdexer?"}]
     }'
```

- **Request Body:**
```json
{
  "model": "emdexer-v1",
  "messages": [
    {"role": "user", "content": "Summarize the Q1 budget report."}
  ],
  "stream": false,
  "max_context": 5
}
```
> **Note**: Default search and RAG limits are globally configurable via `EMDEX_SEARCH_LIMIT` and `EMDEX_CHAT_LIMIT`. The `max_context` field in chat requests can override the global chat limit for specific calls.

#### Error Codes
| Code | Meaning | Description |
|------|---------|-------------|
| 400 | Bad Request | Missing `X-Emdex-Namespace` or invalid JSON body. |
| 401 | Unauthorized | Missing or invalid `Authorization` header. |
| 403 | Forbidden | Token is valid but user lacks access to the requested namespace. |
| 404 | Not Found | Requested endpoint does not exist. |
| 500 | Internal Server Error | Gateway or backend (Qdrant/Gemini) failure. |

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
        "content": "Based on the documents in the 'finance' namespace, the Q1 budget report indicates a 12% increase in operational costs driven by infrastructure upgrades."
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

### 4. Identity (`/v1/whoami`)

Returns the current caller's identity, auth method, and authorized namespaces.

- **Method:** `GET`
- **Path:** `/v1/whoami`
- **Headers:**
  - `Authorization: Bearer <token>` (OIDC JWT or static key)
- **Response:**
```json
{
  "auth_type": "oidc",
  "subject": "user@example.com",
  "email": "user@example.com",
  "groups": ["hr-admins", "finance"],
  "namespaces": ["hr", "hiring", "finance"]
}
```

For static API key auth, `auth_type` is `"api-key"` and `subject`/`email`/`groups` are empty.

### 5. Node Management

Node endpoints do not require the `Authorization` header — they are protected by network policy (should not be exposed externally).

#### List Nodes
- **Method:** `GET`
- **Path:** `/nodes`
- **Response:**
```json
[
  {
    "id": "node-docs",
    "url": "http://node-docs:8081",
    "namespaces": ["docs"],
    "protocol": "local",
    "health_status": "healthy",
    "last_heartbeat": "2026-03-19T12:34:56Z"
  },
  {
    "id": "node-code",
    "url": "http://node-code:8082",
    "namespaces": ["code"],
    "protocol": "local",
    "health_status": "healthy",
    "last_heartbeat": "2026-03-19T12:34:55Z"
  }
]
```

#### Register / Heartbeat Node
Nodes call this on startup and periodically to keep their entry alive in the registry.

- **Method:** `POST`
- **Path:** `/nodes/register`
- **Body:**
```json
{
  "id": "node-docs",
  "url": "http://node-docs:8081",
  "namespaces": ["docs"],
  "protocol": "local",
  "health_status": "healthy"
}
```
- **Response:** `200 OK` with `{"status": "registered"}`

The gateway refreshes its namespace topology from the registry every 30 seconds. Namespaces discovered this way become available for `namespace=*` fan-out.

#### Deregister Node
- **Method:** `DELETE`
- **Path:** `/nodes/deregister/<id>`
- **Response:** `200 OK` with `{"status": "deregistered"}`

### 6. Observability & Health

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
