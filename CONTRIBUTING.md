# Contributing to Emdexer

## Branching Strategy

We follow a modified GitFlow branching model:

- **`master` / `main`**: Production-ready code. Stable releases.
- **`develop`**: Integration branch for features. Pre-releases and beta versions.
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
