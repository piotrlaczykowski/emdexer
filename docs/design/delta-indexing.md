# Delta-Only Re-indexing — Design Document (Phase 15.6)

## Overview

Phase 15.6 introduces **checksum-based delta detection** to the Emdexer poller. Instead of re-indexing every file on every poll cycle, the poller now runs a three-stage pipeline to determine whether file content has actually changed before sending vectors to Qdrant.

## Problem Statement

The legacy poller compared `size` and `mtime` only. This approach has two failure modes:

1. **False negatives (silent overwrites):** A file can be overwritten with new content while retaining the same `mtime` (e.g. `touch -t` or NFS timestamp granularity issues). The poller would skip a genuinely changed file.
2. **False positives (redundant re-indexing):** Any operation that updates `mtime` without changing content (e.g. `touch`, backup software metadata updates) triggers a full re-index, wasting embedding API calls and Qdrant upserts.

## Three-Stage Detection Pipeline

```
Stage 1: Stat check
├── size == cached AND mtime == cached?
│   └── YES → proceed to Stage 2 (verify with hash — catches silent overwrites)
│   └── NO  → proceed to Stage 2 (confirm content actually changed)
│
Stage 2: Sparse/partial hash (XXH3, first+last 1 MB)
├── partial_hash == cached?
│   └── YES, stats matched   → Unchanged — touch last_seen only
│   └── YES, stats changed   → StatChanged — update cache metadata, skip re-index
│   └── NO                   → proceed to Stage 3 (or re-index directly)
│   └── unavailable (no ReaderAt) → fall back to stat-only
│
Stage 3: Full hash (XXH3, entire file) — opt-in via EMDEX_FULL_HASH=1
├── full_hash == cached?
│   └── YES → Unchanged — touch last_seen only
│   └── NO  → Changed — re-index
```

**Default behaviour** (EMDEX_FULL_HASH=0): the pipeline stops at Stage 2. For most workloads the sparse hash (first+last 1 MB) provides sufficient confidence at minimal I/O cost.

## Cache Warm-Up

On first poll after upgrade, the `partial_hash` column will be NULL for existing rows. When a stat-match is detected but no cached hash exists, the poller **warms the cache** (computes and stores the partial hash) without triggering re-indexing. This prevents a re-indexing storm on first deployment.

## Hashing Algorithm: XXH3

[`github.com/zeebo/xxh3`](https://github.com/zeebo/xxh3) — a pure-Go implementation of the XXH3 algorithm.

- **Non-cryptographic**: suitable for change detection, not security.
- **Extremely fast**: ~10× faster than SHA-256 for large files; SIMD-accelerated on amd64.
- **Deterministic**: same input always produces the same 64-bit digest (formatted as `%016x`).
- **Pure Go**: `CGO_ENABLED=0` builds work without a C toolchain.

## I/O Profile

| Operation | File opens | Bytes read |
|-----------|-----------|------------|
| Unchanged file (stat match + partial hash match) | 1 | ≤ 2 MB |
| Changed file (stat changed, partial hash computed in poll) | 1 (for indexing) | file size |
| New file | 1 | file size |

> **Optimisation:** `indexFile()` reads the file once into `content []byte` and derives the partial hash from the in-memory buffer via `bytes.NewReader`. No second VFS open is performed.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `EMDEX_DELTA_ENABLED` | `1` | Set to `0` to disable delta detection and revert to legacy stat-only mode. |
| `EMDEX_FULL_HASH` | `0` | Set to `1` to add Stage 3 (full-file XXH3 hash). Provides maximum accuracy at the cost of reading the entire file on every changed-stat event. |

## Schema

Delta detection adds three columns to the `file_cache` SQLite table:

```sql
ALTER TABLE file_cache ADD COLUMN partial_hash TEXT;  -- sparse XXH3 hex digest
ALTER TABLE file_cache ADD COLUMN full_hash    TEXT;  -- full XXH3 hex digest (opt-in)
ALTER TABLE file_cache ADD COLUMN algorithm    TEXT;  -- always 'xxh3'
```

Migrations are idempotent: "duplicate column name" errors are silently ignored, so existing databases upgrade safely.

## Security Notes

- All file access is routed through the scoped VFS (`p.fs.Open`). Direct `os.Open` calls are prohibited in hashing paths.
- `OpenReaderAt` asserts the VFS file implements `io.ReaderAt` and returns an error if not — it never falls back to `os.Open`.
- The partial hash cap (`length > 32 MB → error`) prevents oversized buffer allocations.
- XXH3 is non-cryptographic and **must not** be used for integrity guarantees against adversarial tampering. It is suitable only for benign change detection.

## Test Coverage

| Test | File | Description |
|------|------|-------------|
| `TestCalculatePartialHash_SmallFile` | `indexer/delta_test.go` | Determinism for small files |
| `TestCalculatePartialHash_DifferentContent` | `indexer/delta_test.go` | Collision resistance for distinct inputs |
| `TestCalculateFullHash_Deterministic` | `indexer/delta_test.go` | Full hash determinism |
| `TestDeltaResultString` | `indexer/delta_test.go` | DeltaResult stringer |
| `TestDelta_UnchangedFile_IsSkipped` | `watcher/delta_test.go` | Unchanged file → no re-index |
| `TestDelta_SameMtimeDifferentContent_IsDetected` | `watcher/delta_test.go` | Mtime-spoofing / silent overwrite detection |
| `TestDelta_NewFile_IsIndexed` | `watcher/delta_test.go` | New file is indexed on first poll |
