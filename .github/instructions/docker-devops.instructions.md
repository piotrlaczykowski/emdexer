---
name: docker-devops
description: Expert multi-stage Docker build standards and CI/CD alignment for Emdexer.
applyTo: "src/*/Dockerfile"
---

# Docker & DevOps Standards for Emdexer

## 🐳 Dockerfile Best Practices
- **Multi-Stage Builds**: Always use a multi-stage approach (`builder` -> `runtime`).
- **Base Images**: Use pinned versions (e.g., `golang:1.26.1-alpine`, `alpine:3.21`).
- **Security**: 
  - ALWAYS run as a non-root user (`USER emdexer`).
  - Set appropriate file ownership before switching user.
- **Dependencies**: Run `go mod download` early to leverage layer caching.
- **Build Flags**: Use `CGO_ENABLED=0` and `ldflags="-s -w"` for small, static binaries.

## 🚀 CI/CD Integration
- **Tagging**: Follow the branch-suffix convention:
  - develop -> `-beta`
  - release/* -> `-rc`
  - hotfix/* -> `-hotfix`
  - feature/* / bugfix/* -> `-alpha`
  - PR -> `-PR`
