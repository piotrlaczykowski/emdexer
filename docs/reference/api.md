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

Search and chat requests specify a namespace via `?namespace=` or `X-Emdex-Namespace` header. The gateway enforces that the requested namespace is in the caller's authorized list. Use `namespace=*` for global search across all authorized namespaces.

---

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
    {"role": "user", "content": "Summarize the Q1 budget report."}
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

#### List Nodes
- **Method:** `GET`
- **Path:** `/nodes`
- **Response:** Array of registered nodes with namespaces, protocol, health status, and last heartbeat.

#### Register Node
- **Method:** `POST`
- **Path:** `/nodes/register`
- **Body:** `{"id": "node-1", "url": "http://...", "namespaces": ["hr"], "protocol": "s3", "health_status": "healthy"}`

#### Deregister Node
- **Method:** `DELETE`
- **Path:** `/nodes/deregister/<id>`

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
