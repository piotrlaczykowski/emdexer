#!/bin/bash
set -e

# Configuration
PROJECT_ROOT="/home/piotr.laczykowski/.openclaw/workspace/projects/emdexer"
GATEWAY_BIN="$PROJECT_ROOT/bin/emdex-gateway"
NODE_BIN="$PROJECT_ROOT/bin/emdex-node"
QDRANT_HOST="localhost:6334"
AUTH_KEY="test-auth-key"
GOOGLE_API_KEY="${GOOGLE_API_KEY:-}"

if [ -z "$GOOGLE_API_KEY" ]; then
  echo "GOOGLE_API_KEY is required for this test"
  exit 1
fi

# Cleanup
pkill emdex-gateway || true
pkill emdex-node || true
rm -rf "$PROJECT_ROOT/test_cache_alpha" "$PROJECT_ROOT/test_cache_beta"
mkdir -p "$PROJECT_ROOT/test_cache_alpha" "$PROJECT_ROOT/test_cache_beta"

# Start Gateway
export GOOGLE_API_KEY=$GOOGLE_API_KEY
export QDRANT_HOST=$QDRANT_HOST
export EMDEX_AUTH_KEY=$AUTH_KEY
export GATEWAY_PORT=7701
export EMDEX_REGISTRY_FILE="$PROJECT_ROOT/test_nodes.json"

echo "Starting Gateway..."
nohup $GATEWAY_BIN > "$PROJECT_ROOT/gateway_test.log" 2>&1 &
GATEWAY_PID=$!
sleep 3

# Start Node Alpha
echo "Starting Node Alpha (Namespace: alpha)..."
export EMDEX_NAMESPACE=alpha
export NODE_ROOT="$PROJECT_ROOT/test_dir_alpha"
export EMDEX_CACHE_DIR="$PROJECT_ROOT/test_cache_alpha"
export NODE_HEALTH_PORT=8082
export EMDEX_GATEWAY_URL="http://localhost:7701"
export EMDEX_GATEWAY_AUTH_KEY=$AUTH_KEY
nohup $NODE_BIN > "$PROJECT_ROOT/node_alpha.log" 2>&1 &
ALPHA_PID=$!

# Start Node Beta
echo "Starting Node Beta (Namespace: beta)..."
export EMDEX_NAMESPACE=beta
export NODE_ROOT="$PROJECT_ROOT/test_dir_beta"
export EMDEX_CACHE_DIR="$PROJECT_ROOT/test_cache_beta"
export NODE_HEALTH_PORT=8083
nohup $NODE_BIN > "$PROJECT_ROOT/node_beta.log" 2>&1 &
BETA_PID=$!

echo "Waiting for indexing..."
sleep 10

# Test Search - Alpha
echo "Searching in ALPHA namespace..."
ALPHA_RESULT=$(curl -s -H "Authorization: Bearer $AUTH_KEY" "http://localhost:7701/v1/search?q=BLUE_OCTOBER&namespace=alpha")
echo "$ALPHA_RESULT" | grep -q "BLUE_OCTOBER" && echo "✅ Found ALPHA secret in ALPHA namespace" || (echo "❌ ALPHA secret NOT found in ALPHA namespace"; exit 1)

# Test Isolation - Alpha query for Beta secret
echo "Searching in ALPHA namespace for BETA secret..."
ALPHA_BETA_RESULT=$(curl -s -H "Authorization: Bearer $AUTH_KEY" "http://localhost:7701/v1/search?q=RED_NOVEMBER&namespace=alpha")
echo "$ALPHA_BETA_RESULT" | grep -q "RED_NOVEMBER" && (echo "❌ Leak! BETA secret found in ALPHA namespace"; exit 1) || echo "✅ BETA secret NOT found in ALPHA namespace"

# Test Search - Beta
echo "Searching in BETA namespace..."
BETA_RESULT=$(curl -s -H "Authorization: Bearer $AUTH_KEY" "http://localhost:7701/v1/search?q=RED_NOVEMBER&namespace=beta")
echo "$BETA_RESULT" | grep -q "RED_NOVEMBER" && echo "✅ Found BETA secret in BETA namespace" || (echo "❌ BETA secret NOT found in BETA namespace"; exit 1)

# Test Isolation - Beta query for Alpha secret
echo "Searching in BETA namespace for ALPHA secret..."
BETA_ALPHA_RESULT=$(curl -s -H "Authorization: Bearer $AUTH_KEY" "http://localhost:7701/v1/search?q=BLUE_OCTOBER&namespace=beta")
echo "$BETA_ALPHA_RESULT" | grep -q "BLUE_OCTOBER" && (echo "❌ Leak! ALPHA secret found in BETA namespace"; exit 1) || echo "✅ ALPHA secret NOT found in BETA namespace"

echo "Namespace Isolation Test PASSED"

# Cleanup
kill $GATEWAY_PID $ALPHA_PID $BETA_PID
