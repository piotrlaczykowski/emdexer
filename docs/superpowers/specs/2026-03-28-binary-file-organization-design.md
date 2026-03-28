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

**Key principle for `main.go`:** The goal is for `main.go` to contain only `func main()`, which calls a single constructor and then calls `Run()`. All wiring (env-var parsing, client construction, dependency assembly) moves into the constructor. This may result in `main.go` being 10–30 lines and the constructor being large — that is acceptable and expected.

---

## Gateway (`src/gateway/`)

### File inventory

| File | Responsibility | What moves there |
|------|---------------|-----------------|
| `main.go` | Entrypoint only — calls `newServer()` then `srv.Run()` | `func main()` stripped to ~15 lines |
| `types.go` | `GraphConfig` struct only — the sole `package main`-owned supporting type | `GraphConfig` (currently inline in main.go) |
| `server.go` | `Server` struct, `newServer()` constructor (absorbs all env-var parsing + OTel init + OIDC setup + BM25/agentic/graph/rerank config + registry + embedder + qdrant client construction), `Run()` (route registration + HTTP listen + graceful shutdown + metrics server start/stop), `writeJSON` | Everything currently in `main()` except handler registrations which move to `Run()` |
| `metrics.go` | All `promauto` var declarations (the three package-level `var` blocks) | The three `var` declarations at lines 43–56 |
| `topology.go` | `refreshTopology()`, `knownNamespaces()`, `startTopologyLoop()` (the background ticker goroutine currently in `main()`, extracted as a method on `Server`) | Lines 116–134, the ticker at lines 993–1004 |
| `handler_registry.go` | `handleRegisterNode`, `handleListNodes`, `handleDeregisterNode` | Lines 147–201 |
| `handler_search.go` | `handleSearch`, `graphExpandResults`, `uniquePaths` | Lines 203–432 |
| `handler_chat.go` | `handleChatCompletions` | Lines 433–693 |
| `handler_health.go` | `handleHealth`, `handleLiveness`, `handleReadiness`, `handleStartup`, `handleQdrantHealth`, `handleWhoami` | Lines 694–784 |
| `handlers_eval.go` | Already exists — no change | — |
| `eventbus.go` | Already exists — no change | — |
| `events.go` | Already exists — no change | — |

### Notes
- `AgenticConfig` is defined in the `rag` package, not `package main` — it does not move anywhere.
- `GraphConfig` is the only `package main`-owned type beyond `Server` itself.
- The Prometheus metrics HTTP server (port 9090) start and shutdown live in `Run()` inside `server.go`.
- `startTopologyLoop()` is called from `Run()` in `server.go`; the loop body lives in `topology.go`.
- `server.go` will be the largest file (~300 lines) because `newServer()` absorbs the full wiring from `main()`.
- **`otelShutdown` closure (post-refactor design):** `telemetry.InitTracer` currently returns a teardown closure called in `main()`. After the refactor, this closure will be stored as a field `otelShutdown func(context.Context) error` on `Server`, set inside `newServer()`, and called from `Run()` during graceful shutdown. This requires adding the field to the `Server` struct — it is not present in the current source and is part of the work.

---

## Node (`src/node/`)

### File inventory

| File | Responsibility | What moves there |
|------|---------------|-----------------|
| `main.go` | Entrypoint only — calls `newApp()` then `app.Run()` | `func main()` stripped to ~15 lines |
| `types.go` | `Config` struct (all 25+ env-var fields), `DefaultEmbedDims` and `CollectionName` constants | The `Config` struct at lines 43–101; constants at lines 37–39 |
| `helpers.go` | `parseIntEnv`, `parseFloatEnv`, `contextModel` — small pure utility functions | Lines 519–547 |
| `app.go` | `App` struct, `newApp()` constructor (reads env vars into `Config`, constructs Qdrant client, bootstraps collection + text index, checks graph migration, runs node self-registration), `Run()` (graceful shutdown loop), package-level globals (`EmbeddingDims`, `walkSeen`, `walkComplete`, `globalPointsClient`, and other package vars) | Lines 103–116 (globals), the qdrant/collection/migration/registration block in main(), graceful shutdown |
| `vfs.go` | `initVFS()` and all VFS-backend wiring (SMB, S3, SFTP, NFS, OS selection logic) | Lines 549–577 |
| `indexing.go` | Queue construction and worker launch, watcher poller setup, plugin loader setup, startup walk goroutine (the background scan at lines 444–497) | The queue/watcher/plugin block and startup walk goroutine |
| `gemini.go` | Already exists — no change | — |
| `events.go` | Already exists — no change | — |
| `graph_migration.go` | Already exists — no change | — |
| `test_nfs.go` | Already exists — manual test helper, no change | — |

### Boundary between `app.go` and `indexing.go`
- `app.go` owns: Qdrant connection, collection bootstrap, graph migration check, node self-registration call, health server. In `Run()`: `health.StartServer` is launched as a goroutine (non-blocking), followed by a `select` on an OS signal channel as the graceful shutdown trigger. This differs from the current source where `health.StartServer` is the blocking call — the refactor makes it a goroutine and adds an explicit signal-wait.
- `indexing.go` owns: queue construction, worker goroutine launch, watcher poller setup, plugin loader, startup walk goroutine.
- The split point is: "infrastructure that must exist before indexing can start" (`app.go`) vs "the indexing pipeline itself" (`indexing.go`).

---

## CLI (`src/cmd/emdex/`)

### File inventory

| File | Responsibility | What moves there |
|------|---------------|-----------------|
| `main.go` | Arg dispatch (`os.Args[1]` switch), call command functions | Stripped dispatch, ~20 lines |
| `types.go` | `workerResult` struct — the only `package main` type | `workerResult` (currently inline) |
| `usage.go` | `printUsage` | Lines 55–74 |
| `cmd_init.go` | `cmdInit` | Lines 76–138 |
| `cmd_start.go` | `cmdStart` | Lines 140–158 |
| `cmd_status.go` | `cmdStatus`, `printStatusLine`, `checkHealth`, `checkWorker`, `checkRegistry` | Lines 160–301 |
| `cmd_nodes.go` | `cmdNodes` | Lines 303–375 |
| `cmd_search.go` | `doSearch(args)` — HTTP request logic, returns result data | Extracted from `cmdSearch` |
| `cmd_search_display.go` | `printSearchResults(results)` — formatting/output; `cmdSearch` calls `doSearch` then `printSearchResults` | Extracted from `cmdSearch` |
| `cmd_chat.go` | `doChat(args)` — HTTP + SSE stream reading logic | Extracted from `cmdChat` |
| `cmd_chat_display.go` | `printChatToken(token)` / stream output formatting; `cmdChat` calls `doChat` then display helpers | Extracted from `cmdChat` |
| `cmd_whoami.go` | `cmdWhoami` | Lines 640+ |

### Notes on function splitting
- `cmdSearch` cannot be split across two files in Go (a function must be in one file). Instead, `cmdSearch` is **refactored** into:
  - `doSearch(args []string) ([]SearchResult, error)` in `cmd_search.go`
  - `printSearchResults(results []SearchResult)` in `cmd_search_display.go`
  - `cmdSearch(args []string)` (thin coordinator) stays in `cmd_search.go`
- Same pattern for `cmdChat`: refactored into `doChat` + display helpers, with `cmdChat` as thin coordinator in `cmd_chat.go`.

---

## Constraints

- **No behavior changes.** This is a pure structural refactor. All existing logic is preserved verbatim where possible; the only exceptions are the `cmdSearch`/`cmdChat` splits which require extracting sub-functions.
- **No new dependencies.** All files remain in their existing `package main`.
- **Build must pass** after each binary is reorganized before moving to the next binary.
- **Existing tests continue to pass** — run `go test ./...` in each module after reorganizing that binary.
- **No file is deleted** — `main.go` is rewritten (not deleted); all other existing files are untouched unless they are the source of extracted content.

---

## Success Criteria

- `gateway/main.go`, `node/main.go`, `cmd/emdex/main.go` each contain only `func main()` at ≤ 20 lines.
- No single new file exceeds ~300 lines.
- `go build` passes for all three binaries after the refactor.
- Existing tests pass.
- Each file has a single, nameable responsibility that can be stated in one sentence.
