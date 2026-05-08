# Kiro Gateway

[English](README.md) | [简体中文](README_CN.md)

Kiro Gateway 将 Kiro / CodeWhisperer 后端封装为 OpenAI 与 Anthropic 兼容接口。它更适合部署成一个小型共享网关：多 Kiro 账号、多客户端 API Key、账号 allow-list、本地优先的管理后台、请求日志、用量统计，以及可直接分发的单文件二进制。

模块路径：`github.com/pinealctx/kiro-gateway`。二进制名称：`kiro-gateway`。

## 重要声明

本项目中的 Kiro 支持为非官方支持，仅用于个人测试与研究。上游协议、模型权限、订阅策略都可能变化。

## 核心功能

| 功能 | 说明 |
|------|------|
| OpenAI API | 兼容 `/v1/chat/completions` 与 `/v1/models` |
| Anthropic API | 兼容 `/v1/messages` 与 `/v1/messages/count_tokens` |
| 账号路由 | 使用 API Key 默认账号，或通过 `/a/{kiro_account}` 强制指定账号 |
| API Key 管理 | 每个 Key 可绑定一个或多个允许使用的 Kiro 账号 |
| Web 管理后台 | 管理账号、API Key、登录状态、模型列表、额度和用量 |
| 内置 Kiro 登录 | 支持 PKCE/device-code 登录，也支持导入本地 `kiro-cli` token |
| 模型发现 | 调用 Kiro `ListAvailableModels`，并暴露 Claude Code 友好的模型 ID |
| 模型 normalization | 支持 `claude-sonnet-4-5`、版本号模型名、`claude-3-7-sonnet` 等格式 |
| Tool Calling | 转换工具定义/工具结果，并尽量把 Kiro 内置工具重映射到客户端真实工具 |
| Thinking 输出 | 支持 `thinking` / `reasoning_effort`，流式输出 `reasoning_content` |
| 流式保护 | 上游连接后长时间没有首个事件时自动重试 |
| Payload Guard | 请求过大时提前拒绝或自动裁剪旧历史，避免直接打到 Kiro 限制 |
| 用量统计 | 按 API Key 将用量统计持久化到 SQLite |
| 单文件部署 | Go 二进制内嵌 `web/static` 中的生产管理后台 |

## 安装

### 一行安装

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/pinealctx/kiro-gateway/main/scripts/install.sh | sh
```

默认安装目录是 `$HOME/.kiro-gateway/bin`。如果该目录不在 `PATH` 中，安装脚本会写入 shell profile。

Windows PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -Command "iwr https://raw.githubusercontent.com/pinealctx/kiro-gateway/main/scripts/install.ps1 -UseB | iex"
```

Linux/macOS 已安装二进制可直接原地升级：

```bash
kiro-gateway update
```

### Release 压缩包

也可以下载 release 压缩包后运行内置安装脚本：

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

如果本机已有 Go：

```bash
go install github.com/pinealctx/kiro-gateway@latest
kiro-gateway --help
```

`go install` 只安装二进制。配置文件可以自己创建，也可以从仓库下载 `config.example.yaml`。

### 从源码构建

```bash
git clone https://github.com/pinealctx/kiro-gateway.git
cd kiro-gateway
go build -o kiro-gateway .
cp config.example.yaml config.yaml
./kiro-gateway --config config.yaml
```

启动后打开 `http://127.0.0.1:8080/ui`，使用启动日志中的 Admin Key 登录后台。

## 配置

最小配置示例：

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  log_level: "info"
  cors_origins: []

auth:
  admin_key: ""           # 留空则启动时生成
  admin_local_only: true  # 默认只允许本机访问 /admin/*

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

使用 `--config` 或 `KIRO_GATEWAY_CONFIG` 指定配置文件。提供配置文件时，显式传入的 CLI 参数会覆盖文件值；未提供配置文件时，优先级为 CLI 参数、环境变量、默认值。

关键运行时配置：

| 配置 | 默认值 | 说明 |
|------|--------|------|
| `auth.admin_local_only` | `true` | 即使业务 API 监听 `0.0.0.0`，也限制 `/admin/*` 仅本机访问 |
| `first_token_timeout_seconds` | `15` | 上游流式响应迟迟没有首个事件时触发重试 |
| `first_token_max_retries` | `3` | 首事件超时的最大重试次数 |
| `max_payload_bytes` | `600000` | 序列化后的 Kiro 请求大小上限；设为 `0` 可禁用 |
| `auto_trim_payload` | `false` | 自动丢弃最旧历史，直到 payload 大小符合限制 |

## 账号与 API Key

1. 启动网关并打开 `/ui`。
2. 在 `Accounts` 页面创建一个或多个 Kiro 账号。
3. 对每个账号执行 Kiro PKCE/device-code 登录，或导入本地 `kiro-cli` token。
4. 在 `API Keys` 页面创建客户端 API Key。
5. 为每个 API Key 绑定允许使用的账号，并设置默认账号。

账号名限制为 1-64 位：字母、数字、`.`、`_`、`-`，且首位必须是字母或数字。

运行时账号选择：

| Base URL | 行为 |
|----------|------|
| `http://localhost:8080` | 使用 API Key 的默认 Kiro 账号 |
| `http://localhost:8080/a/kiro-work` | 强制使用 `kiro-work` 账号 |

URL 指定的账号必须在该 API Key 的允许列表中。

## 模型处理

Kiro Gateway 不把模型绑定到账号或 API Key。请求里的 `model` 由客户端传入，并在请求 Kiro 前做 normalization。

示例：

| 客户端模型名 | Kiro 模型名 |
|--------------|-------------|
| `claude-sonnet-4-5` | `claude-sonnet-4.5` |
| `claude-sonnet-4-5-20250929` | `claude-sonnet-4.5` |
| `claude-3-7-sonnet` | `claude-3.7-sonnet` |
| `claude-4.5-opus-high` | `claude-opus-4.5` |
| `anthropic.deepseek-3.2` | `deepseek-3.2` |

`/v1/models` 与 `/a/{kiro_account}/v1/models` 会先解析账号，再调用 Kiro 的 `ListAvailableModels` 查询该账号真实可用模型。

Claude Code 只会把 ID 以 `claude` 或 `anthropic` 开头的 gateway models 加入 `/model` 选择器，因此 Kiro Gateway 会给非 Claude 的 Kiro 模型加 `anthropic.` 发现前缀，请求上游前再去掉该前缀。Claude Code 侧需要启用：

```bash
CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

## 客户端示例

OpenAI 兼容请求：

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

Anthropic 兼容请求：

```bash
curl -X POST http://localhost:8080/a/kiro-work/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

Claude Code 示例：

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080/a/kiro-work
export ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

## API 参考

业务 API：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/chat/completions` | POST | 使用 Key 默认账号的 OpenAI 兼容聊天接口 |
| `/v1/models` | GET | 查询 Key 默认账号可用模型 |
| `/v1/messages` | POST | 使用 Key 默认账号的 Anthropic 兼容消息接口 |
| `/v1/messages/count_tokens` | POST | Anthropic 兼容 token 估算 |
| `/v1/kiro/usage-limits` | GET | 查询 Key 默认账号的 Kiro 用量/额度 |
| `/a/:kiro_account/v1/chat/completions` | POST | 指定账号的 OpenAI 兼容聊天接口 |
| `/a/:kiro_account/v1/models` | GET | 查询指定账号可用模型 |
| `/a/:kiro_account/v1/messages` | POST | 指定账号的 Anthropic 兼容消息接口 |
| `/a/:kiro_account/v1/messages/count_tokens` | POST | 指定账号的 token 估算 |
| `/a/:kiro_account/v1/kiro/usage-limits` | GET | 查询指定账号的 Kiro 用量/额度 |

管理 API 由 Admin Key 保护，且默认仅允许本机访问：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/admin/accounts` | GET/POST | 列出或创建账号 |
| `/admin/accounts/:id` | GET/PUT/DELETE | 管理账号 |
| `/admin/keys` | GET/POST | 列出或创建 API Key |
| `/admin/keys/:id` | GET/PUT/DELETE | 管理 API Key |
| `/admin/kiro/login` | POST | 启动 Kiro PKCE 登录 |
| `/admin/kiro/device-login` | POST | 启动 Kiro 设备码登录 |
| `/admin/kiro/import-local` | POST | 导入本地 `kiro-cli` token |
| `/admin/kiro/usage-limits` | GET | 查询 Kiro 用量/额度 |
| `/admin/kiro/models` | GET | 查询详细 Kiro 模型列表 |
| `/admin/usage` | GET | 查看用量统计 |

## 日志与用量

默认日志等级为 `info`。业务请求在认证后会记录 API Key name/id、路由、状态码、耗时和账号上下文。`debug` 等级会额外记录已脱敏的原始 HTTP 流量。

用量统计由网关计算并持久化到 SQLite。上游返回 usage 字段时优先使用上游值；否则使用轻量估算。

## 开发

依赖：

- Go 1.25+
- Node.js 20+
- pnpm 10+

常用命令：

```bash
go test ./...
go build ./...

cd frontend
pnpm install
pnpm run build
```

前端生产构建会输出到 `web/static` 并被 Go 二进制嵌入。Release workflow 会先构建前端，再交叉编译 Go 二进制，并打包 `config.example.yaml`、`README.md`、`LICENSE`、`install.sh`、`install.ps1`。
