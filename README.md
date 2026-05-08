# Kiro Gateway

[English](README.md) | [简体中文](README_CN.md)

Kiro Gateway exposes OpenAI and Anthropic-compatible endpoints backed by Kiro / CodeWhisperer, with support for multiple accounts and multiple API keys bound to different accounts.

Module path: `github.com/pinealctx/kiro-gateway`. Binary name: `kiro-gateway`.

## Notice

Kiro support is unofficial and intended for personal testing/research. It may break if upstream protocols or policies change.

## Features

- Multiple accounts, each with independent login, refresh, and persisted token state.
- API keys must bind to one or more allowed accounts.
- OpenAI `/v1/chat/completions` and Anthropic `/v1/messages` compatibility, with optional account-scoped URLs.
- CodeWhisperer request conversion and AWS EventStream parsing.
- Tool-use handling, IDE built-in tool filtering, and best-effort remapping to client tools.
- `thinking` / `reasoning_effort` support with `reasoning_content` streaming.
- Auto-continuation for truncated responses.
- Output sanitization for Kiro / CodeWhisperer identity leaks and XML tool tags.
- Web admin UI for accounts, API keys, login, and usage.

## Quick Start

```bash
go build -o kiro-gateway .
cp config.example.yaml config.yaml
./kiro-gateway --config config.yaml
```

Open `/ui` and log in with the Admin Key printed in the startup logs.

## Configuration

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  log_level: "info"

auth:
  admin_key: ""           # generated at startup when empty
  admin_local_only: true  # restrict /admin/* to localhost clients by default

tenant:
  db_path: "kiro-gateway.db"
```

Use `--config` or `KIRO_GATEWAY_CONFIG` to choose a config file. When a config file is provided, explicitly set CLI flags override file values; otherwise CLI flags override environment variables, which override defaults.

## Accounts and API Keys

1. Create accounts in the `Accounts` admin page, for example `kiro-main` and `kiro-work`.
2. Authorize each account with Kiro PKCE login, or import the local `kiro-cli` token.
3. Create API keys, select the allowed accounts, and choose a default account. The UI uses the first selected account as the default unless you change it.
4. Use plain `/v1/...` routes for the API key's default account, or select another allowed account at request time with `/a/{kiro_account}` in the base URL.

The requested URL account must be in the API key allow-list. For Claude Code, `http://localhost:8080` uses the key default account, while `http://localhost:8080/a/kiro-work` forces `kiro-work`.

Account names must be 1-64 characters: letters, numbers, `.`, `_`, and `-`; the first character must be a letter or number.
Kiro Gateway does not bind models to accounts or API keys; the request `model` is supplied by the client.
`/v1/models` and `/a/{kiro_account}/v1/models` resolve the account first and query Kiro's `ListAvailableModels` API for that account.
Claude Code only adds discovered gateway models whose IDs start with `claude` or `anthropic`, so Kiro Gateway exposes real Kiro model IDs with an `anthropic.` discovery prefix (for example `anthropic.deepseek-3.2`) and strips that prefix before calling Kiro. Enable Claude Code discovery with `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`.

## API Examples

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "claude-sonnet-4.6",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4.6",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## Admin API

Runtime APIs can be exposed on a public listen address, while `/admin/*` is restricted to localhost by default through `auth.admin_local_only`.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/accounts` | GET/POST | List or create accounts |
| `/admin/accounts/:id` | GET/PUT/DELETE | Manage an account |
| `/admin/keys` | GET/POST | List or create API keys |
| `/admin/keys/:id` | GET/PUT/DELETE | Manage API keys |
| `/v1/chat/completions` | POST | OpenAI-compatible chat completions using the key default account |
| `/v1/models` | GET | List Kiro models available to the key default account |
| `/v1/messages` | POST | Anthropic-compatible messages using the key default account |
| `/a/:kiro_account/v1/chat/completions` | POST | OpenAI-compatible chat completions |
| `/a/:kiro_account/v1/models` | GET | List Kiro models available to the selected account |
| `/a/:kiro_account/v1/messages` | POST | Anthropic-compatible messages |
| `/admin/kiro/login` | POST | Start Kiro PKCE login |
| `/admin/kiro/device-login` | POST | Start Kiro device-code login |
| `/admin/kiro/import-local` | POST | Import local kiro-cli token |
| `/admin/usage` | GET | Usage statistics |

## Development

```bash
go test ./...
go build ./...

cd frontend
pnpm install
pnpm run build
```

The frontend production build is emitted to `web/static` and served by the Go binary at `/ui`.
