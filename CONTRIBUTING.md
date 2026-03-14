# Contributing to Emdexer

## Branching Strategy

We follow a modified GitFlow branching model:

- **`master` / `main`**: Production-ready code. Stable releases. **Accepts merges ONLY from `release/*` and `hotfix/*` branches.**
- **`develop`**: Integration branch for features. Pre-releases and beta versions. **Accepts merges from `feature/*` and `bugfix/*` branches.**
- **`release/*`**: Preparation for a new production release. Branches off from `develop`, merges into `master` and back into `develop`.
- **`hotfix/*`**: Urgent fixes for production. Branches off from `master`, merges into `master` and `develop`.
- **`feature/*`**: Development of new features. Branch off from `develop`.

## Conventional Commits

All commit messages and Pull Request titles must follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. This is used to automatically generate changelogs and determine version bumps.

Format: `<type>(<scope>): <description>`

Types:
- `feat`: A new feature (corresponds to `MINOR` in SemVer)
- `fix`: A bug fix (corresponds to `PATCH` in SemVer)
- `docs`: Documentation only changes
- `style`: Changes that do not affect the meaning of the code
- `refactor`: A code change that neither fixes a bug nor adds a feature
- `perf`: A code change that improves performance
- `test`: Adding missing tests or correcting existing tests
- `chore`: Changes to the build process or auxiliary tools and libraries

Breaking changes must be indicated by a `!` after the type/scope or by `BREAKING CHANGE:` in the footer (corresponds to `MAJOR` in SemVer).
