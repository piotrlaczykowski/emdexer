# Design: Binary File Organization (Option A+B)

**Date:** 2026-03-28
**Status:** Approved
**Scope:** `src/gateway/`, `src/node/`, `src/cmd/emdex/`

---

## Problem

Three `main.go` files have grown large and unfocused, making them hard to navigate, review, and work with in AI-assisted development sessions:

| File | Lines | Issues |
|------|-------|--------|
| `src/gateway/main.go` | 1073 | Server struct + 17 handlers + topology + metrics + wiring all in one file |
| `src/cmd/emdex/main.go` | 705 | All CLI commands inline with mixed request and display logic |
| `src/node/main.go` | 577 | Config struct + VFS wiring + indexer setup mixed with entrypoint |

The pattern of extracting into focused files was started (`handlers_eval.go`, `eventbus.go`, `events.go`) but not completed.

---

## Approach: Option A+B — Granular file-split + supporting types extraction

All files remain in `package main` within their respective directories. No interface changes. No import path changes. Pure reorganization — the compiler sees the same package, just across more files.

Supporting structs that are not the primary `Server`/`App` struct move to a `types.go` per binary.

---

## Gateway (`src/gateway/`)

| File | Responsibility | Est. lines |
|------|---------------|------------|
| `main.go` | Parse flags, call `newServer()`, call `srv.Run()` | ~30 |
| `types.go` | `GraphConfig`, `AgenticConfig`, other supporting types | ~30 |
| `server.go` | `Server` struct, `newServer()` constructor, `Run()` (route registration + HTTP listen + graceful shutdown), `writeJSON` | ~150 |
| `metrics.go` | All `promauto` var declarations | ~20 |
| `topology.go` | `refreshTopology()`, `knownNamespaces()`, background refresh loop | ~50 |
| `handler_registry.go` | `handleRegisterNode`, `handleListNodes`, `handleDeregisterNode` | ~60 |
| `handler_search.go` | `handleSearch`, `graphExpandResults`, `uniquePaths` | ~200 |
| `handler_chat.go` | `handleChatCompletions` | ~270 |
| `handler_health.go` | `handleHealth`, `handleLiveness`, `handleReadiness`, `handleStartup`, `handleQdrantHealth`, `handleWhoami` | ~100 |
| `handlers_eval.go` | Already exists — no change | ~89 |
| `eventbus.go` | Already exists — no change | — |
| `events.go` | Already exists — no change | — |

**Note:** `handleWhoami` groups with health because it serves the same diagnostic purpose.

---

## Node (`src/node/`)

| File | Responsibility | Est. lines |
|------|---------------|------------|
| `main.go` | Parse flags, call `newApp()`, call `app.Run()` | ~20 |
| `types.go` | `Config` struct (all 25+ env-var fields, VFS/audio/vision/indexing config) | ~60 |
| `app.go` | `App` struct, `newApp()` constructor, `Run()`, graceful shutdown | ~120 |
| `vfs.go` | `initVFS()`, all VFS-backend wiring (SMB, S3, SFTP, NFS, OS) | ~80 |
| `indexing.go` | Indexer setup, queue wiring, watcher setup, plugin loader wiring | ~100 |
| `gemini.go` | Already exists — no change | — |
| `events.go` | Already exists — no change | — |
| `graph_migration.go` | Already exists — no change | — |

---

## CLI (`src/cmd/emdex/`)

| File | Responsibility | Est. lines |
|------|---------------|------------|
| `main.go` | Arg dispatch (`os.Args[1]` switch), delegate to command functions | ~30 |
| `types.go` | Shared CLI types (e.g. `workerResult`) | ~20 |
| `usage.go` | `printUsage` | ~25 |
| `cmd_init.go` | `cmdInit` | ~65 |
| `cmd_start.go` | `cmdStart` | ~20 |
| `cmd_status.go` | `cmdStatus`, `printStatusLine`, `checkHealth`, `checkWorker`, `checkRegistry` | ~150 |
| `cmd_nodes.go` | `cmdNodes` | ~75 |
| `cmd_search.go` | `cmdSearch` — HTTP request logic only | ~60 |
| `cmd_search_display.go` | `cmdSearch` — result formatting/output | ~70 |
| `cmd_chat.go` | `cmdChat` — HTTP + SSE stream reading logic | ~80 |
| `cmd_chat_display.go` | `cmdChat` — token/stream output formatting | ~50 |
| `cmd_whoami.go` | `cmdWhoami` | ~30 |

---

## Constraints

- **No behavior changes.** This is a pure structural refactor. Function signatures, types, and logic are untouched.
- **No new dependencies.** All files remain in their existing `package main`.
- **Existing files are not renamed or deleted** unless they are being superseded by a split (e.g., `main.go` is rewritten, not deleted).
- **Build must pass** after each binary is reorganized before moving to the next.
- **`test_nfs.go` in `src/node/`** — file stays as-is; it is a manual test helper, not part of the reorganization.

---

## Success Criteria

- All three `main.go` files are ≤ 30 lines.
- No single file in any binary exceeds ~300 lines.
- `go build ./...` passes for all three binaries after the refactor.
- Existing tests continue to pass.
- Each file has a single, nameable responsibility that can be stated in one sentence.
