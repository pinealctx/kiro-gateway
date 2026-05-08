# Kiro Gateway

Kiro Gateway 将 Kiro / CodeWhisperer 后端封装为 OpenAI 与 Anthropic 兼容接口，并支持多个账号与多个 API Key 的绑定路由。

模块路径：`github.com/pinealctx/kiro-gateway`。二进制名称：`kiro-gateway`。

## 重要声明

本项目中的 Kiro 支持为非官方支持，仅用于个人测试与研究。受上游策略或协议变化影响，相关能力可能随时失效。

## 核心功能

- **多账号**：每个账号独立登录、刷新和持久化 token。
- **API Key 账号列表**：每个 API Key 必须绑定一个或多个允许使用的账号。
- **协议兼容**：支持 OpenAI `/v1/chat/completions` 与 Anthropic `/v1/messages`，也支持账号 URL。
- **Kiro 协议转换**：OpenAI / Anthropic 请求转换为 CodeWhisperer 私有协议。
- **Tool Use 支持**：过滤 IDE 内置工具，并尽量重映射到客户端真实工具。
- **Thinking 支持**：支持 `thinking` / `reasoning_effort`，流式输出 `reasoning_content`。
- **自动续写**：自动续写被截断的响应。
- **输出清洗**：移除 IDE 注入身份、XML 工具标签和 Kiro/CodeWhisperer 泄漏。
- **Web 管理后台**：管理账号、API Key、登录授权和用量查看。

## 快速开始

### 依赖

- Go 1.25+
- Node.js 20+（前端开发）

### 构建运行

```bash
go build -o kiro-gateway .
cp config.example.yaml config.yaml
./kiro-gateway --config config.yaml
```

启动后打开 `/ui`，使用启动日志中的 Admin Key 登录后台。

## 配置示例

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  log_level: "info"

auth:
  admin_key: ""           # 留空则启动时生成
  admin_local_only: true  # 默认只允许本机访问 /admin/*

tenant:
  db_path: "kiro-gateway.db"
```

使用 `--config` 或 `KIRO_GATEWAY_CONFIG` 指定配置文件。提供配置文件时，显式传入的 CLI 参数会覆盖文件值；未提供配置文件时，优先级为 CLI 参数、环境变量、默认值。

## 账号与 API Key

1. 在后台 `账号` 页面创建账号，例如 `kiro-main`、`kiro-work`。
2. 对每个账号执行 Kiro PKCE 登录，或导入本地 `kiro-cli` token。
3. 在 `API Keys` 页面创建 API Key，选择允许使用的账号，并设置默认账号。UI 会默认把第一个选中的账号作为默认账号。
4. 普通 `/v1/...` 路径使用该 Key 的默认账号；需要切换账号时，通过 URL 中的 `/a/{kiro_account}` 指定。

URL 指定的账号必须在该 API Key 的允许列表中。Claude Code 配 `http://localhost:8080` 会使用 Key 默认账号；配 `http://localhost:8080/a/kiro-work` 会强制使用 `kiro-work`。

账号名限制为 1-64 位：字母、数字、`.`、`_`、`-`，且首位必须是字母或数字。
Kiro Gateway 不再把模型绑定到账号或 API Key；请求里的 `model` 由客户端自行传入。
`/v1/models` 与 `/a/{kiro_account}/v1/models` 会先解析账号，再调用 Kiro 的 `ListAvailableModels` 查询该账号真实可用模型。
Claude Code 只会把 ID 以 `claude` 或 `anthropic` 开头的 gateway models 加入 `/model` 选择器，因此 Kiro Gateway 会用 `anthropic.` 前缀暴露真实 Kiro 模型（例如 `anthropic.deepseek-3.2`），请求上游前再去掉该前缀。Claude Code 侧需要启用 `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`。

## API 示例

### OpenAI 兼容

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "claude-sonnet-4.6",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

### Anthropic 兼容

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4.6",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

## 管理端点

业务 API 可以监听在公网地址；`/admin/*` 默认通过 `auth.admin_local_only` 限制为本机访问。

| 端点 | 方法 | 说明 |
|------|------|------|
| `/admin/accounts` | GET/POST | 列出或创建账号 |
| `/admin/accounts/:id` | GET/PUT/DELETE | 管理账号 |
| `/admin/keys` | GET/POST | 列出或创建 API Key |
| `/admin/keys/:id` | GET/PUT/DELETE | 管理 API Key |
| `/v1/chat/completions` | POST | 使用 Key 默认账号的 OpenAI 兼容聊天接口 |
| `/v1/models` | GET | 查询 Key 默认账号可用模型 |
| `/v1/messages` | POST | 使用 Key 默认账号的 Anthropic 兼容消息接口 |
| `/a/:kiro_account/v1/chat/completions` | POST | OpenAI 兼容聊天接口 |
| `/a/:kiro_account/v1/models` | GET | 查询指定账号可用模型 |
| `/a/:kiro_account/v1/messages` | POST | Anthropic 兼容消息接口 |
| `/admin/kiro/login` | POST | 启动 Kiro PKCE 登录 |
| `/admin/kiro/device-login` | POST | 启动 Kiro 设备码登录 |
| `/admin/kiro/import-local` | POST | 导入本地 kiro-cli token |
| `/admin/usage` | GET | 查看用量统计 |

## 开发

```bash
go test ./...
go build ./...

cd frontend
pnpm install
pnpm run build
```

前端生产构建会输出到 `web/static`，由 Go 二进制嵌入并通过 `/ui` 提供。
