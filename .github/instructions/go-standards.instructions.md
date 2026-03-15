---
name: go-standards
description: Strict idiomatic Go standards and project-specific architectural guardrails for Emdexer.
applyTo: "src/**/*.go"
---

# Go Development Standards for Emdexer

Follow idiomatic Go practices based on Effective Go and Google's Go Style Guide.

## 🏗️ Architecture & Layout
- **Shared Logic**: ALL core logic (embeddings, VFS, queue, common types) MUST live in `src/pkg/`.
- **Private Logic**: Use `internal/` for logic that should not be exposed to other modules.
- **Entrypoints**: Main applications live in `src/gateway/`, `src/node/`, or `src/cmd/`.
- **DRY**: Do not duplicate implementation between components. Plumb shared dependencies from `src/pkg/`.

## 💻 Coding Style
- **Happy Path**: Keep the happy path left-aligned. Return early to reduce nesting.
- **Errors**:
  - Check errors immediately.
  - Wrap errors with context: `fmt.Errorf("context: %w", err)`.
  - Use `errors.Is` and `errors.As` for checking.
  - Avoid \"log and return\" - choose one.
- **Concurrency**:
  - Prefer channels for communication, mutexes for state protection.
  - Always know how a goroutine will exit.
  - Use `sync.WaitGroup` for waiting; for Go 1.25+, prefer `wg.Go(func())` pattern.
- **Interfaces**: Accept interfaces, return concrete types. Keep interfaces small (1-3 methods).

## 🛡️ Security
- **Permissions**: Database files must be `0600`. Directories must be `0700`.
- **SSH**: Always use `knownhosts` verification. NEVER use `ssh.InsecureIgnoreHostKey()`.
- **Secrets**: Never hardcode keys or tokens. Use environment variables.
