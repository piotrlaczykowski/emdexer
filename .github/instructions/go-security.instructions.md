---
name: go-security
description: >
  Go secure coding rules for Emdexer derived from the Anthropic-Cybersecurity-Skills
  collection (DevSecOps + Container Security categories). Enforces gosec-aligned
  security policies, safe cryptography, input validation, and zero-secret-leakage patterns.
applyTo: "src/**/*.go"
---

# Go Security Standards for Emdexer

> **Source:** Derived from [Anthropic-Cybersecurity-Skills](https://github.com/mukul975/Anthropic-Cybersecurity-Skills) — DevSecOps, Container Security, and Incident Response skill categories.  
> **Enforcement:** `gosec` + `govulncheck` must pass with **zero HIGH/CRITICAL findings** on every PR.

---

## 🔐 Cryptography

- **NEVER** use `crypto/md5` or `crypto/sha1` for any security-sensitive purpose (passwords, MACs, integrity checks).  
  Use `crypto/sha256`, `crypto/sha512`, or `golang.org/x/crypto/bcrypt` / `argon2` for passwords.
- **TLS** configuration must always set:
  ```go
  &tls.Config{
      MinVersion: tls.VersionTLS12,
      // TLS 1.3 preferred; never set MaxVersion to anything < 1.2
  }
  ```
- **NEVER** set `InsecureSkipVerify: true` in any TLS config — not in tests, not in dev mode.  
  Use `knownhosts` or a proper CA bundle.
- All random tokens and nonces must use `crypto/rand`, not `math/rand`.

---

## 🗝️ Secrets & Environment Variables

- **NEVER** hardcode API keys, tokens, passwords, or private keys in source code.
- All secrets must be sourced via `os.Getenv("SECRET_NAME")` or a dedicated config loader.
- **NEVER** log secrets, tokens, or sensitive fields — not even partially.  
  Sanitize error messages: if an error includes a token, redact it before logging.
- Secrets must **never** be embedded in Dockerfile `ENV` or `ARG` layers — they persist in image history.

---

## 📁 File Permissions

- Database files, key material, and credentials: **`0600`** (`os.WriteFile(path, data, 0600)`).
- Private directories: **`0700`** (`os.MkdirAll(path, 0700)`).
- Public config files: **`0644`** maximum.
- NEVER create world-writable files (`0666`, `0777`).

---

## 🛡️ Input Validation & Injection Prevention

- **SQL:** Always use parameterized queries — never construct queries with `fmt.Sprintf` or string concatenation.
  ```go
  // ✅ Correct
  db.QueryContext(ctx, "SELECT * FROM users WHERE id = $1", userID)
  // ❌ Never
  db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM users WHERE id = %s", userID))
  ```
- **Shell:** Never pass unsanitized user input to `exec.Command` or `exec.CommandContext`.  
  Validate and allowlist all arguments.
- **Path traversal:** Always resolve and validate file paths against an expected root before opening:
  ```go
  clean := filepath.Clean(filepath.Join(root, userPath))
  if !strings.HasPrefix(clean, root) {
      return fmt.Errorf("path traversal attempt: %s", userPath)
  }
  ```
- **HTTP input:** Validate and bound-check all JSON/form inputs. Use `http.MaxBytesReader` to limit request bodies.

---

## 🌐 HTTP Server Security

NEVER use `http.DefaultServeMux` in production. Always create an explicit `*http.ServeMux` or router.

All HTTP servers must set explicit timeouts:
```go
server := &http.Server{
    Addr:         addr,
    Handler:      mux,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 30 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

- Set `Strict-Transport-Security`, `X-Content-Type-Options`, and `X-Frame-Options` response headers on all endpoints.
- CORS must be explicitly allowlisted — never use wildcard `*` on authenticated endpoints.

---

## 🔒 SSH & Remote Connections

- Always use `knownhosts.New(knownHostsFile)` for SSH host key verification.
- **NEVER** use `ssh.InsecureIgnoreHostKey()` — this is a hard rule even in integration tests.
- SSH private keys must be loaded from files with `0600` permissions only; abort if permissions are broader.

---

## ⚠️ Error Handling

- Check errors immediately after every function call that returns one.
- Wrap errors with context: `fmt.Errorf("component.operation: %w", err)`.
- Choose **one** strategy per call site: log OR return — never both.
- **NEVER** log sensitive data (tokens, passwords, private keys, PII) in error messages.
- Use `errors.Is` / `errors.As` for structured error type checking — never string matching.

---

## 🔄 Goroutine & Concurrency Safety

- Always know how every goroutine will exit. Unbounded goroutines are security risks (DoS amplification).
- Use `context.Context` cancellation to bound goroutine lifetimes on external I/O.
- Mutexes protecting security-sensitive state (auth tokens, session maps) must use `sync.RWMutex` correctly.
- Never store sensitive data in goroutine-local state that may be dumped in a panic.

---

## 🧪 Security Testing Requirements

Every module under `src/` must pass the following before merge:

```bash
# Static security analysis
gosec ./...
# Must output: "Issues found: 0 [HIGH: 0, MEDIUM: 0]" (or only accepted LOW with inline suppression + comment)

# Vulnerability database check
govulncheck ./...
# Must output: "No vulnerabilities found."
```

Inline suppression of gosec findings requires a comment explaining the accepted risk:
```go
//#nosec G304 -- path is constructed from validated root + allowlisted filename only
f, err := os.Open(safePath)
```

---

## 🚫 Forbidden Patterns

| Pattern | Reason | Alternative |
|---------|--------|-------------|
| `md5.Sum(...)` for security | Collision-vulnerable | `sha256.Sum256(...)` |
| `InsecureSkipVerify: true` | MITM attack surface | Proper CA/knownhosts |
| `math/rand` for tokens | Predictable | `crypto/rand` |
| `fmt.Sprintf` in SQL | SQL injection | Parameterized queries |
| Hardcoded secrets | Secret leakage | `os.Getenv()` |
| `http.DefaultServeMux` | Shared global state | Explicit `http.NewServeMux()` |
| `0666` / `0777` file perms | Unauthorized access | `0600` / `0700` |
| `exec.Command(userInput)` | Command injection | Allowlist + validation |

---

*Derived from: Anthropic-Cybersecurity-Skills / DevSecOps + Container Security categories*  
*Source: https://github.com/mukul975/Anthropic-Cybersecurity-Skills*
