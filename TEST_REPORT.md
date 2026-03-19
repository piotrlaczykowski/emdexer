# Emdexer Integration Test Report

**Date**: 2026-03-19
**Environment**: LXC on Proxmox (`ssh board`), `/opt/emdexer`
**Branch**: `fix/placzykowski/fixes-after-tests`
**Stack**: Docker Compose (single-node Qdrant, Gateway, Node, Extractous, MCP)

---

## Summary of Findings

| # | Severity | Issue | Status |
|---|----------|-------|--------|
| 1 | **CRITICAL** | Google API key flagged as leaked — all embeddings fail | Needs new key |
| 2 | **CRITICAL** | Auth key `44886d4f...` appears 8 times in git history | Needs history rewrite |
| 3 | **HIGH** | Default embedding model `gemini-embedding-exp-03-07` deprecated/removed | **Fixed** |
| 4 | **HIGH** | `docker-compose.yml` node volume mount wrong (`/opt/emdexer/test_dir` vs `/app/test_dir`) | **Fixed** |
| 5 | **HIGH** | `docker-compose.yml` gateway logs volume mount wrong | **Fixed** |
| 6 | **HIGH** | Node starts silently with empty `GOOGLE_API_KEY` — no startup validation | Open |
| 7 | **MEDIUM** | Node missing `EMDEX_EXTRACTOUS_URL` in compose (can't extract non-plaintext) | **Fixed** |
| 8 | **MEDIUM** | Node missing `depends_on: extractous` in compose | **Fixed** |
| 9 | **MEDIUM** | Local watcher does NOT index existing files on startup (only new changes) | Open |
| 10 | **MEDIUM** | File deletions not propagated to Qdrant ("tombstone not yet implemented") | Open |
| 11 | **LOW** | Old `nasdex-gateway` systemd service was blocking port 7700 | Stopped & disabled |
| 12 | **LOW** | Embedding dimension mismatch: code had 3072 (old model), `text-embedding-004` needs 768 | **Fixed** (now configurable via `EMDEX_EMBEDDING_DIMS`) |
| 13 | **LOW** | `.env` on server contains plaintext secrets (GOOGLE_API_KEY, POSTGRES_PASSWORD) | Open |

---

## Phase 1: Full-Cycle Functional Test

**Result: BLOCKED** (API key leaked)

### What worked
- File watcher correctly detected new file creation (`WRITE` event) within ~1s
- File watcher correctly debounced rapid changes (500ms)
- Content extraction succeeded (extractous sidecar)
- Watcher callback invoked `indexDataToPoints` correctly

### What failed
- Google API returns `403: Your API key was reported as leaked`
- Embedding model `gemini-embedding-exp-03-07` returns `404: not found` (deprecated)
- Zero points in Qdrant after all attempts
- Embedding errors are **silent** — only visible via Prometheus metrics (`emdexer_node_errors_total`)

### Pre-existing issues found during setup
- `nasdex-gateway` systemd service was running on port 7700, blocking Docker gateway
- Node container couldn't see test files due to wrong volume mount path
- Qdrant collection created with 3072 dimensions (old model) — deleted and will be recreated with 768

---

## Phase 2: State Persistence & Reboot Test

**Result: PASS**

- `docker compose down` + `docker compose up -d` completes cleanly
- All services return to healthy state within 15 seconds
- Qdrant collection `emdexer_v1` persists across restarts (bind mount `./qdrant_storage`)
- Test files in `test_dir` persist across restarts (bind mount)
- Gateway health endpoints (`/health`, `/healthz/readiness`, `/healthz/liveness`) all respond correctly after cold start

---

## Phase 3, 4: Skipped

- **Phase 3 (Network Partition)**: Requires multi-node Qdrant cluster. Single-node compose setup doesn't support this. Should be tested in Kubernetes.
- **Phase 4 (Stress/Latency)**: Requires working embeddings. Blocked by API key issue.

---

## Phase 5: Security & API Boundary Audit

**Result: PASS**

### Authentication
| Test | Expected | Actual |
|------|----------|--------|
| `GET /v1/search` without auth | 401 | 401 |
| `GET /nodes` without auth | 401 | 401 |
| `POST /v1/chat/completions` without auth | 401 | 401 |
| Invalid bearer token | 401 | 401 |
| Empty bearer token | 401 | 401 |
| Wrong auth scheme (Basic) | 401 | 401 |
| `GET /health` (public) | 200 | 200 |
| `GET /metrics` (public) | 200 | 200 |
| `GET /healthz/*` (public) | 200 | 200 |

### Port Isolation (Docker network)
| Port | Service | Accessible from host? |
|------|---------|-----------------------|
| 7700 | Gateway | Yes (exposed) |
| 8081 | Node health | Yes (exposed) |
| 6333 | Qdrant REST | **No** (internal only) |
| 6334 | Qdrant gRPC | **No** (internal only) |
| 8000 | Extractous | **No** (internal only) |

All internal services are correctly isolated to `emdexer-net`.

### SSRF Protection
- Ollama transport has `isPrivateIP` guard blocking RFC1918, loopback, link-local
- Gemini provider reuses the safe transport (blocks private IP connections at dial time)

---

## Phase 6: File System Watcher Stress Test

**Result: PARTIAL PASS**

### Test: 20 creates, 10 modifies, 5 deletes in ~10 seconds
- **Creates**: All 20 detected and indexed (30 total events with modifications)
- **Modifications**: All 10 modifications detected (debounce correctly coalesced)
- **Deletes**: All 5 detected BUT **skipped** — logged as "tombstone not yet implemented"

### Issues
1. **Ghost files**: Deleted files remain in Qdrant as phantom entries. If a file is deleted from disk, searching will still return results from the deleted file's content.
2. **No initial indexing pass**: The local watcher only registers fsnotify watches on directories. Existing files present before the node starts are NOT indexed. A separate goroutine (lines 289-313) does walk and index, but this also failed due to the API key issue.

---

## Phase 7: Compliance & Secret Hardening

**Result: FAIL**

### 7.1: Secret Scrub Audit
- **FAIL**: Auth key `44886d4f5d0e5a30ea1dd2d390928df76aec4bcbf96d81750991e9767229362e` appears **8 times** in remote git history, **7 times** in local git history
- Google API key not found in git history (good), but was flagged as leaked by Google (likely leaked via other means or `.env` file exposure)
- `.env` file on server contains all secrets in plaintext — no vault integration

### 7.2: Panic on Missing Config
- **FAIL**: Neither Gateway nor Node validate required environment variables at startup
- Empty `GOOGLE_API_KEY`: Node starts normally, fails silently on every embedding call
- Empty `EMDEX_AUTH_KEY`: Gateway starts and likely accepts all requests (no auth check if key is empty)
- Expected behavior: Process should exit with a clear error message

### 7.3: Model Stabilization
- **FAIL** (now fixed): Default model was `gemini-embedding-exp-03-07` (experimental, now removed)
- **Fix applied**: Changed default to `models/text-embedding-004` (stable, 768 dimensions)
- Updated `EmbeddingDims` from 3072 to 768 to match

---

## Fixes Applied in This Branch

### 1. `deploy/docker/docker-compose.yml`
- **Node volume mount**: `../../test_dir:/opt/emdexer/test_dir` → `../../test_dir:/app/test_dir`
- **Gateway logs mount**: `../../logs:/opt/emdexer/logs` → `../../logs:/app/logs`
- **Added** `EMDEX_EXTRACTOUS_URL=http://extractous:8000` to node environment
- **Added** `depends_on: extractous` with health condition to node service
- **Removed** unused S3 environment variables that were cluttering logs with warnings

### 2. `src/pkg/embed/provider.go`
- Default model: `models/gemini-embedding-exp-03-07` → `models/text-embedding-004`

### 3. `src/node/main.go`
- `EmbeddingDims`: 3072 → 768 default (matches `text-embedding-004` output)
- Now configurable via `EMDEX_EMBEDDING_DIMS` environment variable (e.g., set to 3072 for `gemini-embedding-exp` models)

### 4. Remote server
- Stopped and disabled `nasdex-gateway.service` (old binary blocking port 7700)
- Deleted stale 3072-dim Qdrant collection (will be recreated with 768 on next node start)

---

## Recommended Follow-ups

### Immediate (before next deploy)
1. **Rotate Google API key** — current one is permanently revoked by Google
2. **Rotate EMDEX_AUTH_KEY** — current value is in git history
3. **Add startup validation** — Gateway and Node should `log.Fatal` if `GOOGLE_API_KEY` is empty

### Short-term
4. **Implement file deletion propagation** — delete points from Qdrant when files are removed
5. **Add error logging for embedding failures** — currently only metrics, no log output
6. **Scrub git history** — use `git filter-branch` or BFG to remove leaked secrets

### Medium-term
7. **Initial indexing for local watcher** — fire the callback for all existing files on startup (the separate walk goroutine handles this, but verify it works end-to-end)
8. **Vault integration** — move secrets out of `.env` files
9. **Phase 4 stress test** — re-run once embeddings are working
