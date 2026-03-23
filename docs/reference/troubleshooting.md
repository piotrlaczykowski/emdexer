# Troubleshooting Guide

This guide covers common issues encountered when deploying, configuring, or using Emdexer.

## 1. Qdrant Connection Issues

### gRPC Connection Refused
**Symptoms:**
- Gateway or Node logs show `rpc error: code = Unavailable desc = connection error`.
- `emdex status` shows Qdrant as `OFFLINE`.

**Fixes:**
1. **Port Mismatch:** Ensure `QDRANT_HOST` is set to the gRPC port (default `6334`), not the HTTP port (`6333`).
2. **Network Policy:** If running in Kubernetes, ensure the `emdex-gateway` and `emdex-node` pods have network access to the `qdrant` service on port 6334.
3. **Container Status:** Run `docker ps` to ensure the Qdrant container is running. Check logs with `docker logs qdrant`.

### Collection Initialization Failure
**Symptoms:**
- Logs show `collection emdexer_v1 not found`.
- Search requests return 500 errors.

**Fixes:**
1. The gateway and nodes attempt to create the collection on startup. Ensure the `QDRANT_HOST` is reachable during the initial boot.
2. If using a custom collection name, ensure `EMDEX_QDRANT_COLLECTION` matches across all components.

---

## 2. Whisper & Multi-modal Failures

### Transcription Timeouts
**Symptoms:**
- Node logs show `context deadline exceeded` or `timeout` during transcription of large files.
- `[Media: Audio/Transcript]` tags are missing from search results.

**Fixes:**
1. **Hardware Acceleration:** Ensure the Whisper sidecar has access to a GPU (e.g., via `--gpus all` in Docker). CPU-only transcription of 1GB+ video files will likely timeout.
2. **Sidecar URL:** Verify `EMDEX_WHISPER_URL` is correct (e.g., `http://whisper:8080`).
3. **Model Size:** If using a `large` model on low-resource hardware, switch to `base` or `small` via `EMDEX_WHISPER_MODEL`.

### Extractous Sidecar Not Reachable

**Symptoms:**
- Node logs: `cb open` — the circuit breaker has opened after 5 consecutive extraction failures.
- Node logs: `extraction failed for <file>: ...` — individual file extraction errors.
- PDFs, DOCX, XLSX, and images are indexed with empty text.

**Fixes:**
1. **Check sidecar health:**
   ```bash
   docker compose exec node wget -qO- http://extractous:8000/health
   # Expected: {"status":"ok"}
   ```
2. **Check sidecar logs:**
   ```bash
   docker compose logs extractous --tail=50
   ```
3. **Verify env var** — `EMDEX_EXTRACTOUS_URL` must point to the sidecar (default: `http://localhost:8000/extract`). Check it is NOT set to the old variable name `EXTRACTOUS_HOST`.
4. **Circuit breaker recovery** — If the sidecar was temporarily down, the circuit resets automatically after 5 minutes. Restart the node to force immediate retry.
5. **Rebuild the sidecar** if Python dependencies changed:
   ```bash
   docker compose build extractous --no-cache && docker compose up -d extractous
   ```

See [INFRASTRUCTURE.md](../INFRASTRUCTURE.md#extractous-sidecar) for the full sidecar reference.

---

### OCR Not Working
**Symptoms:**
- Images are indexed but their content is missing or shows only metadata.
- PDF fallback to OCR is not triggering.

**Fixes:**
1. **Enable Flag:** Ensure `EMDEX_ENABLE_OCR=true` is set on the node.
2. **Extractous Health:** See "Extractous Sidecar Not Reachable" above.
3. **Tesseract Data:** Ensure the Extractous container has the necessary Tesseract language data installed for the documents you are indexing.

---

## 3. VFS & Permission Issues

### SMB/CIFS Connection Denied
**Symptoms:**
- Node logs show `NT_STATUS_ACCESS_DENIED` or `NT_STATUS_LOGON_FAILURE`.

**Fixes:**
1. **Credentials:** Check `SMB_USER`, `SMB_PASS`, and `SMB_DOMAIN` environment variables.
2. **Pathing:** Ensure the `SMB_SHARE` does not include the root slash if the client doesn't expect it (e.g., `Documents` instead of `/Documents`).
3. **NTLM Version:** Emdexer uses a modern SMB client; ensure the server supports SMB2 or SMB3.

### S3 Access Issues
**Symptoms:**
- Node logs show `Access Denied` or `SignatureDoesNotMatch`.

**Fixes:**
1. **Keys:** Verify `S3_ACCESS_KEY` and `S3_SECRET_KEY`.
2. **Endpoint:** If using MinIO or Wasabi, ensure `S3_ENDPOINT` includes the protocol (e.g., `http://minio:9000`) and `S3_USE_SSL` is set correctly.
3. **Region:** Some S3 providers require a specific `S3_REGION` (default is `us-east-1`).

---

## 4. Auth & OIDC Failures

### JWT Validation Error
**Symptoms:**
- Gateway logs show `failed to verify ID Token: oidc: expected audience...`.
- Clients receive `401 Unauthorized`.

**Fixes:**
1. **Client ID:** Ensure `OIDC_CLIENT_ID` exactly matches the `aud` claim in your JWT.
2. **Issuer URL:** Ensure `OIDC_ISSUER` matches the `iss` claim exactly (including trailing slashes).
3. **Clock Skew:** Ensure the gateway server's time is synchronized (NTP).

### Missing Namespaces
**Symptoms:**
- User is logged in but `GET /v1/whoami` returns an empty `namespaces` list.
- User gets `403 Forbidden` on search requests.

**Fixes:**
1. **Group Mapping:** Check `EMDEX_GROUP_ACL` JSON structure. Ensure group names match the values in the JWT `groups` claim.
2. **Claim Name:** If your provider uses a different claim for groups (e.g., `roles`), set `OIDC_GROUPS_CLAIM=roles`.

---

## 5. Performance & Resource Issues

### High Memory Usage on Nodes
**Symptoms:**
- Node containers are OOMKilled.

**Fixes:**
1. **Streaming Check:** Emdexer uses a "Zero-RAM" streaming architecture. If you see high RAM, check if `EMDEX_MAX_FILE_SIZE` is set too high or if a specific sidecar is buffering data.
2. **Concurrency:** Reduce `EMDEX_BATCH_SIZE` (default 100) to lower the number of simultaneous embedding requests.

### Slow Search Results
**Symptoms:**
- `/v1/search` takes > 2 seconds.

**Fixes:**
1. **Global Search Timeout:** If using `namespace=*`, increase `EMDEX_GLOBAL_SEARCH_TIMEOUT` (default 500ms).
2. **Qdrant Indexing:** Ensure Qdrant has finished building HNSW indexes for the collection. Check Qdrant metrics.
3. **Embedding Latency:** If using Gemini, check your internet latency. If using Ollama, ensure it has enough CPU/GPU resources.
