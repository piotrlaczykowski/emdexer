# <img src="assets/logo.png" alt="Emdexer Logo" width="60"> Emdexer

**Distributed RAG Engine for Filesystem Intelligence.**  
*Turn any NAS, SMB share, S3 bucket, or local disk into a secure, semantic AI knowledge base.*

[![Go Version](https://img.shields.io/github/go-mod/go-v/piotrlaczykowski/emdexer?filename=src%2Fgateway%2Fgo.mod)](https://go.dev/)
[![Build Status](https://img.shields.io/github/actions/workflow/status/piotrlaczykowski/emdexer/fanout-ci.yml?branch=main)](https://github.com/piotrlaczykowski/emdexer/actions)
[![Latest Release](https://img.shields.io/github/v/release/piotrlaczykowski/emdexer)](https://github.com/piotrlaczykowski/emdexer/releases/latest)
[![License](https://img.shields.io/badge/license-BSL--1.1-blue)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[**Getting Started**](docs/getting-started/installation.md) | [**API Reference**](docs/reference/api.md) | [**Architecture**](docs/design/architecture.md) | [**HA Deployment**](docs/design/ha-infrastructure.md)

---

## ❓ Why Emdexer?

Most RAG (Retrieval-Augmented Generation) systems assume your data is already centralized or easily accessible via a single mount point. In reality, your data is scattered: documents on a NAS, archives in an S3 bucket, and code on your local NVMe.

**Emdexer stops the "data migration" madness.** Instead of bringing your data to the AI, Emdexer brings the indexing agent to your data.

*   **No more `mount -t cifs` headaches**: Index SMB or S3 directly via native protocols.
*   **No more network bottlenecks**: Extraction and embedding happen at the source.
*   **No more privacy leaks**: Keep your sensitive data on-premises with local Ollama support.

---

### 🚀 Features Grid

| | | |
| :--- | :--- | :--- |
| 🛡️ **Zero-Mount Indexing** | 🌐 **Protocol Agnostic** | 🧠 **Multi-Hop RAG** |
| Index directly where data lives. No central mounts or network bottlenecks. | Local FS, SMB, NFS, SFTP, and S3/MinIO streaming support. | Two-hop retrieval with LLM-driven query refinement. |
| 🔒 **Enterprise Auth** | ⚡ **Delta Sync** | 🔌 **OpenAI Compatible** |
| OIDC/JWT identity with group-based namespace isolation (ACLs). | 3-stage XXH3 change detection avoids redundant embedding calls. | Drop-in replacement for `/v1/chat/completions`. |
| 📁 **Format Mastery** | 🌍 **Global Search** | ☁️ **Air-Gap Ready** |
| PDF, Office, Media (Whisper), and OCR. Multi-modal extraction. | Parallel fan-out search across all namespaces with RRF merging. | Fully local embeddings and LLM via Ollama integration. |

---

### 🏠 The "LAN Brain"

Emdexer unifies your entire home or office network into a single, searchable knowledge base. Deploy lightweight nodes on your **MacBook**, **Windows PC**, **Linux Server**, and **NAS** — all without OS-level mounts or complex networking.

- **Search across all your computers** simultaneously with a single query.
- **Zero-Mount Discovery**: Nodes self-announce to the gateway via your local network.
- **No data leaves the LAN**: Extraction and vectorization happen locally; only embeddings travel to your secure database.

---

### 🛰️ Zero-Mount Distributed Flow

Emdexer breaks the "central mount" bottleneck. Nodes deploy directly alongside your data, streaming only vector embeddings to the central database.

```mermaid
sequenceDiagram
    participant S as Storage (NAS/S3/SMB)
    participant N as Emdex-Node (Local/Edge)
    participant G as Emdexer-Gateway (Central)
    participant Q as Qdrant (Vector DB)

    Note over S,N: Zero-Mount: Indexing at Source
    N->>S: Native Protocol (VFS) Scan
    S-->>N: File Stream (Memory-Only)
    N->>N: Text Extraction & Chunking
    N->>N: Generate Embeddings (Gemini/Ollama)
    N->>Q: Upsert Vectors (gRPC)
    N->>G: Register Presence & Namespaces
    
    Note over G,Q: Search Flow
    User->>G: Semantic Search / RAG Query
    G->>Q: Vector Similarity Search
    Q-->>G: Relevant Context
    G-->>User: AI Response + Citations
```

---

## ⚡ Quick Start (3 Minutes)

### Option A — Pre-built Binaries (fastest)

Download the latest binaries directly from [GitHub Releases](https://github.com/piotrlaczykowski/emdexer/releases/latest):

```bash
# Linux amd64 — Gateway
curl -L https://github.com/piotrlaczykowski/emdexer/releases/latest/download/emdex-gateway-linux-amd64 \
  -o emdex-gateway && chmod +x emdex-gateway

# Linux amd64 — Node
curl -L https://github.com/piotrlaczykowski/emdexer/releases/latest/download/emdex-node-linux-amd64 \
  -o emdex-node && chmod +x emdex-node
```

Available targets: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`

### Option B — Docker Compose (batteries included)

The fastest way to get everything running: Gateway, Node, and Qdrant in one command.

1. **Clone the repo**:
   ```bash
   git clone https://github.com/piotrlaczykowski/emdexer.git && cd emdexer/deploy/docker
   ```

2. **Configure environment**:
   ```bash
   cp ../../.env.example .env
   # Set your GOOGLE_API_KEY (or use Ollama) and a custom EMDEX_AUTH_KEY
   ```

3. **Fire it up**:
   ```bash
   docker compose up -d
   ```

4. **Verify search**:
   ```bash
   curl -H "Authorization: Bearer YOUR_AUTH_KEY" \
        "http://localhost:7700/v1/search?q=hello+world&namespace=default"
   ```

### Option C — Build from Source

```bash
git clone https://github.com/piotrlaczykowski/emdexer.git && cd emdexer
./scripts/install.sh --all   # interactive setup: gateway + node + systemd
```

See the full [Installation Guide](docs/getting-started/installation.md) for configuration details.

---

### 🎯 Who is it for?

*   🏠 **Homelab Enthusiasts**: Index decades of personal documents and media on a NAS with natural language search.
*   💻 **Developers**: Build a private AI assistant over local codebases and documentation without leaking data to third parties.
*   🏢 **Enterprises**: Deploy compliance-ready, air-gapped semantic search over internal knowledge bases with strict data sovereignty.
*   ⚖️ **Compliance Teams**: Enforce strict data boundaries using OIDC identity and namespace-isolated retrieval.

---

### 🌎 Real-World Use Cases

*   🏠 **NAS Semantic Search**: Search decades of family PDFs, tax returns, and media on your home NAS using natural language.
*   💻 **Private AI over Code**: Index your local `/projects` directory to give your AI agent deep context without ever uploading source code to the cloud.
*   🏢 **Enterprise Compliance**: Securely index internal department knowledge bases with strict OIDC-based namespace isolation and audit logging.

---

## 🛠️ Technical Differentiators

*   🛡️ **Zero-Mount Architecture**: Our nodes implement native VFS backends for SMB, SFTP, NFS, and S3. This eliminates the operational fragility and performance overhead of OS-level mount points.
*   ⚡ **Edge-Extraction**: Heavy multi-modal processing (OCR, Whisper transcription, PDF parsing) is performed by sidecars directly at the node level. Only lightweight vector embeddings travel to the central database.
*   📊 **RRF (Reciprocal Rank Fusion)**: When searching across multiple namespaces (`namespace=*`), the gateway fans out queries in parallel and merges results using RRF. This ensures the most relevant facts float to the top, regardless of which node they originated from.
*   🔄 **3-Stage Delta Sync**: XXH3 hash-based change detection at file, chunk, and embedding level — redundant embedding calls are skipped automatically.
*   🏗️ **HA-Ready**: 3-node Qdrant cluster (Raft consensus) with multi-replica Gateway behind Nginx. Statically linked Go binaries with no runtime dependencies.

---

## 📦 Releases & Docker Images

Pre-built binaries and Docker images are published automatically on every release via CI:

| Artifact | Location |
|---|---|
| Binaries | [GitHub Releases](https://github.com/piotrlaczykowski/emdexer/releases/latest) |
| Gateway image | `ghcr.io/piotrlaczykowski/emdexer-gateway:latest` |
| Node image | `ghcr.io/piotrlaczykowski/emdexer-node:latest` |
| Helm charts | `oci://ghcr.io/piotrlaczykowski/charts/emdexer-gateway` |

---

## 📚 Documentation

- [Installation Guide](docs/getting-started/installation.md)
- [Configuration Reference](docs/reference/configuration.md)
- [API Reference](docs/reference/api.md)
- [Architecture Overview](docs/design/architecture.md)
- [HA Deployment](docs/design/ha-infrastructure.md)

## 🤝 Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## 📄 License

Emdexer is licensed under the [Business Source License 1.1](LICENSE).
