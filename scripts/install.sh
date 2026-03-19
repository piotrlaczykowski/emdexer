#!/bin/bash
# ============================================================
# Emdexer Modular Installer
# Usage:
#   ./install.sh --gateway    Install ONLY emdex-gateway
#   ./install.sh --node       Install ONLY emdex-node
#   ./install.sh --all        Install both binaries
#
# Binaries are statically linked (CGO_ENABLED=0).
# Requires: go >= 1.21, openssl, systemd (Linux only)
# ============================================================

set -euo pipefail

# ─── Config ─────────────────────────────────────────────────
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/emdexer"
DATA_DIR="/var/lib/emdexer"
LOG_DIR="/var/log/emdexer"
SERVICE_USER="emdexer"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GATEWAY_BIN_SRC="$PROJECT_ROOT/bin/emdex-gateway"
NODE_BIN_SRC="$PROJECT_ROOT/bin/emdex-node"
CLI_BIN_SRC="$PROJECT_ROOT/bin/emdex"

# ─── Helpers ─────────────────────────────────────────────────
log()   { echo -e "\033[1;32m[INSTALL]\033[0m $*"; }
warn()  { echo -e "\033[1;33m[WARN]\033[0m    $*" >&2; }
error() { echo -e "\033[1;31m[ERROR]\033[0m   $*" >&2; exit 1; }
prompt() {
    local _var="$1"
    local _msg="$2"
    local _default="${3:-}"
    local _input
    if [[ -n "$_default" ]]; then
        read -r -p "$_msg [$_default]: " _input
        _input="${_input:-$_default}"
    else
        read -r -p "$_msg: " _input
    fi
    printf -v "$_var" '%s' "$_input"
}
prompt_secret() {
    local _var="$1"
    local _msg="$2"
    local _input
    read -r -s -p "$_msg: " _input
    echo
    printf -v "$_var" '%s' "$_input"
}

# ─── Usage ───────────────────────────────────────────────────
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

  --gateway    Install emdex-gateway binary and systemd service
  --node       Install emdex-node binary and systemd service
  --all        Install both gateway and node
  --help       Show this help

Each mode prompts only for the environment variables it needs.
EOF
    exit 0
}

# ─── Parse flags ─────────────────────────────────────────────
INSTALL_GATEWAY=false
INSTALL_NODE=false

if [[ $# -eq 0 ]]; then
    usage
fi

for arg in "$@"; do
    case "$arg" in
        --gateway) INSTALL_GATEWAY=true ;;
        --node)    INSTALL_NODE=true ;;
        --all)     INSTALL_GATEWAY=true; INSTALL_NODE=true ;;
        --help|-h) usage ;;
        *) error "Unknown option: $arg. Run '$0 --help' for usage." ;;
    esac
done

# ─── OS Detection ────────────────────────────────────────────
OS_TYPE=""
PKG_MANAGER=""
case "$(uname -s)" in
    Linux*)
        OS_TYPE="linux"
        if [[ -f /etc/os-release ]]; then
            . /etc/os-release
            case "$ID" in
                centos|rhel|fedora|rocky|almalinux) PKG_MANAGER="yum" ;;
                debian|ubuntu|raspbian)             PKG_MANAGER="apt" ;;
                *) warn "Unrecognised distro '$ID'. Assuming apt." ; PKG_MANAGER="apt" ;;
            esac
        else
            error "Cannot detect Linux distro. Install Go and openssl manually."
        fi
        ;;
    Darwin*)
        OS_TYPE="macos"
        PKG_MANAGER="brew"
        ;;
    *)
        error "Unsupported OS: $(uname -s)"
        ;;
esac

# ─── Preflight ───────────────────────────────────────────────
check_go() {
    if ! command -v go &>/dev/null; then
        error "Go is not installed. Install Go >= 1.21 from https://go.dev/dl/ and retry."
    fi
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    log "Found Go $GO_VERSION"
}

check_openssl() {
    if ! command -v openssl &>/dev/null; then
        warn "openssl not found — cannot auto-generate auth keys."
    fi
}

generate_key() {
    if command -v openssl &>/dev/null; then
        openssl rand -hex 32
    else
        cat /dev/urandom | tr -dc 'a-f0-9' | fold -w 64 | head -n 1
    fi
}

# ─── Build ───────────────────────────────────────────────────
build_gateway() {
    log "Building emdex-gateway (CGO_ENABLED=0, static)..."
    (cd "$PROJECT_ROOT" && make gateway)
    [[ -f "$GATEWAY_BIN_SRC" ]] || error "Build failed: $GATEWAY_BIN_SRC not found."
    log "Gateway binary ready: $GATEWAY_BIN_SRC"
}

build_node() {
    log "Building emdex-node (CGO_ENABLED=0, static)..."
    (cd "$PROJECT_ROOT" && make node)
    [[ -f "$NODE_BIN_SRC" ]] || error "Build failed: $NODE_BIN_SRC not found."
    log "Node binary ready: $NODE_BIN_SRC"
}

build_cli() {
    log "Building emdex CLI (CGO_ENABLED=0, static)..."
    (cd "$PROJECT_ROOT" && make cli)
    [[ -f "$CLI_BIN_SRC" ]] || error "Build failed: $CLI_BIN_SRC not found."
    log "CLI binary ready: $CLI_BIN_SRC"
}

# ─── System Setup ────────────────────────────────────────────
create_service_user() {
    if [[ "$OS_TYPE" == "linux" ]]; then
        if ! id "$SERVICE_USER" &>/dev/null; then
            log "Creating system user '$SERVICE_USER'..."
            sudo useradd --system --no-create-home --shell /sbin/nologin "$SERVICE_USER"
        else
            log "System user '$SERVICE_USER' already exists."
        fi
    fi
}

install_cli_binary() {
    log "Installing emdex CLI to $INSTALL_DIR/emdex..."
    sudo install -m 755 "$CLI_BIN_SRC" "$INSTALL_DIR/emdex"
}

setup_dirs() {
    log "Creating directories: $CONFIG_DIR, $DATA_DIR, $LOG_DIR..."
    sudo mkdir -p "$CONFIG_DIR" "$DATA_DIR/cache" "$DATA_DIR/nodes" "$LOG_DIR"
    if [[ "$OS_TYPE" == "linux" ]]; then
        sudo chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR" "$LOG_DIR" 2>/dev/null || true
    fi
}

# ─── Interactive Config ───────────────────────────────────────

collect_gateway_config() {
    echo
    log "=== Gateway Configuration ==="
    echo "  The gateway requires: Gemini API key, Qdrant host, auth credentials."
    echo

    prompt_secret GOOGLE_API_KEY  "Gemini API Key (https://aistudio.google.com/app/apikey)"
    [[ -z "$GOOGLE_API_KEY" ]] && error "GOOGLE_API_KEY is required for the gateway."

    prompt QDRANT_HOST      "Qdrant gRPC host:port" "localhost:6334"
    prompt QDRANT_URL       "Qdrant HTTP URL"       "http://localhost:6333"
    prompt QDRANT_COLLECTION "Qdrant collection name" "emdexer_v1"
    prompt GATEWAY_PORT     "Gateway listen port"   "7700"

    echo
    echo "  Authentication mode:"
    echo "    1) Simple  — single shared bearer token (EMDEX_AUTH_KEY)"
    echo "    2) Advanced — per-key namespace ACL map (EMDEX_API_KEYS JSON)"
    prompt AUTH_MODE "Select mode [1/2]" "1"

    if [[ "$AUTH_MODE" == "2" ]]; then
        EMDEX_AUTH_KEY=""
        echo "  Enter the JSON map. Example:"
        echo '    {"sk-admin": ["*"], "sk-hr": ["hr", "legal"]}'
        prompt EMDEX_API_KEYS "EMDEX_API_KEYS JSON" ""
    else
        EMDEX_API_KEYS=""
        prompt EMDEX_AUTH_KEY "Auth key (leave blank to auto-generate)" ""
        if [[ -z "$EMDEX_AUTH_KEY" ]]; then
            EMDEX_AUTH_KEY="$(generate_key)"
            log "Auto-generated EMDEX_AUTH_KEY: $EMDEX_AUTH_KEY"
        fi
    fi

    prompt EMBED_PROVIDER   "Embed provider [gemini|ollama]" "gemini"
    if [[ "$EMBED_PROVIDER" == "ollama" ]]; then
        prompt OLLAMA_HOST        "Ollama host URL"   "http://localhost:11434"
        prompt OLLAMA_EMBED_MODEL "Ollama embed model" "nomic-embed-text"
    else
        OLLAMA_HOST=""
        OLLAMA_EMBED_MODEL=""
    fi

    echo
    echo "  OIDC/JWT Authentication (optional):"
    echo "    Leave empty to skip OIDC and use static API keys only."
    prompt OIDC_ISSUER "OIDC Issuer URL (leave blank to skip)" ""
    if [[ -n "$OIDC_ISSUER" ]]; then
        prompt OIDC_CLIENT_ID "OIDC Client ID" ""
        prompt OIDC_GROUPS_CLAIM "OIDC Groups Claim" "groups"
        prompt EMDEX_GROUP_ACL "Group ACL JSON (e.g. {\"admins\": [\"*\"]})" ""
    else
        OIDC_CLIENT_ID=""
        OIDC_GROUPS_CLAIM=""
        EMDEX_GROUP_ACL=""
    fi

    EMDEX_REGISTRY_FILE="$DATA_DIR/nodes/nodes.json"
}

collect_node_config() {
    echo
    log "=== Node Configuration ==="
    echo "  The node requires: Gateway URL + auth key, VFS type, embed provider."
    echo

    prompt EMDEX_GATEWAY_URL      "Gateway URL"                        "http://localhost:7700"
    prompt_secret EMDEX_GATEWAY_AUTH_KEY "Gateway auth key (from gateway install)" 
    [[ -z "$EMDEX_GATEWAY_AUTH_KEY" ]] && error "EMDEX_GATEWAY_AUTH_KEY is required for node registration."

    prompt EMDEX_NAMESPACE "Namespace for indexed vectors" "default"

    prompt QDRANT_HOST       "Qdrant gRPC host:port" "localhost:6334"
    prompt QDRANT_COLLECTION "Qdrant collection name" "emdexer_v1"

    echo
    echo "  VFS backend type:"
    echo "    local — index a local directory"
    echo "    smb   — Samba / Windows Share"
    echo "    sftp  — SSH File Transfer Protocol"
    echo "    nfs   — Network File System"
    prompt NODE_TYPE "VFS type [local|smb|sftp|nfs|s3]" "local"

    SMB_HOST="" SMB_USER="" SMB_PASS="" SMB_SHARE=""
    SFTP_HOST="" SFTP_PORT="" SFTP_USER="" SFTP_PASS=""
    NFS_HOST="" NFS_PATH=""

    case "$NODE_TYPE" in
        local)
            prompt NODE_ROOT "Local root path to index" "/opt/emdexer/data"
            ;;
        smb)
            prompt NODE_ROOT  "SMB root within share (. for share root)" "."
            prompt SMB_HOST   "SMB host"
            prompt SMB_USER   "SMB username"
            prompt_secret SMB_PASS "SMB password"
            prompt SMB_SHARE  "SMB share name"
            ;;
        sftp)
            prompt NODE_ROOT  "SFTP remote path"
            prompt SFTP_HOST  "SFTP host"
            prompt SFTP_PORT  "SFTP port" "22"
            prompt SFTP_USER  "SFTP username"
            prompt_secret SFTP_PASS "SFTP password"
            ;;
        nfs)
            prompt NODE_ROOT  "NFS root within export (. for export root)" "."
            prompt NFS_HOST   "NFS host"
            prompt NFS_PATH   "NFS export path"
            ;;
        s3)
            prompt S3_ENDPOINT "S3/MinIO endpoint" "play.min.io"
            prompt S3_ACCESS_KEY "S3 access key"
            prompt_secret S3_SECRET_KEY "S3 secret key"
            prompt S3_BUCKET "S3 bucket name"
            prompt S3_USE_SSL "Use SSL [true|false]" "true"
            prompt S3_PREFIX "S3 key prefix (empty for entire bucket)" ""
            NODE_ROOT="${S3_PREFIX:-.}"
            ;;
        *)
            error "Unknown VFS type: $NODE_TYPE. Valid: local, smb, sftp, nfs, s3"
            ;;
    esac

    prompt EMDEX_POLL_INTERVAL "Poll interval (remote VFS only)" "60s"
    EMDEX_CACHE_DIR="$DATA_DIR/cache"

    prompt EXTRACTOUS_HOST  "Extractous sidecar URL" "http://localhost:8000"
    prompt NODE_HEALTH_PORT "Node health port"       "8081"

    prompt EMBED_PROVIDER   "Embed provider [gemini|ollama]" "gemini"
    if [[ "$EMBED_PROVIDER" == "ollama" ]]; then
        prompt OLLAMA_HOST        "Ollama host URL"    "http://localhost:11434"
        prompt OLLAMA_EMBED_MODEL "Ollama embed model" "nomic-embed-text"
    else
        OLLAMA_HOST=""
        OLLAMA_EMBED_MODEL=""
        prompt_secret GOOGLE_API_KEY "Gemini API Key (for node embeddings)"
        [[ -z "$GOOGLE_API_KEY" ]] && error "GOOGLE_API_KEY required when EMBED_PROVIDER=gemini"
    fi
}

# ─── Write env files ─────────────────────────────────────────

write_gateway_env() {
    local ENV_FILE="$CONFIG_DIR/gateway.env"
    log "Writing gateway config to $ENV_FILE ..."
    sudo bash -c "cat > '$ENV_FILE'" <<EOF
# emdex-gateway environment — generated by install.sh $(date -u +"%Y-%m-%dT%H:%M:%SZ")
GOOGLE_API_KEY=${GOOGLE_API_KEY}
QDRANT_HOST=${QDRANT_HOST}
QDRANT_URL=${QDRANT_URL}
EMDEX_QDRANT_COLLECTION=${QDRANT_COLLECTION}
EMDEX_PORT=${GATEWAY_PORT}
EMDEX_AUTH_KEY=${EMDEX_AUTH_KEY}
EMDEX_API_KEYS=${EMDEX_API_KEYS}
EMBED_PROVIDER=${EMBED_PROVIDER}
OLLAMA_HOST=${OLLAMA_HOST:-}
OLLAMA_EMBED_MODEL=${OLLAMA_EMBED_MODEL:-}
EMDEX_REGISTRY_FILE=${EMDEX_REGISTRY_FILE}
OIDC_ISSUER=${OIDC_ISSUER:-}
OIDC_CLIENT_ID=${OIDC_CLIENT_ID:-}
OIDC_GROUPS_CLAIM=${OIDC_GROUPS_CLAIM:-}
EMDEX_GROUP_ACL=${EMDEX_GROUP_ACL:-}
EMDEX_GLOBAL_SEARCH_TIMEOUT=500
EMDEX_HA_MODE=${EMDEX_HA_MODE:-}
POSTGRES_URL=${POSTGRES_URL:-}
EOF
    sudo chmod 640 "$ENV_FILE"
    [[ "$OS_TYPE" == "linux" ]] && sudo chown "root:$SERVICE_USER" "$ENV_FILE" 2>/dev/null || true
    log "Gateway env written."
}

write_node_env() {
    local ENV_FILE="$CONFIG_DIR/node.env"
    log "Writing node config to $ENV_FILE ..."
    sudo bash -c "cat > '$ENV_FILE'" <<EOF
# emdex-node environment — generated by install.sh $(date -u +"%Y-%m-%dT%H:%M:%SZ")
EMDEX_GATEWAY_URL=${EMDEX_GATEWAY_URL}
EMDEX_GATEWAY_AUTH_KEY=${EMDEX_GATEWAY_AUTH_KEY}
EMDEX_NAMESPACE=${EMDEX_NAMESPACE}
QDRANT_HOST=${QDRANT_HOST}
EMDEX_QDRANT_COLLECTION=${QDRANT_COLLECTION}
NODE_TYPE=${NODE_TYPE}
NODE_ROOT=${NODE_ROOT:-}
EMDEX_POLL_INTERVAL=${EMDEX_POLL_INTERVAL}
EMDEX_CACHE_DIR=${EMDEX_CACHE_DIR}
EMDEX_EXTRACTOUS_URL=${EXTRACTOUS_HOST}/extract
NODE_HEALTH_PORT=${NODE_HEALTH_PORT}
EMBED_PROVIDER=${EMBED_PROVIDER}
GOOGLE_API_KEY=${GOOGLE_API_KEY:-}
OLLAMA_HOST=${OLLAMA_HOST:-}
OLLAMA_EMBED_MODEL=${OLLAMA_EMBED_MODEL:-}
SMB_HOST=${SMB_HOST:-}
SMB_USER=${SMB_USER:-}
SMB_PASS=${SMB_PASS:-}
SMB_SHARE=${SMB_SHARE:-}
SFTP_HOST=${SFTP_HOST:-}
SFTP_PORT=${SFTP_PORT:-}
SFTP_USER=${SFTP_USER:-}
SFTP_PASS=${SFTP_PASS:-}
NFS_HOST=${NFS_HOST:-}
NFS_PATH=${NFS_PATH:-}
S3_ENDPOINT=${S3_ENDPOINT:-}
S3_ACCESS_KEY=${S3_ACCESS_KEY:-}
S3_SECRET_KEY=${S3_SECRET_KEY:-}
S3_BUCKET=${S3_BUCKET:-}
S3_USE_SSL=${S3_USE_SSL:-}
S3_PREFIX=${S3_PREFIX:-}
EOF
    sudo chmod 640 "$ENV_FILE"
    [[ "$OS_TYPE" == "linux" ]] && sudo chown "root:$SERVICE_USER" "$ENV_FILE" 2>/dev/null || true
    log "Node env written."
}

# ─── Systemd Services ────────────────────────────────────────

install_gateway_service() {
    log "Installing emdex-gateway binary to $INSTALL_DIR/emdex-gateway..."
    sudo install -m 755 "$GATEWAY_BIN_SRC" "$INSTALL_DIR/emdex-gateway"

    log "Creating systemd unit: emdex-gateway.service"
    sudo bash -c "cat > /etc/systemd/system/emdex-gateway.service" <<EOF
[Unit]
Description=Emdexer Gateway — semantic search & RAG API
Documentation=https://github.com/piotr-laczykowski/emdexer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=${CONFIG_DIR}/gateway.env
WorkingDirectory=${DATA_DIR}
ExecStart=${INSTALL_DIR}/emdex-gateway
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal
SyslogIdentifier=emdex-gateway

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${DATA_DIR} ${LOG_DIR}
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable emdex-gateway.service
    log "emdex-gateway.service installed and enabled."
}

install_node_service() {
    log "Installing emdex-node binary to $INSTALL_DIR/emdex-node..."
    sudo install -m 755 "$NODE_BIN_SRC" "$INSTALL_DIR/emdex-node"

    log "Creating systemd unit: emdex-node.service"
    sudo bash -c "cat > /etc/systemd/system/emdex-node.service" <<EOF
[Unit]
Description=Emdexer Node — file indexing agent
Documentation=https://github.com/piotr-laczykowski/emdexer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=${CONFIG_DIR}/node.env
WorkingDirectory=${DATA_DIR}
ExecStart=${INSTALL_DIR}/emdex-node
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal
SyslogIdentifier=emdex-node

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${DATA_DIR} ${LOG_DIR}
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    sudo systemctl enable emdex-node.service
    log "emdex-node.service installed and enabled."
}

# ─── Main ────────────────────────────────────────────────────

main() {
    log "Emdexer Bare-Metal Installer"
    log "Mode: gateway=$INSTALL_GATEWAY  node=$INSTALL_NODE"
    echo

    check_go
    check_openssl

    if [[ "$OS_TYPE" == "linux" ]]; then
        create_service_user
    fi

    setup_dirs

    # ── Build selected components ──────────────────────────────
    if $INSTALL_GATEWAY; then
        build_gateway
    fi
    if $INSTALL_NODE; then
        build_node
    fi
    # Always build and install the CLI
    build_cli
    install_cli_binary

    # ── Collect config interactively ──────────────────────────
    if $INSTALL_GATEWAY; then
        collect_gateway_config
        write_gateway_env
    fi
    if $INSTALL_NODE; then
        # If we already collected gateway config in this run, reuse GOOGLE_API_KEY
        # for the node only if not set by user — node prompts will ask.
        collect_node_config
        write_node_env
    fi

    # ── Install systemd services (Linux only) ─────────────────
    if [[ "$OS_TYPE" == "linux" ]]; then
        if $INSTALL_GATEWAY; then
            install_gateway_service
        fi
        if $INSTALL_NODE; then
            install_node_service
        fi
    else
        warn "macOS detected. Systemd services not installed. Use 'emdex start' or launchd manually."
    fi

    # ── Summary ───────────────────────────────────────────────
    echo
    log "=========================================="
    log " Installation complete!"
    log "=========================================="
    if $INSTALL_GATEWAY; then
        echo "  Gateway binary : $INSTALL_DIR/emdex-gateway"
        echo "  Gateway config : $CONFIG_DIR/gateway.env"
        if [[ "$OS_TYPE" == "linux" ]]; then
            echo "  Start           : sudo systemctl start emdex-gateway"
            echo "  Status          : sudo systemctl status emdex-gateway"
            echo "  Logs            : journalctl -u emdex-gateway -f"
        fi
    fi
    if $INSTALL_NODE; then
        echo "  Node binary    : $INSTALL_DIR/emdex-node"
        echo "  Node config    : $CONFIG_DIR/node.env"
        if [[ "$OS_TYPE" == "linux" ]]; then
            echo "  Start          : sudo systemctl start emdex-node"
            echo "  Status         : sudo systemctl status emdex-node"
            echo "  Logs           : journalctl -u emdex-node -f"
        fi
    fi
    echo "  CLI            : $INSTALL_DIR/emdex"
    echo
    log "Review configs in $CONFIG_DIR before starting services."
}

main
