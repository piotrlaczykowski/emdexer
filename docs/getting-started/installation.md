# Emdexer Installation Guide

Emdexer ships as two independent binaries: **`emdex-gateway`** and **`emdex-node`**.
Deploy them together on one machine, or split them across hosts for distributed indexing.

---

## Prerequisites

### All modes
- **Go >= 1.21** — required to build the binaries.
- **openssl** — used by the installer to auto-generate auth keys.
- **Qdrant** running and reachable — the shared vector store.

### Gateway only
- **Google AI API Key** — for Gemini embeddings and chat completions.
  Get one at [Google AI Studio](https://aistudio.google.com/).

### Node only
- Gateway URL and auth key (from gateway installation).
- Access credentials for the data source (SMB/SFTP/NFS), if applicable.

---

## Quick Start — Automated Installer

The installer script handles building, binary placement, config file generation,
and systemd service setup in one shot.

```bash
# Clone the repo
git clone https://github.com/piotr-laczykowski/emdexer
cd emdexer

# Install ONLY the gateway
./scripts/install.sh --gateway

# Install ONLY a node
./scripts/install.sh --node

# Install both on the same machine
./scripts/install.sh --all
```

The script will **interactively prompt** for the configuration values relevant
to the selected mode — gateway install asks for the Gemini API key; node install
asks for the Gateway URL and auth key, plus VFS credentials.

### What the installer does

1. Builds selected binaries via `make` (`CGO_ENABLED=0`, statically linked).
2. Creates the `emdexer` system user (Linux only).
3. Creates `/etc/emdexer/`, `/var/lib/emdexer/`, `/var/log/emdexer/`.
4. Prompts for config values relevant to the chosen mode.
5. Writes `/etc/emdexer/gateway.env` and/or `/etc/emdexer/node.env`.
6. Installs systemd unit files and enables them.
7. Installs the `emdex` management CLI to `/usr/local/bin/emdex`.

---

## Manual Installation

### Step 1 — Build

```bash
# Build everything
make all

# Or individually
make gateway   # → bin/emdex-gateway
make node      # → bin/emdex-node
make cli       # → bin/emdex
```

Binaries are statically linked (`CGO_ENABLED=0`) — no libc dependency,
runs on any Linux distro.

### Step 2 — Create system user and directories

```bash
sudo useradd --system --no-create-home --shell /sbin/nologin emdexer
sudo mkdir -p /etc/emdexer /var/lib/emdexer/cache /var/lib/emdexer/nodes /var/log/emdexer
sudo chown -R emdexer:emdexer /var/lib/emdexer /var/log/emdexer
```

### Step 3 — Write configuration

**Gateway** → `/etc/emdexer/gateway.env`:

```ini
GOOGLE_API_KEY=your-gemini-api-key
EMDEX_GEMINI_MODEL=gemini-1.5-flash
QDRANT_HOST=localhost:6334
QDRANT_URL=http://localhost:6333
QDRANT_COLLECTION=emdexer_v1
EMDEX_AUTH_KEY=your-bearer-token-here
EMBED_PROVIDER=gemini
EMDEX_REGISTRY_FILE=/var/lib/emdexer/nodes/nodes.json
EMDEX_PORT=7700
EMDEX_SEARCH_LIMIT=10
EMDEX_CHAT_LIMIT=5
```

**Node** → `/etc/emdexer/node.env`:

```ini
EMDEX_GATEWAY_URL=http://gateway-host:7700
EMDEX_GATEWAY_AUTH_KEY=your-bearer-token-here
EMDEX_NAMESPACE=my-datasource
QDRANT_HOST=qdrant-host:6334
QDRANT_COLLECTION=emdexer_v1
NODE_TYPE=local
NODE_ROOT=/opt/emdexer/data
EMBED_PROVIDER=gemini
GOOGLE_API_KEY=your-gemini-api-key
EMDEX_GEMINI_MODEL=gemini-1.5-flash
EXTRACTOUS_HOST=http://localhost:8000
EMDEX_POLL_INTERVAL=60s
EMDEX_CACHE_DIR=/var/lib/emdexer/cache
EMDEX_QUEUE_DB=queue.db
NODE_HEALTH_PORT=8081
```

Secure the files:
```bash
sudo chmod 640 /etc/emdexer/*.env
sudo chown root:emdexer /etc/emdexer/*.env
```

### Step 4 — Install binaries

```bash
sudo install -m 755 bin/emdex-gateway /usr/local/bin/emdex-gateway
sudo install -m 755 bin/emdex-node    /usr/local/bin/emdex-node
sudo install -m 755 bin/emdex         /usr/local/bin/emdex
```

### Step 5 — Create systemd services

**`/etc/systemd/system/emdex-gateway.service`**:

```ini
[Unit]
Description=Emdexer Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=emdexer
Group=emdexer
EnvironmentFile=/etc/emdexer/gateway.env
WorkingDirectory=/var/lib/emdexer
ExecStart=/usr/local/bin/emdex-gateway
Restart=on-failure
RestartSec=5s
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/emdexer /var/log/emdexer

[Install]
WantedBy=multi-user.target
```

**`/etc/systemd/system/emdex-node.service`**:

```ini
[Unit]
Description=Emdexer Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=emdexer
Group=emdexer
EnvironmentFile=/etc/emdexer/node.env
WorkingDirectory=/var/lib/emdexer
ExecStart=/usr/local/bin/emdex-node
Restart=on-failure
RestartSec=5s
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/emdexer /var/log/emdexer

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now emdex-gateway
sudo systemctl enable --now emdex-node
```

---

## Docker Compose Deployment

### Standard (single node)

```bash
cp .env.example .env
# Edit .env with your values
docker compose up -d
```

### Multi-Node

```bash
docker compose -f docker-compose.multi-node.yml up -d
```

---

## Kubernetes (Helm)

### Gateway

```bash
helm install emdex-gateway deploy/helm/emdexer-gateway \
  --set secret.googleApiKey=YOUR_API_KEY \
  --set secret.emdexerAuthKey=YOUR_GATEWAY_AUTH_KEY
```

### Node

```bash
helm install emdex-node-nas deploy/helm/emdexer-node \
  --set config.gatewayUrl=https://emdex-gateway.internal \
  --set config.namespace=nas-docs \
  --set config.nodeType=smb \
  --set secret.smbHost=10.0.0.10 \
  --set secret.smbUser=nas_user \
  --set secret.smbPass=secure_pass \
  --set secret.smbShare=documents
```

---

## Advanced Node Configuration

### Remote VFS — SMB

```ini
NODE_TYPE=smb
NODE_ROOT=.
SMB_HOST=10.0.0.10
SMB_USER=nas_user
SMB_PASS=secure_password
SMB_SHARE=documents
EMDEX_POLL_INTERVAL=120s
```

### Remote VFS — NFS

```ini
NODE_TYPE=nfs
NODE_ROOT=.
NFS_HOST=10.0.0.11
NFS_PATH=/export/docs
EMDEX_POLL_INTERVAL=60s
```

### Remote VFS — SFTP

```ini
NODE_TYPE=sftp
NODE_ROOT=/home/user/docs
SFTP_HOST=10.0.0.12
SFTP_PORT=22
SFTP_USER=ssh_user
SFTP_PASS=secure_password
EMDEX_POLL_INTERVAL=60s
```

### Local Air-Gap Embedding (Ollama)

Set on the node (or gateway) to avoid all Gemini API calls:

```ini
EMBED_PROVIDER=ollama
OLLAMA_HOST=http://localhost:11434
OLLAMA_EMBED_MODEL=nomic-embed-text
```

> **Note**: Vector dimensions must match the Qdrant collection config.
> `nomic-embed-text` produces 768-dim vectors; the default collection expects 3072 (Gemini).
> Re-create the collection if switching providers.

---

## Verify the Services

```bash
# Gateway health
curl http://localhost:7700/healthz/readiness

# Node health
curl http://localhost:8081/healthz/readiness

# List registered nodes
curl -H "Authorization: Bearer YOUR_KEY" http://localhost:7700/nodes
```

---

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Gateway exits immediately | `journalctl -u emdex-gateway -n 50` — usually missing `GOOGLE_API_KEY` or `EMDEX_AUTH_KEY` |
| Node fails to register | Verify `EMDEX_GATEWAY_URL` and `EMDEX_GATEWAY_AUTH_KEY` match the gateway config |
| No results from search | Check `EMDEX_NAMESPACE` matches what was set on the indexing node |
| Qdrant unreachable | Confirm `QDRANT_HOST` is correct; test with `grpc_health_probe -addr=qdrant-host:6334` |
| SMB/NFS/SFTP connection error | Verify credentials, firewall rules, and that the share/export is mounted on the host |

Logs:
```bash
journalctl -u emdex-gateway -f
journalctl -u emdex-node -f
```

Qdrant dashboard: `http://qdrant-host:6333/dashboard`
