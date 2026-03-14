# Contributing to Emdexer

Thank you for your interest in contributing to Emdexer.

## Development Workflow

Emdexer follows **Semantic Versioning 2.0.0** and uses **Conventional Commits** to automate the release process.

### Branching Strategy

- `master` / `main`: Production-ready code. Only accepts merges from `release/*` and `hotfix/*`.
- `develop`: Integration branch for features and pre-releases.
- `release/*`: Release preparation branches.
- `hotfix/*`: Urgent production fixes.
- `feature/*`: Active development of new features.

### Automated Releases

Releases are managed by [Release Please](https://github.com/google-github-actions/release-please-action).

1.  **Develop**: Merge your feature branch into `develop` using a Conventional Commit (e.g., `feat: add new extractor`).
2.  **Release PR**: Release Please will automatically create or update a Release PR for the `develop` (pre-release) or `master` (stable) branch.
3.  **Tag & Release**: When the Release PR is merged, the action will automatically tag the commit, create a GitHub Release, and generate the changelog.
