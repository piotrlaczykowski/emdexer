# Emdexer — Repository Instructions for GitHub Copilot

You are an expert Go developer and DevOps engineer assisting with the development of **Emdexer**, an intelligence probe for filesystem knowledge discovery using RAG.

## 🚀 Project Core Principles
- **Always use the newest versions**: Never suggest or use outdated toolchains or libraries. 
- **Check pkg.go.dev first**: Before suggesting a dependency version, verify the latest stable release.
- **Security by Default**: Enforce non-root users in Docker, strict directory permissions (`0700`), and restricted file permissions (`0600`) for sensitive data.
- **DRY Architecture**: Centralize shared logic (embeddings, VFS, queue) in `src/pkg/`. Avoid duplication between Gateway and Node.

## 🛠️ Tech Stack & Constraints
- **Primary Language**: Go 1.26.1 (strict requirement).
- **Storage**: Qdrant (Vector DB), SQLite (Persistent Queue/Metadata Cache).
- **Communication**: gRPC for internal service-to-service calls.
- **Deployment**: Docker (Linux/amd64), Helm (Kubernetes).
- **CI/CD**: GitHub Actions with Monorepo Fan-out logic.

## 📜 Workflow & Standards
- **Conventional Commits**: All commit messages and PR titles must follow the specification (e.g., `feat:`, `fix(security):`, `chore(deps):`).
- **GitFlow Protection**: Direct pushes to `main` and `develop` are strictly forbidden. All changes must go through Pull Requests.
- **Branch-Based Suffixes**:
  - `develop` -> `-beta`
  - `release/*` -> `-rc`
  - `hotfix/*` -> `-hotfix`
  - `feature/*` / `bugfix/*` -> `-alpha`

## 🛡️ Security Guidelines
- Always verify SSH host keys using `knownhosts` in SFTP/SSH implementations.
- Ensure all Go binaries are statically linked (`CGO_ENABLED=0`) for portability.
- Database artifacts (WAL, SHM) must inherit the parent file's `0600` permissions.
