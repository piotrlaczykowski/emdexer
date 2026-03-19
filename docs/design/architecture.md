# Emdexer Architecture

> "Why does it work this way?" — This document answers that question.

---

## 1. High-Level Overview

```
┌─────────────────────────────────────────────────────┐
│                   Consumers                         │
│  (OpenClaw MCP  |  Telegram Bot  |  OpenAI client)  │
└────────────────────┬────────────────────────────────┘
                     │  OIDC JWT or Bearer Token
                     ▼
          ┌──────────────────────┐
          │   emdex-gateway      │   :7700
          │  ─────────────────   │
          │  /v1/search          │
          │  /v1/chat/completions│──► Gemini LLM (generateContent)
          │  /v1/daily-delta     │
          │  /nodes/register     │◄── Node heartbeat (EMDEX_GATEWAY_URL)
          │  /metrics            │
          └──────────┬───────────┘
                     │  Qdrant gRPC  :6334
                     ▼
          ┌──────────────────────┐
          │      Qdrant          │   Vector DB — single source of truth
          │  (collection per ns) │
          └──────────────────────┘
                     ▲
        ┌────────────┼──────────────────┐
        │            │                  │
   ┌────┴───┐  ┌─────┴────┐  ┌─────────┴──┐  ┌────────┐
   │ Local  │  │ SMB/NFS  │  │    SFTP    │  │   S3   │
   │  Node  │  │   Node   │  │    Node    │  │  Node  │
   └────────┘  └──────────┘  └────────────┘  └────────┘
 emdex-node
 :8081 health
```

---

## 2. Dual-Binary Runtime Model

Emdexer ships as **two independent binaries**:

| Binary          | Role                            | Key env vars                                |
|-----------------|---------------------------------|---------------------------------------------|
| `emdex-gateway` | API + search + RAG              | `GOOGLE_API_KEY`, `QDRANT_HOST`, `EMDEX_AUTH_KEY`, `EMDEX_REGISTRY_FILE` |
| `emdex-node`    | Filesystem indexing agent       | `EMDEX_GATEWAY_URL`, `EMDEX_GATEWAY_AUTH_KEY`, `NODE_TYPE`, `EMDEX_NAMESPACE` |

A third binary, `emdex` (the CLI), wraps both for management operations (init, status, start via Docker Compose).

### Why two binaries?

**Operational isolation**: A node running in a DMZ or on a NAS appliance does not need — and should not have — Gemini API keys or Qdrant admin access. Splitting removes that attack surface.

**Independent scaling**: Multiple nodes (different VFS sources, different namespaces) can register against a single gateway. The gateway is horizontally scalable; nodes are single-tenant daemons.

**Independent upgrades**: A gateway can be updated without restarting nodes, and vice versa. There is no shared in-process state.

### Build System

All binaries are built statically linked (`CGO_ENABLED=0`) for maximum portability across Linux distros (Debian, RHEL, Alpine, Raspberry Pi OS, etc.):

```bash
# Build both binaries
make all

# Or individually
make gateway    # → bin/emdex-gateway
make node       # → bin/emdex-node
make cli        # → bin/emdex
```

The `Makefile` sets `GOFLAGS := CGO_ENABLED=0 GOOS=linux GOARCH=amd64` and passes `-ldflags="-s -w -extldflags=-static"`.

---

## 3. Configuration Decoupling

Each binary reads only its own environment variables. In a bare-metal deployment, configs are stored in separate files:

- **Gateway**: `/etc/emdexer/gateway.env`
- **Node**: `/etc/emdexer/node.env`

### Gateway Environment

| Variable                  | Required | Default           | Description                                      |
|---------------------------|----------|-------------------|--------------------------------------------------|
| `GOOGLE_API_KEY`          | ✅       | —                 | Gemini API key (embeddings + LLM calls)          |
| `QDRANT_HOST`             | ✅       | `localhost:6334`  | Qdrant gRPC endpoint                             |
| `EMDEX_QDRANT_COLLECTION` |          | `emdexer_v1`      | Qdrant collection name                           |
| `EMDEX_AUTH_KEY`          | ✅*      | —                 | Simple-mode bearer token                         |
| `EMDEX_API_KEYS`          | ✅*      | —                 | Advanced multi-key JSON map                      |
| `EMBED_PROVIDER`          |          | `gemini`          | `gemini` or `ollama`                             |
| `OLLAMA_HOST`             |          | —                 | Ollama URL (when `EMBED_PROVIDER=ollama`)         |
| `EMDEX_REGISTRY_FILE`     |          | `./nodes.json`    | Persistent node registry path                   |
| `EMDEX_PORT`              |          | `7700`            | HTTP listen port                                 |
| `EMDEX_AUDIT_LOG_FILE`    |          | `logs/audit.json` | Path to the audit log file                       |
| `EMDEX_SEARCH_LIMIT`      |          | `10`              | Max results returned in search                   |
| `EMDEX_CHAT_LIMIT`        |          | `5`               | Max results considered during RAG hops           |
| `EMDEX_GEMINI_MODEL`      |          | `gemini-embedding-2-preview`| Gemini model for RAG/Chat                        |
| `EMDEX_STRICT_NAMESPACE`  |          | `false`           | Require namespace header for all requests        |

| `OIDC_ISSUER`             |          | —                 | OIDC provider URL. Enables JWT auth when set.    |
| `OIDC_CLIENT_ID`          | ✅**     | —                 | Client ID for JWT audience validation            |
| `OIDC_GROUPS_CLAIM`       |          | `groups`          | JWT claim containing group memberships           |
| `EMDEX_GROUP_ACL`         |          | —                 | JSON map: OIDC group → namespace list            |

*Either `EMDEX_AUTH_KEY` or `EMDEX_API_KEYS` must be set.
**Required when `OIDC_ISSUER` is set.

### Node Environment

| Variable                  | Required | Default           | Description                                      |
|---------------------------|----------|-------------------|--------------------------------------------------|
| `EMDEX_GATEWAY_URL`       | ✅       | —                 | Gateway URL for self-registration                |
| `EMDEX_GATEWAY_AUTH_KEY`  | ✅       | —                 | Auth key to authenticate with the gateway        |
| `EMDEX_NAMESPACE`         | ✅       | `default`         | Namespace tag on all indexed vectors             |
| `QDRANT_HOST`             | ✅       | `localhost:6334`  | Qdrant gRPC endpoint (direct write)              |
| `EMDEX_QDRANT_COLLECTION` |          | `emdexer_v1`      | Qdrant collection name                           |
| `NODE_TYPE`               |          | `local`           | `local` / `smb` / `sftp` / `nfs` / `s3`         |
| `NODE_ROOT`               |          | `./test_dir`      | Root path to index                               |
| `EMBED_PROVIDER`          |          | `gemini`          | `gemini` or `ollama`                             |
| `GOOGLE_API_KEY`          | ✅*      | —                 | Gemini key (when `EMBED_PROVIDER=gemini`)         |
| `EMDEX_GEMINI_MODEL`      |          | `gemini-embedding-2-preview`| Gemini model for embeddings                      |
| `EMDEX_EXTRACTOUS_URL`    |          | `http://localhost:8000/extract` | Extractous sidecar URL             |
| `EMDEX_POLL_INTERVAL`     |          | `60s`             | Remote VFS poll interval                         |
| `EMDEX_CACHE_DIR`         |          | `./cache`         | SQLite metadata cache directory                  |
| `EMDEX_QUEUE_DB`          |          | `queue.db`        | Indexing queue SQLite path                       |
| `NODE_HEALTH_PORT`        |          | `8081`            | Health/metrics HTTP port                         |
| `EMDEX_BATCH_SIZE`        |          | `100`             | Batch size for batch indexing                    |
| `EMDEX_MAX_FILE_SIZE`     |          | `50MB`            | Maximum file size for extraction                 |
| `EMDEX_MAX_ARCHIVE_ENTRY_SIZE` |     | `10MB`            | Max size for extracted archive entry             |

*Required when `EMBED_PROVIDER=gemini` (default).

### Sidecar Environment

| Variable              | Required | Default | Description                |
|-----------------------|----------|---------|----------------------------|
| `EMDEX_SIDECAR_PORT`  |          | `8000`  | Sidecar HTTP listen port   |


---

## 4. The VFS Abstraction — Zero-Mount Architecture

Emdexer's core differentiator is **Zero-Mount**: indexing nodes deploy directly where data lives. No `mount -t cifs`, no FUSE layers, no data copying. Only vector embeddings travel to Qdrant. This eliminates network bottlenecks for bulk reads and removes the operational burden of maintaining mount points across heterogeneous storage.

Emdexer must index files from heterogeneous sources: local disk, Samba shares, SFTP servers, NFS exports, S3 buckets, and eventually USB drives. Rather than writing a separate indexer for each protocol, all storage backends implement the `vfs.FileSystem` interface:

```go
type FileSystem interface {
    Open(name string) (File, error)
    ReadDir(name string) ([]fs.DirEntry, error)
    Stat(name string) (fs.FileInfo, error)
    Close() error
}
```

The `Indexer` in `pkg/node/` operates exclusively on `vfs.FileSystem`. Adding a new backend (e.g., Azure Blob) requires only implementing four methods — no changes to the indexing or embedding pipeline.

### VFS Backends

| Backend | `NODE_TYPE` | Implementation | Connection Model |
|---------|------------|----------------|------------------|
| Local FS | `local` | `vfs/os.go` | Direct `os.*` calls |
| SMB/CIFS | `smb` | `vfs/smb.go` | NTLM session + share mount |
| SFTP | `sftp` | `vfs/sftp.go` | SSH + SFTP client |
| NFS | `nfs` | `vfs/nfs.go` | NFS mount + Unix auth |
| **S3/MinIO** | **`s3`** | **`vfs/s3.go`** | **MinIO-Go v7 HTTP client** |

### S3 Zero-Mount Streaming

The S3 backend (`vfs/s3.go`) uses MinIO-Go v7 which is compatible with AWS S3, MinIO, DigitalOcean Spaces, and Wasabi.

**Data flow:** `S3 GetObject → *minio.Object (in memory) → Extractous → SmartChunk → Embed → Qdrant Upsert`

No local disk writes at any point. MinIO's `*minio.Object` implements `io.Reader`, `io.Seeker`, and `io.ReaderAt` using S3 range requests internally — this means delta detection (partial hash of first+last 1 MB) works without downloading the full object.

The S3 transport uses `safenet.NewSafeTransport()` to prevent SSRF via malicious `S3_ENDPOINT` values.

### Why not use `io/fs.FS`?

`io/fs.FS` is read-only and does not model stateful connections (SMB sessions, SFTP connections). Our interface adds `Close()` to ensure resources are cleaned up, and we control the `ReadDir` contract to handle protocol-specific pagination.

---

## 5. Authentication & Identity

### Dual-Auth Middleware

The gateway uses a **tiered authentication strategy** implemented in `src/pkg/auth/auth.go`. Every request to a protected endpoint goes through this middleware:

```
Request with Authorization: Bearer <token>
  │
  ├─ Step 1: OIDC JWT validation (if OIDC_ISSUER is configured)
  │   ├─ Valid JWT → extract UserClaims (sub, email, groups)
  │   │   → resolve namespaces via GroupACL → proceed
  │   └─ Invalid JWT → fall through to Step 2
  │
  └─ Step 2: Legacy static key validation
      ├─ Match in EMDEX_API_KEYS → use per-key namespace ACL → proceed
      ├─ Match EMDEX_AUTH_KEY → wildcard access → proceed
      └─ No match → 401 Unauthorized
```

Both paths inject `UserClaims` into the request context (via `auth.WithUserClaims`), so handlers can always call `auth.GetUserClaims(r)` to identify the caller regardless of auth method.

### OIDC/JWT (Enterprise Identity)

When `OIDC_ISSUER` is set, the gateway discovers the provider's JWKS endpoint via OpenID Connect Discovery and validates JWT signatures automatically (using `github.com/coreos/go-oidc/v3`). Key rotation is handled transparently.

| Variable | Description |
|----------|-------------|
| `OIDC_ISSUER` | Provider URL (e.g., `https://keycloak.internal/realms/emdexer`) |
| `OIDC_CLIENT_ID` | Client ID for audience validation |
| `OIDC_GROUPS_CLAIM` | JWT claim containing group memberships (default: `groups`) |

**Fail-secure**: If the OIDC provider is unreachable at startup, the gateway calls `log.Fatalf` and refuses to start.

### Group-Based ACLs

OIDC groups are mapped to namespace lists via `EMDEX_GROUP_ACL`:

```
EMDEX_GROUP_ACL='{"hr-admins": ["hr", "hiring"], "engineers": ["*"], "finance": ["finance"]}'
```

- A user in groups `["hr-admins", "finance"]` can access namespaces `["hr", "hiring", "finance"]`.
- A user in group `["engineers"]` gets wildcard access to all namespaces.
- A user with no matching groups gets zero namespaces → 403 on all queries.

The namespace resolution uses the same `search.ResolveNamespaces` function as static keys — the intersection logic is shared.

### Static API Keys (Legacy / Automation)

1.  **Simple Mode**: Single shared bearer token via `EMDEX_AUTH_KEY`. Grants wildcard namespace access.
2.  **Advanced Multi-Key Mode**: Per-key namespace ACL via `EMDEX_API_KEYS` JSON map (e.g., `{"sk-hr": ["hr", "legal"]}`).

Static keys are tried only after OIDC validation fails. This means a deployment can serve both OIDC-authenticated human users and static-key-authenticated automation bots simultaneously.

### Node → Gateway (Registration)
Nodes call `POST /nodes/register` with `Authorization: Bearer <EMDEX_GATEWAY_AUTH_KEY>`. The auth key on the node must match one of the gateway's valid keys.

### Node → Qdrant (Internal)
Nodes write directly to Qdrant via gRPC. This is an internal network path — Qdrant is not exposed externally. In K8s, this is enforced via NetworkPolicy.

---

## 6. Namespace Semantics

Every indexed point carries a `namespace` payload field. Namespaces are **user-defined strings** — typically mapped to a data source or user group (e.g., `emdexer`, `finance-team`, `personal`).

### Invariants (enforced in code)
1. The `/v1/search` endpoint accepts `?namespace=` and passes it as a Qdrant `must` filter.
2. The `/v1/chat/completions` endpoint **requires** `X-Emdex-Namespace` header or `?namespace=` query param. Missing namespace → `400 Bad Request`. This prevents accidental cross-tenant context bleed during RAG.
3. Both RAG hops (Hop 1 initial search + Hop 2 refined search) use the same namespace. The LLM refinement loop cannot escape its namespace.

---

## 7. EmbedProvider Interface

Emdexer was originally hard-locked to the Gemini embedding API. The `EmbedProvider` interface breaks this coupling:

```go
type EmbedProvider interface {
    Embed(text string) ([]float32, error)
    Name() string
}
```

**`GeminiProvider`** — default, calls Google Generative Language API.
**`OllamaProvider`** — fully implemented (Phase 15.5 complete). Calls a local Ollama instance for zero-external-call, air-gapped deployments.

Switch via `EMBED_PROVIDER=ollama`. Vector dimensions must match the Qdrant collection config; changing providers on an existing collection requires re-indexing.

The embed provider is configured **independently** on the gateway (for query embedding) and on each node (for document embedding). A deployment can have a Gemini-backed gateway and an Ollama-backed node, or vice versa.

---

## 8. Real-Time Watcher & Remote VFS Poller (P4+)

Local nodes use `pkg/watcher/Watcher` (fsnotify-backed) to detect filesystem changes without polling.

**Remote VFS (SMB/SFTP/NFS)** nodes use a background poller with a SQLite-backed metadata cache (`emdex_cache.db`):

```
Poller Loop (Interval = EMDEX_POLL_INTERVAL)
    → Walk Remote VFS
    → Compare (size, mtime) with SQLite Cache
    → If changed/new: index & upsert (UUID v5 stable ID)
    → If missing in VFS but in Cache: delete points from Qdrant & Cache (Tombstone)
```

Key properties:
- **SQLite Cache**: Stores metadata in `EMDEX_CACHE_DIR` to avoid expensive full re-indexing of remote mounts.
- **Idempotent upserts**: point IDs are `UUID v5(NamespaceOID, path:chunkIndex)`. Re-indexing the same file produces the same ID — no duplicates accumulate.
- **Debounce**: Watcher uses 500ms to coalesce rapid writes.
- **Delete Support**: Poller detects deletions and purges vectors from Qdrant.

---

## 9. Data Flow: Query → Answer

```
Client: POST /v1/chat/completions
  Header: X-Emdex-Namespace: <ns>
  Body: { "messages": [{"role": "user", "content": "..."}] }

Gateway:
  1. Authenticate (Bearer token)
  2. Extract namespace — reject if missing
  3. Extract last user message
  4. Embed(question) → vector [float32 × 3072]
  5. Qdrant search(vector, namespace=<ns>, limit=5) → results
  6. Build context string from payloads
  7. Gemini: evaluate context sufficiency
     → If sufficient: answer directly
     → If "search:<refined>": embed refined, search again (same ns), merge context, answer
  8. Return answer (streaming or non-streaming)
```

---

## 10. Global Namespace Aggregation (Phase 15.3)

When a user sends `namespace=*` (or `namespace=__global__`), the gateway queries all authorized namespaces in parallel and merges the results.

### Fan-Out Search Orchestrator

```
Client: GET /v1/search?q=budget&namespace=*
  │
  Gateway:
  1. Resolve namespaces: intersect known topology with user's allowed namespaces
  2. If single namespace → fast path (existing direct Qdrant search)
  3. If multiple namespaces:
     a. Launch parallel goroutines via errgroup (one per namespace)
     b. Each goroutine: searchQdrant(ctx, collection, vector, limit, namespace)
     c. Timeout: EMDEX_GLOBAL_SEARCH_TIMEOUT (default 500ms)
     d. Non-fatal failures: partial results from healthy namespaces
  4. Merge via Reciprocal Rank Fusion (RRF, k=60)
  5. Return top-K with source_namespace in each result payload
```

### Reciprocal Rank Fusion (RRF)

Results from multiple namespace searches are merged using RRF. For each result, the score is:

```
score = Σ 1/(k + rank + 1)    where k=60 (standard constant)
```

Results are deduplicated by `namespace:path:chunk` composite key, sorted by RRF score descending, and truncated to the requested limit.

### Namespace Topology Map

The gateway maintains an in-memory `map[string][]string` (namespace → node IDs), refreshed every 30 seconds from the node registry. This tells the fan-out orchestrator which namespaces exist. The map is also refreshed immediately after a new node registers.

### Global RAG

When `X-Emdex-Namespace: *` is used with `/v1/chat/completions`, the context builder includes `[Source: namespace/path]` tags so the LLM can cite which namespace each fact came from.

---

## 11. Registry Persistence (renumbered)

The Node Registry stores registered nodes in `nodes.json` (path configurable via `EMDEX_REGISTRY_FILE`). Writes use an atomic temp-file swap (`nodes.json.tmp` → `nodes.json`) to prevent corruption on crash.

**HA mode**: When `EMDEX_HA_MODE=true`, the gateway requires a PostgreSQL-backed `DBNodeRegistry` (via `POSTGRES_URL`). If PostgreSQL is unreachable, the gateway **fails fatally** instead of falling back to `FileNodeRegistry`. This prevents split-brain where multiple gateway replicas maintain divergent local registries. Without HA mode, the fallback to `FileNodeRegistry` is permitted for single-replica deployments.

Why not SQLite? For the current node count (< 100), JSON is sufficient and has zero dependencies. SQLite becomes appropriate when node metadata needs querying beyond simple list/register operations.

---

## 11. Operational Boundaries

| Boundary | Mechanism | Gap |
|----------|-----------|-----|
| Consumer ↔ Gateway | OIDC JWT or Bearer token | OIDC provides per-user identity; static keys are shared |
| Namespace isolation | Qdrant filter | Not cryptographic; trust the gateway |
| Node ↔ Gateway | Bearer token (`EMDEX_GATEWAY_AUTH_KEY`) | Same trust model as consumer keys |
| Node ↔ Qdrant | Internal gRPC | NetworkPolicy required in K8s |
| Data at rest | Qdrant default | No encryption at rest |
| Data in transit | HTTP (no TLS) | TLS termination at load balancer / ingress |
| Outbound HTTP (safenet) | SSRF-protected `SafeClient` | See below |

### SSRF Protection — safenet

All outbound HTTP calls from the embedding providers (`GeminiProvider`, `OllamaProvider`) and the Extractous sidecar use `safenet.NewSafeClient()`. This client validates destination addresses before connecting, rejecting:

- **RFC 1918** private ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- **Loopback** (127.0.0.0/8, ::1)
- **Link-local** (169.254.0.0/16, fe80::/10)

This prevents SSRF attacks where a malicious file path or Ollama host URL could trick the node into probing internal services. The safenet guard is enforced at the `http.Transport` `DialContext` level — it cannot be bypassed by URL redirection.

---

## 12. Deployment Topologies

### Topology A — All-in-One (Dev / Single Machine)
```
[Machine]
  emdex-gateway  (reads /etc/emdexer/gateway.env)
  emdex-node     (reads /etc/emdexer/node.env)
  qdrant         (local Docker container)
```

### Topology B — Distributed (Enterprise)
```
[Server A — gateway machine]
  emdex-gateway  ← API consumers
  qdrant

[Server B — NAS/fileserver]
  emdex-node  (NODE_TYPE=smb, EMDEX_GATEWAY_URL=https://gateway.internal)

[Server C — another fileserver]
  emdex-node  (NODE_TYPE=local, EMDEX_NAMESPACE=finance)
```

### Topology C — Kubernetes (Helm)
```
Namespace: emdexer
  Deployment: emdex-gateway   (Helm chart: deploy/helm/emdexer-gateway)
  DaemonSet:  emdex-node      (Helm chart: deploy/helm/emdexer-node)
  StatefulSet: qdrant
```

See [INSTALL.md](../getting-started/installation.md) for step-by-step instructions.
