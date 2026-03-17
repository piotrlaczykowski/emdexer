# Emdexer Architecture

> "Why does it work this way?" — This document answers that question.

---

## 1. High-Level Overview

```
┌─────────────────────────────────────────────────────┐
│                   Consumers                         │
│  (OpenClaw MCP  |  Telegram Bot  |  OpenAI client)  │
└────────────────────┬────────────────────────────────┘
                     │  Bearer Token (EMDEX_AUTH_KEY)
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
   ┌────┴───┐  ┌─────┴────┐  ┌─────────┴──┐
   │ Local  │  │ SMB/NFS  │  │    SFTP    │
   │  Node  │  │   Node   │  │    Node    │
   └────────┘  └──────────┘  └────────────┘
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
| `EMDEX_GEMINI_MODEL`      |          | `gemini-1.5-flash`| Gemini model for RAG/Chat                        |
| `EMDEX_STRICT_NAMESPACE`  |          | `false`           | Require namespace header for all requests        |

*Either `EMDEX_AUTH_KEY` or `EMDEX_API_KEYS` must be set.

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
| `EMDEX_GEMINI_MODEL`      |          | `gemini-1.5-flash`| Gemini model for embeddings                      |
| `EMDEX_S3_BUCKET`         | ✅**     | —                 | S3 Bucket name                                   |
| `EMDEX_S3_REGION`         | ✅**     | `us-east-1`       | S3 Region                                        |
| `EMDEX_S3_ENDPOINT`       |          | —                 | Custom S3 endpoint (MinIO/LocalStack)           |
| `EMDEX_S3_ACCESS_KEY`     | ✅**     | —                 | S3 Access Key                                    |
| `EMDEX_S3_SECRET_KEY`     | ✅**     | —                 | S3 Secret Key                                    |
| `EMDEX_S3_USE_PATH_STYLE` |          | `false`           | Use path-style addressing                        |
| `EMDEX_S3_PREFIX`         |          | —                 | S3 Prefix (folder)                               |
| `EMDEX_EXTRACTOUS_URL`    |          | `http://localhost:8000/extract` | Extractous sidecar URL             |

*Required when `EMBED_PROVIDER=gemini` (default).
**Required when `NODE_TYPE=s3`.
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

## 4. The VFS Abstraction — Why It Exists

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

### Why not use `io/fs.FS`?

`io/fs.FS` is read-only and does not model stateful connections (SMB sessions, SFTP connections). Our interface adds `Close()` to ensure resources are cleaned up, and we control the `ReadDir` contract to handle protocol-specific pagination.

---

## 5. Authentication Model

### Gateway (Consumer → Gateway)
Emdexer supports two authentication modes:

1.  **Simple Mode**: All endpoints (except `/health*` and `/metrics`) require:
    ```
    Authorization: Bearer <EMDEX_AUTH_KEY>
    ```
    The key is set at startup and is static. There is one key per gateway deployment.

2.  **Advanced Multi-Key Mode**: Support for multi-tenant or multi-team access using:
    ```
    EMDEX_API_KEYS='{"sk-admin": ["*"], "sk-hr": ["hr", "legal"]}'
    ```
    The gateway middleware extracts the key, retrieves allowed namespaces, and validates that the requested `X-Emdex-Namespace` (or `?namespace=`) is authorized for that specific key.
    - `["*"]` allows access to all namespaces.
    - Specific lists (e.g., `["hr", "legal"]`) restrict the key to those namespaces only.
    - Unauthorized namespace requests return `403 Forbidden`.

### Node → Gateway (Registration)
Nodes call `POST /nodes/register` with `Authorization: Bearer <EMDEX_GATEWAY_AUTH_KEY>`. The auth key on the node must match one of the gateway's valid keys (`EMDEX_AUTH_KEY` or a key within `EMDEX_API_KEYS`).

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
**`OllamaProvider`** — stub, ready for Phase 15.5 (local model, zero external calls).

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

## 10. Registry Persistence

The Node Registry stores registered nodes in `nodes.json` (path configurable via `EMDEX_REGISTRY_FILE`). Writes use an atomic temp-file swap (`nodes.json.tmp` → `nodes.json`) to prevent corruption on crash.

Why not SQLite? For the current node count (< 100), JSON is sufficient and has zero dependencies. SQLite becomes appropriate when node metadata needs querying beyond simple list/register operations.

---

## 11. Operational Boundaries

| Boundary | Mechanism | Gap |
|----------|-----------|-----|
| Consumer ↔ Gateway | Bearer token | Single shared key — no per-user isolation |
| Namespace isolation | Qdrant filter | Not cryptographic; trust the gateway |
| Node ↔ Gateway | Bearer token (`EMDEX_GATEWAY_AUTH_KEY`) | Same trust model as consumer keys |
| Node ↔ Qdrant | Internal gRPC | NetworkPolicy required in K8s |
| Data at rest | Qdrant default | No encryption at rest |
| Data in transit | HTTP (no TLS) | TLS termination at load balancer / ingress |

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
