# Contributing to Emdexer

Thank you for your interest in contributing to Emdexer!

## Development Setup

### Prerequisites

- Go >= 1.21
- Docker & Docker Compose
- Make

### Building locally

```bash
git clone https://github.com/piotrlaczykowski/emdexer.git
cd emdexer

# Build all binaries
make all

# Run tests
make test

# Build Docker images locally
docker compose -f deploy/docker/docker-compose.yml build
```

### Running with Docker Compose (dev mode)

```bash
cd deploy/docker
cp ../../.env.example .env
# Edit .env with your API keys
docker compose up -d
```

---

## Development Workflow

Emdexer follows **Semantic Versioning 2.0.0** and uses **Conventional Commits** to automate the release process.

### Branching Strategy

This project uses **trunk-based development** — `main` is the only long-lived branch.

| Branch | Purpose |
|---|---|
| `main` | Always releasable. All PRs target this branch. |
| `feature/*` | New features |
| `fix/*` | Bug fixes |
| `chore/*` | Maintenance, deps, refactors |

Releases are created by pushing a version tag (e.g. `git tag v1.2.0 && git push origin v1.2.0`), which triggers the release workflow.

### Commit Convention

Use **Conventional Commits** for clear history:

| Prefix | Effect | Example |
|---|---|---|
| `feat:` | minor version bump | `feat: add SFTP VFS backend` |
| `fix:` | patch version bump | `fix: handle empty SMB share` |
| `feat!:` / `BREAKING CHANGE:` | major bump | `feat!: redesign namespace API` |
| `docs:`, `chore:`, `refactor:` | no release | `docs: update installation guide` |

### Good First Issues

Look for issues labeled [`good first issue`](https://github.com/piotrlaczykowski/emdexer/issues?q=label%3A%22good+first+issue%22) — these are well-scoped and have enough context to get started quickly.

### Areas welcoming contributions

- **New VFS backends** — currently: Local, SMB, NFS, SFTP, S3. Adding more is well-defined.
- **Extractors** — new file format support (epub, DOCX improvements, etc.)
- **Documentation** — guides, examples, architecture diagrams
- **Tests** — integration and e2e test coverage

---

## Pull Request Process

1. Fork the repo and create your branch from `main`
2. Write tests for your changes
3. Ensure `make test` and `make vet` pass
4. Open a PR against `main` with a clear description
5. Reference any related issues

---

## License

By contributing, you agree that your contributions will be licensed under the [BSL-1.1 License](LICENSE).
