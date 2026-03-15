# INFRASTRUCTURE.md — Technical Reference for Sub-Agents

## ClawBoard Backend
- **Host:** LXC .156 (`192.168.0.156`)
- **Source:** `/opt/clawboard/backend/`
- **Deploy:** `cd /opt/clawboard && docker compose build backend && docker compose up -d backend`
- **API:** `https://board.laczyk.com` (via NPM on .133)
- **ClawBoard API Token:** `3641d53b873a265ce6e8f7d757bfb0c3299650110a7ad269ab73987efbb0193c`
  - Use this for ALL ClawBoard API calls: `Authorization: Bearer 3641d53b873a265ce6e8f7d757bfb0c3299650110a7ad269ab73987efbb0193c`
  - **This is NOT the same as the OpenClaw Gateway tokens below**

## SSH Access (MANDATORY — ONE KEY ONLY)
```bash
ssh -i ~/.ssh/piotr.laczykowski.ed -o StrictHostKeyChecking=no root@192.168.0.156
```
- Key lives on pazurbot VM (`.153`, user `piotr.laczykowski`)
- **No other keys work** on .156 (`id_ed25519`, `id_rsa`, `id_subagent`, etc. are all rejected)
- **No sshpass**, no Proxmox (`.21`) workarounds

## OpenClaw Gateway
- **Host:** `pazurbot.laczyk.com`
- **Port:** `18789`
- **TLS:** `true` → protocols are computed by code (`https://`, `wss://`)
- **Do NOT hardcode protocols in env** — code derives them from `OPENCLAW_GATEWAY_TLS`

### Two Tokens (NEVER confuse them)
| Token | Env Var | Use |
|---|---|---|
| **Gateway Token** | `OPENCLAW_GATEWAY_TOKEN` | Gateway management/admin |
| **Auth/Hook Token** | `OPENCLAW_AUTH_TOKEN` | **Spawning agents** via `/v1/chat/completions` AND **GUI login** at `https://pazurbot.laczyk.com:18789` |

> ℹ️ The **Auth/Hook Token** (`OPENCLAW_AUTH_TOKEN`) is the credential used to log in to the OpenClaw web GUI at **https://pazurbot.laczyk.com:18789**. It is also used for webhook callbacks and agent spawning. Do NOT use the Gateway Token for these purposes.

Using `OPENCLAW_GATEWAY_TOKEN` for agent spawning = 401 Unauthorized = stuck tasks.

### Config centralization (`app/core/config.py`)
All OpenClaw settings are centralized in `Settings`:
```python
OPENCLAW_GATEWAY_HOST: str = "pazurbot.laczyk.com"
OPENCLAW_GATEWAY_PORT: int = 18789
OPENCLAW_GATEWAY_TLS: bool = True
OPENCLAW_GATEWAY_TOKEN: str = ""   # management
OPENCLAW_AUTH_TOKEN: str = ""      # spawning agents

@property
def openclaw_http_url(self) -> str:
    scheme = "https" if self.OPENCLAW_GATEWAY_TLS else "http"
    return f"{scheme}://{self.OPENCLAW_GATEWAY_HOST}:{self.OPENCLAW_GATEWAY_PORT}/v1/chat/completions"
```

## PostgreSQL
- **Host:** `192.168.0.154:5432`
- **User:** `postgres` / **Password:** `Crooptown#2012`
- **DB:** `clawboard`
- Always use parameters — `#` in password breaks URL parsers

## Deployment Rules (NO EXCEPTIONS)
- ⛔ **NEVER `docker exec`** to edit files inside containers
- ⛔ **NEVER `docker cp`** — always full rebuild
- ✅ Edit files on **host** (`/opt/clawboard/backend/app/`) → `docker compose build && up`
- Every code change = mandatory rebuild before marking task `done`

---

## Git Protection Rules (ALL repos)
- ⛔ **Direct pushes to `main` and `develop` are FORBIDDEN**
- ✅ All changes MUST go through a Pull Request
- PRs require at least one review before merge
- Branch naming: `feat/`, `fix/`, `docs/`, `chore/`, `hotfix/`

---

## CI/CD Architecture — Emdexer

| Property | Value |
|---|---|
| **Registry** | GitHub Container Registry (`ghcr.io/piotrlaczykowski/emdexer`) |
| **Flow** | Fan-out builds triggered by path filtering per component (`gateway/`, `node/`, `mcp/`) |
| **Reusable workflow** | `.github/workflows/docker-build-template.yml` called from `fanout-ci.yml` |
| **Tagging strategy** | Branch-driven suffixes: `-beta` (beta branches), `-rc` (release candidates), `-hotfix` (hotfixes), `-alpha` (feature/experimental), `-PR` (pull request builds) |
| **Main tag** | `latest` on merge to `main`; `develop` on merge to `develop` |
