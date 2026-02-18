# Pocket-Omega

Go 语言 Agent 框架 —— 从多步思维链到全能 Agent。

## 快速开始

```bash
# 1. 复制并编辑环境变量
cp .env.example .env
# 编辑 .env，填写 LLM_API_KEY 等配置

# 2. 运行
go run ./cmd/omega/

# 3. 打开浏览器
# http://localhost:8080
```

## 配置说明

通过 `.env` 文件或系统环境变量配置：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `LLM_API_KEY` | — | API Key (必填) |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | 兼容 litellm / Ollama 等 |
| `LLM_MODEL` | `gpt-4o` | 模型名称 |
| `LLM_TEMPERATURE` | `0.7` | 创造性 0.0-2.0 |
| `LLM_MAX_TOKENS` | `0` (无限制) | 最大 token 数 |
| `LLM_MAX_RETRIES` | `3` | 重试次数 |
| `WEB_PORT` | `8080` | Web 服务端口 |

### 使用 litellm proxy

```bash
LLM_BASE_URL=http://localhost:4000
LLM_MODEL=gpt-4o
```

### 使用 Ollama

```bash
LLM_BASE_URL=http://localhost:11434/v1
LLM_MODEL=qwen2.5
LLM_API_KEY=ollama
```

## 项目架构

```
cmd/omega/          程序入口
internal/
  core/             流程框架 (Node/Flow, 泛型)
  llm/              LLM 抽象 (OpenAI-compatible)
  thinking/         思维链 (Chain of Thought)
  web/              Web UI (Go + HTMX)
pkg/config/         配置加载 (godotenv)
```

## 路线图

- [x] Step 1: CoT 思维链 + Web Chat UI
- [ ] Step 2: Skill 引擎 + MCP Client (工具调用)
- [ ] Step 3: 记忆系统
- [ ] Step N: 渠道网关 (WhatsApp / Telegram / 微信)

## 技术栈

- **语言**: Go 1.21+
- **LLM**: OpenAI-compatible 协议 (go-openai)
- **前端**: HTMX + Tailwind CSS
- **框架**: 自研泛型 Node/Flow (参考 PocketFlow)
