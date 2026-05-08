# Kiro Gateway

[English](README.md) | [简体中文](README_CN.md)

Kiro Gateway exposes OpenAI and Anthropic-compatible APIs backed by Kiro / CodeWhisperer. It is designed for running a small shared gateway: multiple Kiro accounts, multiple client API keys, account allow-lists, a local-first admin UI, request logging, usage statistics, and release-ready single binary deployment.

Module path: `github.com/pinealctx/kiro-gateway`. Binary name: `kiro-gateway`.

## Notice

Kiro support is unofficial and intended for personal testing and research. Upstream protocols, model access, and subscription policy may change without notice.

## Features

| Feature | Description |
|---------|-------------|
| OpenAI API | `/v1/chat/completions` and `/v1/models` compatible routes |
| Anthropic API | `/v1/messages` and `/v1/messages/count_tokens` compatible routes |
| Account routing | Use an API key default account or force an account with `/a/{kiro_account}` |
| API key management | Each key can be bound to one or more Kiro accounts |
| Web admin UI | Manage accounts, API keys, login state, model list, quotas, and usage |
| Built-in Kiro login | Supports PKCE/device-code login and local `kiro-cli` token import |
| Model discovery | Queries Kiro `ListAvailableModels`; exposes Claude Code-friendly model IDs |
| Model normalization | Accepts names like `claude-sonnet-4-5`, versioned IDs, and legacy `claude-3-7-sonnet` |
| Tool calling | Converts tool definitions/results and remaps selected Kiro built-in tools to client tools |
| Thinking output | Supports `thinking` / `reasoning_effort` and streams `reasoning_content` |
| Stream resilience | Retries when upstream connects but produces no first stream event |
| Payload guard | Rejects or trims overlarge Kiro payloads before upstream submission |
| Usage statistics | Persists per-key usage summaries in SQLite |
| Single binary | Go binary embeds the production admin UI from `web/static` |

## Installation

### One-Line Install

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/pinealctx/kiro-gateway/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -Command "iwr https://raw.githubusercontent.com/pinealctx/kiro-gateway/main/scripts/install.ps1 -UseB | iex"
```

Set `INSTALL_DIR` to choose a custom install directory.

### Release Archive

You can also download a release archive and run the included installer:

```bash
tar -xzf kiro-gateway_vX.Y.Z_linux_amd64.tar.gz
cd kiro-gateway_vX.Y.Z_linux_amd64
sh install.sh
```

```powershell
Expand-Archive .\kiro-gateway_vX.Y.Z_windows_amd64.zip
cd .\kiro-gateway_vX.Y.Z_windows_amd64\kiro-gateway_vX.Y.Z_windows_amd64
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

### Go Install

If you already have Go installed:

```bash
go install github.com/pinealctx/kiro-gateway@latest
kiro-gateway --help
```

`go install` installs only the binary. Create your own config file, or download `config.example.yaml` from the repository.

### Build From Source

```bash
git clone https://github.com/pinealctx/kiro-gateway.git
cd kiro-gateway
go build -o kiro-gateway .
cp config.example.yaml config.yaml
./kiro-gateway --config config.yaml
```

Open `http://127.0.0.1:8080/ui` and log in with the Admin Key printed in the startup logs.

## Configuration

Minimal configuration:

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  log_level: "info"
  cors_origins: []

auth:
  admin_key: ""           # generated at startup when empty
  admin_local_only: true  # restrict /admin/* to localhost clients by default

defaults:
  health_check_enabled: true
  health_check_seconds: 60
  first_token_timeout_seconds: 15
  first_token_max_retries: 3
  max_payload_bytes: 600000
  auto_trim_payload: false

tenant:
  db_path: "kiro-gateway.db"
```

Use `--config` or `KIRO_GATEWAY_CONFIG` to choose a config file. When a config file is provided, explicitly set CLI flags override file values; otherwise CLI flags override environment variables, which override defaults.

Important runtime options:

| Option | Default | Description |
|--------|---------|-------------|
| `auth.admin_local_only` | `true` | Keeps `/admin/*` local-only even when public API routes listen on `0.0.0.0` |
| `first_token_timeout_seconds` | `15` | Retries a stream if upstream sends no first event in this window |
| `first_token_max_retries` | `3` | Number of first-event timeout retries |
| `max_payload_bytes` | `600000` | Maximum serialized Kiro request size; set `0` to disable |
| `auto_trim_payload` | `false` | Drops oldest history entries until the payload fits |

## Accounts and API Keys

1. Start the gateway and open `/ui`.
2. Create one or more Kiro accounts in the `Accounts` page.
3. Authorize each account with Kiro PKCE/device-code login, or import a local `kiro-cli` token.
4. Create API keys in the `API Keys` page.
5. Bind each API key to the accounts it may use and choose a default account.

Account names must be 1-64 characters: letters, numbers, `.`, `_`, and `-`; the first character must be a letter or number.

Runtime account selection:

| Base URL | Behavior |
|----------|----------|
| `http://localhost:8080` | Uses the API key's default Kiro account |
| `http://localhost:8080/a/kiro-work` | Forces the `kiro-work` account |

The URL account must be in the API key allow-list.

## Model Handling

Kiro Gateway does not bind models to accounts or API keys. The request `model` is supplied by the client and normalized before it reaches Kiro.

Examples:

| Client model | Kiro model |
|--------------|------------|
| `claude-sonnet-4-5` | `claude-sonnet-4.5` |
| `claude-sonnet-4-5-20250929` | `claude-sonnet-4.5` |
| `claude-3-7-sonnet` | `claude-3.7-sonnet` |
| `claude-4.5-opus-high` | `claude-opus-4.5` |
| `anthropic.deepseek-3.2` | `deepseek-3.2` |

`/v1/models` and `/a/{kiro_account}/v1/models` resolve the account first and query Kiro's `ListAvailableModels` API for that account.

Claude Code only adds discovered gateway models whose IDs start with `claude` or `anthropic`, so Kiro Gateway exposes non-Claude Kiro model IDs with an `anthropic.` discovery prefix and strips that prefix before calling Kiro. Enable Claude Code discovery with:

```bash
CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

## Client Examples

OpenAI-compatible request:

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

Anthropic-compatible request:

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

Claude Code example:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080/a/kiro-work
export ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

## API Reference

Runtime APIs:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | OpenAI-compatible chat completions using the key default account |
| `/v1/models` | GET | List Kiro models available to the key default account |
| `/v1/messages` | POST | Anthropic-compatible messages using the key default account |
| `/v1/messages/count_tokens` | POST | Anthropic-compatible token estimate |
| `/v1/kiro/usage-limits` | GET | Kiro usage/quota limits for the key default account |
| `/a/:kiro_account/v1/chat/completions` | POST | OpenAI-compatible chat completions for a selected account |
| `/a/:kiro_account/v1/models` | GET | List Kiro models for a selected account |
| `/a/:kiro_account/v1/messages` | POST | Anthropic-compatible messages for a selected account |
| `/a/:kiro_account/v1/messages/count_tokens` | POST | Token estimate for a selected account |
| `/a/:kiro_account/v1/kiro/usage-limits` | GET | Kiro usage/quota limits for a selected account |

Admin APIs are protected by the Admin Key and local-only by default:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/accounts` | GET/POST | List or create accounts |
| `/admin/accounts/:id` | GET/PUT/DELETE | Manage an account |
| `/admin/keys` | GET/POST | List or create API keys |
| `/admin/keys/:id` | GET/PUT/DELETE | Manage API keys |
| `/admin/kiro/login` | POST | Start Kiro PKCE login |
| `/admin/kiro/device-login` | POST | Start Kiro device-code login |
| `/admin/kiro/import-local` | POST | Import local `kiro-cli` token |
| `/admin/kiro/usage-limits` | GET | Query Kiro usage/quota limits |
| `/admin/kiro/models` | GET | Query detailed Kiro model list |
| `/admin/usage` | GET | Usage statistics |

## Logging and Usage

Default log level is `info`. Runtime request logs include the API key name/id when a request is authenticated, the selected route, status, latency, and account context. Debug mode adds raw HTTP traffic logging with secret redaction.

Usage statistics are calculated by the gateway and persisted in SQLite. When upstream usage fields are present they are used; otherwise the gateway falls back to a lightweight token estimate.

## Development

Requirements:

- Go 1.25+
- Node.js 20+
- pnpm 10+

Commands:

```bash
go test ./...
go build ./...

cd frontend
pnpm install
pnpm run build
```

The frontend production build is emitted to `web/static` and embedded by the Go binary. Release builds run the frontend build first, then cross-compile the Go binary and package `config.example.yaml`, `README.md`, `LICENSE`, `install.sh`, and `install.ps1`.
