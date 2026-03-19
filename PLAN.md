# Emdexer Delivery Plan
> Roadmap to a bot that knows your entire filesystem.

---

## Status Key
- ✅ **Done** — Implemented, compiles, tested end-to-end.
- 🔧 **Done (Partial)** — Core path implemented; known gaps documented.
- 🚧 **In Progress** — Work started, not production-ready.
- 📋 **Planned** — Stub or future phase; not functional.

---

## P0–P5: Core (Done)
| Phase | Description | Status | Notes |
|-------|-------------|--------|-------|
| P0 | Node-local Foundations (Walker, Chunker, Embedder) | ✅ Done | |
| P1 | Gateway Core (Node registry, /v1/search, /v1/chat/completions) | ✅ Done | Registry is now persistent (nodes.json) |
| P2 | Security & Isolation (Namespaces, Bearer Auth, Audit logging) | ✅ Done | Namespace bypass fixed in v1.0.1; `/v1/chat/completions` now enforces namespace strictly |
| P3 | MCP Interface (Context-aware tools for Claude/OpenClaw) | ✅ Done | |
| P4 | Real-time Incremental Sync (fsnotify watcher) | ✅ Done | Watcher implemented in `node/pkg/watcher/`; local nodes only. Remote VFS (SMB/SFTP/NFS) still one-shot walk only. |
| P5 | Intelligent Agent (Multi-hop RAG, query refinement) | ✅ Done | Both hops now enforce namespace |

## P6–P14: Extended Features
| Phase | Description | Status | Notes |
|-------|-------------|--------|-------|
| P6 | Cloud Storage (S3) | ✅ Done | Full S3/MinIO indexing via `NODE_TYPE=s3`. `src/pkg/vfs/s3.go` implements VFS interface via MinIO client. Uses the same `emdex-node` binary as local/SMB/SFTP/NFS — no separate binary. Polling, delta detection, extraction, embedding, and Qdrant upsert all reuse existing infrastructure. Zero-Mount: data streams S3 → Memory → Embedder → Qdrant without touching disk. |
| P7 | Mobile Access (Telegram adapter) | ✅ Done | |
| P8 | Enterprise Connect (Slack/Teams) | ✅ Done | |
| P9 | Multi-modal Support (OCR/Video) | 📋 Planned | `pkg/extractor/ocr.go` and `video.go` are explicit **stubs** — both return `not yet implemented` errors. OCR routes through Extractous sidecar for supported formats (PDF, DOCX). Direct image OCR and video transcription require Tesseract and Whisper integration (post-P14). |
| P10 | Infrastructure-as-Code (Helm charts) | ✅ Done | |
| P11 | Native Protocol Support — SMB | ✅ Done | |
| P12 | Native Protocol Support — NFS | ✅ Done | |
| P13 | Native Protocol Support — SFTP | ✅ Done | |
| P14 | Observability Suite (Prometheus, Grafana) | ✅ Done | |

---

## Phase 15: Enterprise Scale & High Availability
The goal is to move from a "trusted tool" to "critical infrastructure."

| Sub-phase | Description | Status |
|-----------|-------------|--------|
| 15.1 | Distributed Qdrant Clustering | ✅ Done | 3-node Qdrant cluster with Raft consensus; bootstrap via qdrant-1; isolated named volumes; healthchecks. Design doc at `docs/design/ha-infrastructure.md`. |
| 15.2 | Gateway High Availability (multi-replica + shared registry) | ✅ Done | 2 gateway replicas behind Nginx round-robin LB; `NodeRegistry` interface with `FileNodeRegistry` (default) and `DBNodeRegistry` (PostgreSQL, HA mode); `newRegistry()` factory toggles on `POSTGRES_URL`. |
| 15.3 | Global Namespace Aggregation | 📋 Planned |
| 15.4 | OIDC/Active Directory Integration (per-file ACL) | 📋 Planned |
| 15.5 | Air-Gapped Optimization — Ollama/vLLM local embeddings | ✅ Done | `EmbedProvider` interface implemented; `OllamaProvider` fully implemented. Refactored into `src/pkg/embed` (DRY). |
| 15.6 | Delta-Only Re-indexing (checksum-based) | ✅ Done | 3-stage pipeline (stat → partial XXH3 → full XXH3); `EMDEX_DELTA_ENABLED` / `EMDEX_FULL_HASH` env vars; 7 tests; design doc at `docs/design/delta-indexing.md`. |
| 15.7 | S3 node full pipeline (P6 completion) | ✅ Done | `vfs/s3.go` S3FileSystem + `NODE_TYPE=s3` in the standard `emdex-node` binary. Uses `watcher.Poller` for delta polling, `indexer.IndexDataToPoints` for pipeline. No separate binary — S3 is a VFS backend like SMB/SFTP/NFS. |

## Phase 18: Parameterization
| Sub-phase | Description | Status |
|-----------|-------------|--------|
| 18.1 | Environment Variable Cleanup | ✅ Done |
| 18.2 | Configurable Search/Chat Limits | ✅ Done |
| 18.3 | Documentation Synchronization | ✅ Done |

## Phase 19: Advanced CI/CD & Automation ✅ Done
| Feature | Description | Status |
|---------|-------------|--------|
| Monorepo Fan-Out CI | Path-filtered fan-out builds per component (gateway/node/mcp) via GitHub Actions reusable workflow | ✅ Done |
| Branch-based Tagging Suffixes | Automatic image tag suffixes: `-beta`, `-rc`, `-hotfix`, `-alpha`, `-PR` driven by branch name | ✅ Done |
| Expert Copilot Instructions | `.github/copilot-instructions.md` with architecture, conventions, and security policies for AI-assisted development | ✅ Done |
| Final Hardening Sprint | SSRF protection, archive size limits, HTTP timeouts, and Docker resource limits | ✅ Done |

---

## Integrity Notes (v1.0.1 Hard-Fix Sprint)
The following structural issues were identified and fixed in the v1.0.1 sprint:

1. **Namespace bypass** — `handleChatCompletions` was passing `""` to Qdrant on both RAG hops, allowing cross-tenant data access. Fixed: endpoint now requires `X-Emdex-Namespace` header; missing namespace returns 400.
2. **Registry race condition** — `NodeRegistry` was storing `*NodeInfo` (shared pointers); callers could mutate live registry state. Fixed: registry now stores value types with deep copies on read/write.
3. **Registry persistence** — Registry was in-memory only; a gateway restart lost all registered nodes. Fixed: registry now persists to `nodes.json` with atomic temp-file swap.
4. **Gemini hard-lock** — Embedding was directly calling the Gemini API everywhere. Fixed: `EmbedProvider` interface introduced in both gateway and node; `GeminiProvider` is default, `OllamaProvider` stub ready for Phase 15.5.
5. **P4 honesty** — Real-time watcher was marked Done but was one-shot only. Fixed: `pkg/watcher/` implements fsnotify with debounce + recursive directory watching.
6. **P6 / P9 honesty** — Both were marked `[x] Done` despite being stubs. Fixed: marked accurately above.

---

## Integrity Notes (Go 1.26.1 Hardening Sprint — 2026-03-15)

1. **Full Go 1.26.1 migration** — All 6 modules (`gateway`, `node`, `node-s3`, `node-smb`, `node-nfs`, `mcp`) migrated to Go 1.26.1. `go.mod` and CI matrix updated across the board.
2. **Native `gosec` in CI** — `gosec` static analysis integrated as a mandatory CI step; blocks merge on high-severity findings. Replaces ad-hoc manual audits.
3. **`EmbedProvider` refactored to `src/pkg/embed`** — Shared provider logic extracted from per-module copies into a single `src/pkg/embed` package. All modules import from there (DRY).
4. **Cache directory permissions hardened (0700)** — `os.MkdirAll` calls for cache dirs now use `0700` instead of `0755`, preventing other local users from reading embedding caches.
5. **Hardcoded secrets removed from E2E tests** — All API keys, tokens, and passwords inlined in test fixtures replaced with environment variable lookups (`os.Getenv`). No secrets in source.
6. **HTTP server timeouts** — Strict `ReadTimeout` and `WriteTimeout` enforced in `gateway` and `node` HTTP servers to prevent Slowloris attacks and connection leakage.

---
*Time is a flat circle, but your data doesn't have to be lost in it.*
