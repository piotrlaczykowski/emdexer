# HA Infrastructure Design — Phase 15.1 & 15.2

> **Branch:** `feat/ha-infrastructure`
> **Authors:** KIRO (Research), ROCK (Ops/Infra), ROHIT (Backend)
> **Status:** Ready for PR

---

## Overview

Phase 15.1 and 15.2 transform emdexer from a single-node tool into a clustered, highly available service. This document describes the cluster topology, component interactions, and operational considerations.

---

## Architecture Diagram

```
                        ┌────────────────────────────────────────┐
                        │        emdexer-net (bridge)            │
                        │                                        │
  Clients ──► :7700    │  ┌─────────────────────────────────┐  │
     │        Nginx    │  │    Nginx Load Balancer (:80)     │  │
     └────────────────►│  │    Round-robin + keepalive 32    │  │
                        │  └────────────┬──────────┬──────────┘  │
                        │               │          │             │
                        │  ┌────────────▼──┐  ┌───▼────────────┐│
                        │  │  gateway-1    │  │  gateway-2     ││
                        │  │  :8080        │  │  :8080         ││
                        │  │  (Go HTTP srv)│  │  (Go HTTP srv) ││
                        │  └──────┬────────┘  └───┬────────────┘│
                        │         │               │             │
                        │         └───────┬───────┘             │
                        │                 │                     │
                        │  ┌──────────────▼──────────────────┐  │
                        │  │     PostgreSQL :5432             │  │
                        │  │     Shared NodeRegistry          │  │
                        │  │     (registered_nodes table)     │  │
                        │  └─────────────────────────────────┘  │
                        │                                        │
                        │  ┌──────────┐ ┌──────────┐ ┌────────┐ │
                        │  │qdrant-1  │ │qdrant-2  │ │qdrant-3│ │
                        │  │ :6333/34 │ │ :6333/34 │ │:6333/34│ │
                        │  │ bootstrap│ │ bootstrap│ │bootstrap││
                        │  │ (leader) │ │ →qdrant-1│ │→qdrant-1││
                        │  └──────────┘ └──────────┘ └────────┘ │
                        │   P2P Raft consensus on :6335          │
                        └────────────────────────────────────────┘
```

---

## Phase 15.1 — Distributed Qdrant Clustering

### Configuration

| Setting | Value |
|---------|-------|
| Image | `qdrant/qdrant:v1.13.0` |
| Nodes | 3 (qdrant-1, qdrant-2, qdrant-3) |
| Consensus | Raft (QDRANT__CLUSTER__ENABLED=true) |
| P2P Port | 6335 |
| HTTP Port | 6333 |
| gRPC Port | 6334 |
| Bootstrap | qdrant-2 and qdrant-3 → `http://qdrant-1:6335` |

### Bootstrap Sequence

1. `qdrant-1` starts first and forms a single-node cluster (no bootstrap peer).
2. `qdrant-2` and `qdrant-3` depend on `qdrant-1:service_healthy` and join via `QDRANT__CLUSTER__BOOTSTRAP=http://qdrant-1:6335`.
3. Raft consensus is established once ≥2 nodes are live (quorum = 2 of 3).

### Volumes

Each node has a dedicated named volume for data isolation:
- `qdrant-storage-1`, `qdrant-storage-2`, `qdrant-storage-3`

### Gateway→Qdrant connectivity

Both gateway replicas currently connect to `qdrant-1:6334` (gRPC). For a production-grade setup, consider adding a round-robin DNS or internal load balancer in front of all three Qdrant gRPC ports.

> **Note:** The Qdrant Go client does not natively perform multi-node failover across cluster members. A follow-up task (Phase 15.1 hardening) should add a Qdrant-aware proxy or use Qdrant's cluster-internal forwarding.

---

## Phase 15.2 — Gateway High Availability

### Load Balancer (Nginx)

| Setting | Value |
|---------|-------|
| Image | `nginx:1.27-alpine` |
| Exposed port | `7700:80` |
| Strategy | Round-robin (default) |
| keepalive | 32 connections |
| Read timeout | 120s (accommodates slow LLM calls) |
| Connect timeout | 10s |

Nginx routes the following path prefixes to the upstream pool:
- `/v1/` — search + chat completions
- `/nodes` — registry endpoints
- `/health`, `/healthz/` — liveness/readiness probes
- `/metrics` — Prometheus scrape target
- `/nginx_status` — restricted to RFC-1918 addresses only

### Gateway Replicas

Both `gateway-1` and `gateway-2` are identical stateless replicas:
- Built from `src/gateway/Dockerfile`
- Inject `POSTGRES_URL` to switch from `FileNodeRegistry` to `DBNodeRegistry`
- Share the same PostgreSQL instance → consistent node registry across replicas
- Health-checked at `/healthz/readiness` (Qdrant gRPC probe)

### NodeRegistry Abstraction

```
NodeRegistry (interface)
├── FileNodeRegistry  — local nodes.json, mutex-safe, atomic persist (default)
└── DBNodeRegistry    — PostgreSQL, UPSERT on conflict, JSONB collections field
```

Factory logic in `newRegistry()`:
```go
if POSTGRES_URL != "" → DBNodeRegistry (with graceful fallback to File on init failure)
else                  → FileNodeRegistry
```

---

## Shared PostgreSQL Registry

### Schema

```sql
CREATE TABLE IF NOT EXISTS registered_nodes (
    id            TEXT        PRIMARY KEY,
    url           TEXT        NOT NULL,
    collections   JSONB       NOT NULL DEFAULT '[]',
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Security

- All queries use parameterized statements (`$1`, `$2`, `$3`) — **no SQL injection surface**.
- Service credentials are injected via environment variables (not hardcoded).
- Default password uses `${POSTGRES_PASSWORD:-emdexer_secret}` — **must be overridden in production**.
- PostgreSQL is not exposed outside `emdexer-net`; no host port binding.

---

## Network Security

All inter-service traffic is isolated within the `emdexer-net` bridge network:

| Traffic | Source | Destination | Port | Exposure |
|---------|--------|-------------|------|----------|
| Client API | Host | Nginx | 7700 | Public |
| HTTP (proxied) | Nginx | gateway-1/2 | 8080 | Internal only |
| gRPC (Qdrant) | gateway-1/2 | qdrant-1 | 6334 | Internal only |
| Qdrant P2P Raft | qdrant-* | qdrant-* | 6335 | Internal only |
| PostgreSQL | gateway-1/2 | postgres | 5432 | Internal only |

No Qdrant ports, PostgreSQL, or gateway ports are exposed to the host.

---

## Startup Dependency Order

```
qdrant-1 (healthy)
  └─► qdrant-2 (healthy)
  └─► qdrant-3 (healthy)
  └─► postgres (healthy)
        └─► gateway-1 (healthy)
        └─► gateway-2 (healthy)
              └─► nginx
```

---

## Operational Notes

### Scaling

- Add more gateway replicas by duplicating the `gateway-N` service block.
- Qdrant cluster requires odd quorum (3, 5, 7…) for safe Raft consensus.

### Credentials

Set these environment variables before deploying:
```bash
GOOGLE_API_KEY=...
EMDEX_AUTH_KEY=...
POSTGRES_PASSWORD=...        # override the default!
EMDEX_API_KEYS=...           # optional per-key namespace scoping
```

### Health Endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz/liveness` | Always returns 200 (process alive) |
| `GET /healthz/readiness` | Returns 200 only when Qdrant gRPC is SERVING |
| `GET /healthz/startup` | Returns 503 for first 5s after boot |
| `GET /health` | Gateway version + collection summary |

---

## Known Gaps & Future Work

| Item | Phase |
|------|-------|
| Gateway→Qdrant should load-balance across all 3 nodes | 15.1 hardening |
| TLS between gateway and Qdrant (currently `insecure.NewCredentials()`) | 15.4 |
| Per-user OIDC/AD integration | 15.4 |
| Qdrant collection replication factor configuration | 15.1 hardening |
| PostgreSQL read replica for registry reads | 15.2 hardening |

---

*Generated during Phase 15.1/15.2 HA audit — `feat/ha-infrastructure` branch.*
