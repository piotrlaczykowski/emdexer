#!/bin/bash
set -e

# Configuration
PROJECT_ROOT="/home/piotr.laczykowski/.openclaw/workspace/projects/emdexer"
NODE_BIN="$PROJECT_ROOT/bin/emdex-node"
QDRANT_HOST="localhost:6334"
GOOGLE_API_KEY="${GOOGLE_API_KEY:-}"

if [ -z "$GOOGLE_API_KEY" ]; then
  echo "GOOGLE_API_KEY is required for this test"
  exit 1
fi

# Cleanup
pkill emdex-node || true
rm -rf "$PROJECT_ROOT/test_cache_fallback"
mkdir -p "$PROJECT_ROOT/test_cache_fallback"
rm -rf "$PROJECT_ROOT/test_dir_fallback"
mkdir -p "$PROJECT_ROOT/test_dir_fallback"

# Prepare test files
echo "This is a plain text file. It should be indexed even without sidecar." > "$PROJECT_ROOT/test_dir_fallback/plain.txt"
# Create a dummy binary file that would normally need Extractous (e.g., a fake PDF)
echo "%PDF-1.4 binary content that needs sidecar" > "$PROJECT_ROOT/test_dir_fallback/fake.pdf"

# Start Node with unreachable Sidecar
export GOOGLE_API_KEY=$GOOGLE_API_KEY
export QDRANT_HOST=$QDRANT_HOST
export EXTRACTOUS_HOST="http://localhost:9999" # Non-existent port
export EMDEX_NAMESPACE=fallback
export NODE_ROOT="$PROJECT_ROOT/test_dir_fallback"
export EMDEX_CACHE_DIR="$PROJECT_ROOT/test_cache_fallback"
export NODE_HEALTH_PORT=8084

echo "Starting Node with unreachable sidecar..."
nohup $NODE_BIN > "$PROJECT_ROOT/node_fallback.log" 2>&1 &
NODE_PID=$!

echo "Waiting for indexing..."
sleep 10

# Verify logs for extraction error but continued execution
echo "Checking logs for extraction errors..."
grep -q "Error extracting" "$PROJECT_ROOT/node_fallback.log" && echo "✅ Found expected extraction error in logs" || (echo "❌ Extraction error NOT found in logs"; kill $NODE_PID; exit 1)

# Verify that plain text file WAS indexed (check Qdrant via gateway if possible, or just check node logs)
grep -q "Processing: .*/plain.txt" "$PROJECT_ROOT/node_fallback.log" && echo "✅ Plain text file was processed" || (echo "❌ Plain text file was NOT processed"; kill $NODE_PID; exit 1)

# Ensure it didn't crash
ps -p $NODE_PID > /dev/null && echo "✅ Node is still running" || (echo "❌ Node crashed!"; exit 1)

echo "Sidecar Fallback Test PASSED"

# Cleanup
kill $NODE_PID
