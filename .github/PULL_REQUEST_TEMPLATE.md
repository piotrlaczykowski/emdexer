## Summary

<!-- What does this PR do? Link the phase/ticket. -->

## Changes

<!-- Brief bullet list of what changed and why. -->

## Checklist

### Code
- [ ] `go test ./...` passes locally
- [ ] `go vet ./...` — zero warnings
- [ ] `govulncheck ./...` — no vulnerabilities

### Environment variables
- [ ] New `EMDEX_*` vars added to **all three** compose files (`docker-compose.yml`, `docker-compose.multi-node.yml`, `docker-compose.ha.yml`)
- [ ] New vars added to `.env.example` with a comment explaining purpose and default
- [ ] New vars added to relevant Helm `values.yaml` and `deployment.yaml`

### Observability
- [ ] New Prometheus metrics live in a `*_metrics.go` file and have a panel in a Grafana dashboard (`deploy/monitoring/grafana/dashboards/`)
- [ ] New duration-parsing helpers guard against `n=0` and `n<0`
- [ ] New code paths produce useful log output and OTel span attributes

### Backwards compatibility
- [ ] No breaking changes to existing env vars or API surface, OR breaking changes are documented above
- [ ] Schema migrations are idempotent and tested

### Docs
- [ ] `CHANGELOG.md` updated (or N/A for small fixes)

---

## PR Completeness Checklist

Before opening or merging any feature PR, use the `emdexer-finalizer` skill or ask: *"Are all areas covered for this PR?"*

| Area | What to check |
|------|---------------|
| **docs** | `.env.example` updated with new env vars? |
| **envs** | All new env vars have defaults in docker-compose + .env.example? |
| **docker** | `deploy/docker/docker-compose.yml` updated? |
| **helm** | `deploy/helm/emdexer-gateway/values.yaml` + `deployment.yaml` updated? |
| **backend** | `src/gateway/` + `src/pkg/` changes complete? |
| **tests** | Unit tests added for new code? Existing tests still pass? |
| **mcp** | `src/mcp/main.py` — does this feature affect the MCP surface? |
| **logging** | Are new code paths producing useful log output? |
| **metrics** | New Prometheus counters/gauges added where appropriate? |
| **observability** | OTel span attributes set for new code paths? |
| **pipelines** | CI passing? (lint, govulncheck, tests, build) |
| **plugin** | `src/plugin-sidecar/` — does this feature affect plugin behaviour? |
| **cli/cmd** | `src/cmd/` — does this feature need a new flag or command? |
