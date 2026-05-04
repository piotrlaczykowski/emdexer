#!/usr/bin/env bash
# Verifies that every uncommented EMDEX_* env var declared in the canonical
# compose's `node:` service block is also present in docker-compose.multi-node.yml
# and docker-compose.ha.yml.
#
# Why: P47 shipped EMDEX_EXTRACT_CACHE_{ENABLED,REDIS_URL,TTL} to the canonical
# node service but the vars were missing from multi-node node services (node-docs,
# node-code) and the HA smb/sftp node services.  This script catches that class
# of drift in CI.
#
# Scoping: Only the canonical `node:` service block is inspected (from the line
# matching `^  node:` up to the next two-space-indented service header).  This
# prevents gateway-only vars (EMDEX_CACHE_BACKEND, EMDEX_RERANK_*, etc.) from
# producing false MISSING reports in files that have no gateway service.
#
# Exclusions: EMDEX_NODE_URL is a single-node self-registration URL intentionally
# omitted from multi-node/HA topologies; it is excluded from the check.
#
# Limitations: Checks for the variable *name* anywhere in the target file — does
# not scope to a specific service block in multi/HA.  A var present in any service
# (e.g. a comment) will pass.  Comment lines (optional whitespace + `#`) in the
# canonical block are skipped.

set -euo pipefail

CANONICAL="deploy/docker/docker-compose.yml"
MULTI="deploy/docker/docker-compose.multi-node.yml"
HA="deploy/docker/docker-compose.ha.yml"

for f in "$CANONICAL" "$MULTI" "$HA"; do
    if [ ! -f "$f" ]; then
        echo "ERROR: required file $f not found"
        exit 2
    fi
done

# Extract only the `node:` service block from the canonical compose.
# The block starts at the line matching `^  node:` (two-space indent under
# `services:`) and ends at the next line with the same two-space-indented
# service name pattern.
node_block=$(
    awk '
        /^  node:[[:space:]]*$/ { in_block=1; next }
        in_block && /^  [a-zA-Z][a-zA-Z0-9_-]*:/ { in_block=0 }
        in_block { print }
    ' "$CANONICAL"
)

if [ -z "$node_block" ]; then
    echo "ERROR: could not extract node: service block from $CANONICAL — awk pattern broken?"
    exit 2
fi

# Collect uncommented EMDEX_* var names from the node block only.
# Strip the leading whitespace+`- ` prefix, take the token before `=`, then
# extract the EMDEX_* name.  This avoids capturing right-hand-side references
# like `${EMDEX_NODE_URL_NODE:-...}` as false positives.
node_vars=$(
    echo "$node_block" \
        | grep -E '^[[:space:]]*-[[:space:]]*EMDEX_' \
        | grep -vE '^[[:space:]]*#' \
        | sed 's/^[[:space:]]*-[[:space:]]*//' \
        | cut -d'=' -f1 \
        | grep -oE 'EMDEX_[A-Z0-9_]+' \
        | sort -u
)

if [ -z "$node_vars" ]; then
    echo "ERROR: no active EMDEX_* vars found in node: block of $CANONICAL — regex broken?"
    exit 2
fi

# Vars intentionally omitted from multi-node/HA topologies.
# EMDEX_NODE_URL: single-node self-registration URL; each multi-node service
# uses its own host binding and does not set this centrally.
EXCLUDE_VARS="EMDEX_NODE_URL"  # space-separated; add vars that are intentionally absent from multi-node/HA

fail=0
for var in $node_vars; do
    # Skip intentionally excluded single-node vars.
    skip=0
    for excl in $EXCLUDE_VARS; do
        if [ "$var" = "$excl" ]; then
            skip=1
            break
        fi
    done
    [ "$skip" -eq 1 ] && continue

    for file in "$MULTI" "$HA"; do
        if ! grep -q "$var" "$file"; then
            echo "MISSING: $var not found in $file"
            echo "  -> Add it to all node services in $file (see canonical node service in deploy/docker/docker-compose.yml)"
            fail=1
        fi
    done
done

if [ "$fail" -eq 0 ]; then
    checked=$(
        for var in $node_vars; do
            skip=0
            for excl in $EXCLUDE_VARS; do
                [ "$var" = "$excl" ] && skip=1 && break
            done
            [ "$skip" -eq 0 ] && echo "$var"
        done | wc -l | tr -d ' '
    )
    echo "OK: all $checked EMDEX_* node-service env vars from canonical compose are present in multi-node and HA composes."
fi
exit $fail
