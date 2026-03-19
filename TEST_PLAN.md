# Emdexer Integration Test Plan

> Single-node Docker Compose deployment validation.
> HA/Kubernetes testing excluded — should be done in a proper k8s environment.

---

## Phase 1: Full-Cycle Functional Test
**Goal**: Verify the "Happy Path" from file system to search result.
1. **Ingestion**: Drop a new markdown file `spectrum_test.md` into the node's watched directory.
2. **Indexing Audit**: Verify the **Node** detects the change, generates embeddings, and pushes to **Qdrant**.
3. **Discovery**: Query the **Gateway** (Port 7700) to ensure the new content is searchable within < 5 seconds.
4. **Metadata Integrity**: Verify that the returned search result includes correct source file paths and snippets.

## Phase 2: State Persistence & Data Integrity (Reboot Test)
**Goal**: Verify zero data loss after a full stack power-off.
1. **Mass Ingestion**: Index 50 markdown files at once.
2. **Hard Reset**: Run `docker compose down` and `docker compose up -d`.
3. **Cold Start Recovery**: Verify that Qdrant recovers and previously indexed data is immediately searchable.

## Phase 3: Network Partition & Consensus (Split-Brain)
> **SKIPPED** — Requires multi-node Qdrant cluster. Not applicable to single-node Docker Compose.
> Should be tested in Kubernetes with a 3-node Qdrant StatefulSet.

## Phase 4: Latency & Throughput (Stress Test)
**Goal**: Find the performance ceiling of the Gateway -> Qdrant path.
1. **Concurrent Flood**: Fire **100 concurrent search queries** per second for 1 minute.
2. **Metrics Audit**: Monitor `gateway` logs for "Context Deadline Exceeded" or "Pool Exhaustion" errors.
3. **Resource Contention**: Monitor the Node's memory usage during a heavy embedding burst (20MB+ text) to ensure no OOM.

## Phase 5: Security & API Boundary Audit
**Goal**: Verify "Hardened" production settings.
1. **SSRF Protection**: Attempt a search query with a payload hitting internal metadata IPs (e.g., `169.254.169.254`). Verify the Gateway blocks it.
2. **Unauthorized Access**: Attempt to hit internal Node API (8081) or Qdrant ports (6333) directly from the external network. Verify they are restricted to `emdexer-net`.

## Phase 6: File System Consistency (Watcher Stress)
**Goal**: Verify the Node's real-time watcher logic under heavy churn.
1. **Rapid Churn**: Run a script that creates, modifies, and deletes 20 files in 10 seconds.
2. **Sync Check**: Query the Gateway 30 seconds later to ensure the indexed state matches the final file system state (no "ghost" files).

## Phase 7: Compliance & Secret Hardening
**Goal**: Verify "Zero-Leak" and robust configuration policies.
1. **Secret Scrub Audit**: Verify that the repository history is clean of any leaked secrets (auth keys, API keys).
2. **Test Panic Enforcement**: Run E2E tests without setting environment variables. Verify the system panics/fails rather than using a hardcoded fallback.
3. **Model Stabilization**: Verify that all embedding calls in Node/Gateway use `gemini-embedding-2-preview` instead of experimental models.
