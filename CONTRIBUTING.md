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
make build

# Run tests
make test

# Build Docker images locally
make docker-build
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

| Branch | Purpose |
|---|---|
| `main` | Production-ready. Only accepts merges from `release/*` and `hotfix/*` |
| `develop` | Integration branch for features |
| `feature/*` | Active development of new features |
| `bugfix/*` | Bug fixes targeting develop |
| `hotfix/*` | Urgent production fixes |
| `release/*` | Release preparation |

### Commit Convention

Releases are automated via [Release Please](https://github.com/google-github-actions/release-please-action) using Conventional Commits:

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

1. Fork the repo and create your branch from `develop`
2. Write tests for your changes
3. Ensure `make test` and `make lint` pass
4. Open a PR against `develop` with a clear description
5. Reference any related issues

---

## License

By contributing, you agree that your contributions will be licensed under the [BSL-1.1 License](LICENSE).
