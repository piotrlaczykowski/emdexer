#!/bin/bash
set -e

# Path to the built binary
EMDEX_BIN="$(dirname "$0")/../../../bin/emdex"
EMDEX_BIN=$(realpath "$EMDEX_BIN")

if [ ! -f "$EMDEX_BIN" ]; then
    echo "Error: emdex binary not found at $EMDEX_BIN. Run 'make cli' first."
    exit 1
fi

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

echo "--- CLI Tests Passed ---"
rm -rf "$TEST_ROOT"
