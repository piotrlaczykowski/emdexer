---
name: ci-security
description: >
  CI/CD security policies for Emdexer derived from the Anthropic-Cybersecurity-Skills
  collection. Covers GitHub Actions hardening, Docker image security gates, secrets
  management in pipelines, container forensics traceability, and Kubernetes deployment
  security controls.
applyTo: ".github/workflows/**, deploy/**"
---

# CI/CD & Deployment Security Standards for Emdexer

> **Source:** Derived from [Anthropic-Cybersecurity-Skills](https://github.com/mukul975/Anthropic-Cybersecurity-Skills) — DevSecOps, Container Security, Cloud Security, and Incident Response categories.  
> **Context:** GitHub Actions CI/CD → Docker multi-stage builds → Helm deploy to K8s/OpenShift clusters.

---

## 🔒 GitHub Actions Workflow Hardening

### Action Version Pinning
- **ALWAYS** pin third-party actions to a full commit SHA, not just a semver tag:
  ```yaml
  # ✅ Correct — pinned to SHA
  - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683  # v4.2.2
  # ❌ Risky — mutable tag can be hijacked
  - uses: actions/checkout@v4
  ```
- First-party GitHub actions (`actions/*`, `github/*`) may use semver tags with a pinned SHA comment.

### GITHUB_TOKEN Permissions
- Declare `permissions` explicitly on **every job** — never rely on repository defaults:
  ```yaml
  jobs:
    build:
      permissions:
        contents: read
        packages: write
        security-events: write  # only if uploading SARIF
  ```
- Use the principle of least privilege: only grant the permissions a job actually needs.
- Never use `permissions: write-all`.

### Secrets Handling in Workflows
- **NEVER** echo secrets in `run` steps. Avoid `set -x` in any step that has secrets in scope.
- **NEVER** print environment variables containing secrets (`env | grep -i token`).
- Secrets must only be referenced via `${{ secrets.SECRET_NAME }}` — never via env var interpolation in shell strings.
- Do not pass secrets as command-line arguments (they appear in `ps aux`). Use stdin or temp files with `0600` permissions.
- After use, unset secret variables: `unset SECRET_NAME`.

### Workflow Isolation
- Use separate jobs for security scanning and deployment — never combine in one job.
- All deploy jobs must reference a named GitHub `environment` with required reviewers for production.
- Never use `continue-on-error: true` on security scan steps — a failed scan must block the workflow.

---

## 🐳 Docker Image Security Gates

### Mandatory Trivy Scan Before Push
Every image build pipeline **must** include a Trivy vulnerability scan that gates the push:

```yaml
- name: Trivy vulnerability scan
  uses: aquasecurity/trivy-action@915b19bbe73b92a6cf82a1bc12b087c9a19a5fe2  # v0.28.0
  with:
    image-ref: ${{ env.IMAGE }}:${{ github.sha }}
    format: sarif
    output: trivy-results.sarif
    exit-code: '1'           # Fail pipeline on findings
    severity: 'CRITICAL'     # Block on CRITICAL CVEs
    ignore-unfixed: true

- name: Upload Trivy SARIF
  uses: github/codeql-action/upload-sarif@v3
  if: always()
  with:
    sarif_file: trivy-results.sarif
```

**Rule:** Images with CRITICAL CVEs must **never** be pushed to any registry.

### Secret Scanning with Gitleaks
Run on every push and PR — not just scheduled:

```yaml
- name: Gitleaks secret scan
  uses: gitleaks/gitleaks-action@cb7149a9b57195b609c63e8518d2c6056677d2d0  # v2
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

**Rule:** Any commit containing a detected secret must be blocked immediately. Rotate the exposed credential before merging.

### SARIF Upload to GitHub Security Tab
All scanner outputs (gosec, govulncheck, Trivy, Gitleaks) must be uploaded as SARIF artifacts to the GitHub Security tab for centralized tracking.

---

## 🔐 Secrets Management in Deployments

### GitHub Actions Secrets
- Use **GitHub Actions Secrets** (not repository variables) for all credentials.
- Scope secrets to the minimum required environment (environment-level secrets > repository-level).
- Rotate secrets on any suspected exposure event.

### Kubernetes / Helm Deployments
- **NEVER** embed secrets as base64 values in Helm `values.yaml` committed to the repo.
- Use **External Secrets Operator** or **Sealed Secrets** for all K8s secret injection.
- Service accounts used by CI/CD pipelines must have:
  ```yaml
  automountServiceAccountToken: false
  ```
  Token must be explicitly mounted only in pods that need it.
- TLS certificates must be managed via **cert-manager** — never manually embedded in charts.

### Helm Chart Security
```yaml
# Required in all pod specs:
securityContext:
  runAsNonRoot: true
  runAsUser: 65534       # nobody
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL

# Required for containers:
securityContext:
  privileged: false      # NEVER true
  allowPrivilegeEscalation: false
```

---

## 🛡️ Container Runtime Security (Kubernetes/OpenShift)

### Pod Security
- All pods must run as **non-root** (`runAsNonRoot: true`).
- `readOnlyRootFilesystem: true` must be set unless a writable FS is explicitly documented.
- `allowPrivilegeEscalation: false` on all containers — no exceptions without security review.
- Drop `ALL` capabilities; add back only the specific capabilities needed (e.g., `NET_BIND_SERVICE` for port 80).
- **NEVER** mount `/var/run/docker.sock` into a pod.

### RBAC Least Privilege
- Each deployed component must have its own dedicated ServiceAccount.
- RBAC roles must follow least privilege: specify exact `verbs` and `resources` — no wildcards (`*`).
- ClusterRoles should only be used when namespace-scoped Roles are insufficient.
- Audit RBAC on every cluster with `kubectl rbac-tool` or KubiScan before production changes.

### Network Policies
- All namespaces must have a default-deny NetworkPolicy with explicit ingress/egress allowlists.
- Pod-to-pod communication must be explicitly permitted — no implicit cluster-wide trust.

---

## 📦 Container Forensics & Traceability

All images built by CI must include OCI standard labels for forensic traceability:

```dockerfile
LABEL org.opencontainers.image.source="https://github.com/piotrlaczykowski/emdexer"
LABEL org.opencontainers.image.revision="${GIT_SHA}"
LABEL org.opencontainers.image.created="${BUILD_DATE}"
LABEL org.opencontainers.image.version="${VERSION}"
```

In GitHub Actions workflows:
```yaml
- name: Set image metadata
  id: meta
  uses: docker/metadata-action@v5
  with:
    images: ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}
    tags: |
      type=sha,prefix=,suffix=,format=short
      type=ref,event=branch
```

**Logging:** All containers must emit structured JSON logs to stdout/stderr. Never write application logs only to local container filesystem paths — they are lost on container restart and unavailable for forensic analysis.

---

## ✅ Required Security Checks Per Pipeline Stage

| Stage | Required Checks | Failure Action |
|-------|----------------|----------------|
| PR / Push | Gitleaks secret scan | Block merge |
| PR / Push | gosec (Go security analysis) | Block merge if HIGH/CRITICAL |
| PR / Push | govulncheck (CVE check) | Block merge if vulnerabilities found |
| Image Build | Trivy CRITICAL scan | Block image push |
| Image Build | Dockerfile lint (hadolint) | Warn on DL3008+, block on HIGH |
| Deploy Staging | Trivy against deployed image | Alert if drift detected |
| Deploy Production | Manual approval via GitHub environment | Required reviewers must approve |
| Weekly Scheduled | Full Trivy + gosec + govulncheck sweep | Create GitHub Security Advisory if new findings |

---

## 🚫 Forbidden CI/CD Patterns

| Pattern | Risk | Correct Alternative |
|---------|------|---------------------|
| `continue-on-error: true` on security steps | Silent security failures | Remove; let it fail |
| `permissions: write-all` | Over-privileged token | Explicit minimal permissions |
| Unpinned third-party actions (`@v1`) | Supply chain attack | Pin to SHA digest |
| Secrets in `env:` at workflow level | Broad secret exposure | Job-level or step-level scope |
| `docker run --privileged` in CI | Container escape risk | Remove; use rootless builds |
| Base64 secrets in `values.yaml` | Secret in version control | External Secrets / Sealed Secrets |
| Wildcard RBAC (`verbs: ["*"]`) | Privilege escalation | Explicit verb + resource lists |
| Mounting `/var/run/docker.sock` | Docker daemon takeover | Rootless builds (Kaniko/Buildah) |
| `--skip-tls-verify` in kubectl/helm | MITM on cluster API | Proper kubeconfig with CA bundle |

---

*Derived from: Anthropic-Cybersecurity-Skills / DevSecOps + Container Security + Cloud Security categories*  
*Source: https://github.com/mukul975/Anthropic-Cybersecurity-Skills*
