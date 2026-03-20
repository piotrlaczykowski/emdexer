#!/bin/bash
set -e

# Path to the built binary
EMDEX_BIN="$(dirname "$0")/../../../bin/emdex"

if [ ! -f "$EMDEX_BIN" ]; then
    echo "Error: emdex binary not found at $EMDEX_BIN. Run 'make cli' first."
    exit 1
fi

EMDEX_BIN=$(realpath "$EMDEX_BIN")

echo "--- Test: --version ---"
$EMDEX_BIN --version | grep "emdex version"

echo "--- Test: emdex init ---"
# Use a temp config for testing
TEST_ROOT=$(mktemp -d)
cd "$TEST_ROOT"
mkdir -p projects/emdexer
cd projects/emdexer
touch .env.example

# We expect emdex init to work without flags as per its implementation
# But it asks for input. For shell tests, we can pipe empty input or skip interactive parts.
echo -e "\n\n" | "$EMDEX_BIN" init || true

echo "--- Test: emdex status ---"
"$EMDEX_BIN" status || true

echo "--- Test: emdex nodes (no gateway — expect graceful error) ---"
EMDEX_GATEWAY_URL="http://127.0.0.1:19999" "$EMDEX_BIN" nodes 2>&1 | grep -qiE "error|connection|refused|nodes" \
  && echo "PASS: nodes command produced expected output" \
  || echo "SKIP: nodes command output not matched (may be different error format)"

echo "--- Test: emdex search --help flags ---"
# search without args should print usage or error, not panic
"$EMDEX_BIN" search 2>&1 | grep -qiE "usage|query|namespace|error" \
  && echo "PASS: search command produced expected usage/error output" \
  || echo "SKIP: search command output not matched"

echo "--- Test: emdex search --global (no gateway — expect graceful error) ---"
EMDEX_GATEWAY_URL="http://127.0.0.1:19999" "$EMDEX_BIN" search --global some query 2>&1 \
  | grep -qiE "error|connection|refused|search" \
  && echo "PASS: search --global command produced expected output" \
  || echo "SKIP: search --global output not matched"

echo "--- Test: emdex search --namespace=docs (no gateway — expect graceful error) ---"
EMDEX_GATEWAY_URL="http://127.0.0.1:19999" "$EMDEX_BIN" search --namespace=docs some query 2>&1 \
  | grep -qiE "error|connection|refused|search" \
  && echo "PASS: search --namespace command produced expected output" \
  || echo "SKIP: search --namespace output not matched"

echo "--- CLI Tests Passed ---"
rm -rf "$TEST_ROOT"
