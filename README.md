<div align="center">
  <img src="clawlet.png" alt="clawlet" width="500">
  <h1>Clawlet</h1>
  <h3>Ultra-lightweight and efficient personal AI assistant</h3>
</div>

**Clawlet** is a lightweight personal AI agent with hybrid semantic memory search — a single static binary with no runtime and no CGO.  
Bundled SQLite + sqlite-vec. Drop it on any machine and memory search just works.

This project is inspired by **OpenClaw** and **nanobot**.

## Install

Download from [GitHub Releases](https://github.com/mosaxiv/clawlet/releases/latest).

macOS (Apple Silicon):
```bash
curl -L https://github.com/mosaxiv/clawlet/releases/latest/download/clawlet_Darwin_arm64.tar.gz | tar xz
mv clawlet ~/.local/bin/
```

## Quick Start

```bash
# Initialize
clawlet onboard \
  --openrouter-api-key "sk-or-..." \
  --model "openrouter/anthropic/claude-sonnet-4.5"

# Check effective configuration
clawlet status

# Chat (global default workspace)
clawlet agent -m "What is 2+2?"

# Chat in a project-scoped workspace/session
clawlet agent --dir ./my-project -m "Summarize this repo"
```

## Project scope and sessions

`--dir` is the project boundary. With `--dir`, workspace state is stored in that project and the default session key is `default`:

- workspace: `{dir}`
- sessions: `{dir}/.clawlet/sessions/`
- gateway socket: `{dir}/.clawlet/gateway.sock`
- memory: `{dir}/memory/`

Without `--dir`, clawlet uses `~/.clawlet/workspace` and `~/.clawlet/sessions`.

## Configuration (`~/.clawlet/config.json`)

Config file: `~/.clawlet/config.json`

### Supported providers

clawlet currently supports these LLM providers:

- **OpenAI** (`openai/<model>`, API key: `env.OPENAI_API_KEY`)
- **OpenAI Codex (OAuth)** (`openai-codex/<model>`, no API key; login: `clawlet provider login openai-codex`)
- **OpenRouter** (`openrouter/<provider>/<model>`, API key: `env.OPENROUTER_API_KEY`)
- **Anthropic** (`anthropic/<model>`, API key: `env.ANTHROPIC_API_KEY`)
- **Gemini** (`gemini/<model>`, API key: `env.GEMINI_API_KEY` or `env.GOOGLE_API_KEY`)
- **Local (Ollama / vLLM / OpenAI-compatible local endpoint)** (`ollama/<model>` or `local/<model>`, default base URL: `http://localhost:11434/v1`, API key optional)

Minimal config (OpenRouter):

```json
{
  "env": { "OPENROUTER_API_KEY": "sk-or-..." },
  "agents": { "defaults": { "model": "openrouter/anthropic/claude-sonnet-4-5" } }
}
```

Agent generation defaults are configurable:

```json
{
  "agents": {
    "defaults": {
      "model": "openrouter/anthropic/claude-sonnet-4-5",
      "maxTokens": 8192,
      "temperature": 0.7
    }
  }
}
```

Minimal config (Local via Ollama):

```json
{
  "agents": { "defaults": { "model": "ollama/qwen2.5:14b" } }
}
```

Minimal config (Local via vLLM using the same `ollama/` route):

```json
{
  "agents": { "defaults": { "model": "ollama/meta-llama/Llama-3.1-8B-Instruct" } },
  "llm": { "baseURL": "http://localhost:8000/v1" }
}
```

OpenAI Codex (OAuth):

```bash
# one-time login
clawlet provider login openai-codex

# headless environment (SSH / container)
clawlet provider login openai-codex --device-code
```

```json
{
  "agents": { "defaults": { "model": "openai-codex/gpt-5.1-codex" } }
}
```

### Option: Memory search setup

To enable semantic memory search, add `memorySearch` to the agent defaults:

```json
{
  "env": {
    "OPENAI_API_KEY": "sk-..."
  },
  "agents": {
    "defaults": {
      "memorySearch": {
        "enabled": true,
        "provider": "openai",
        "model": "text-embedding-3-small"
      }
    }
  }
}
```

Local embedding (Ollama / OpenAI-compatible local endpoint):

```json
{
  "agents": {
    "defaults": {
      "memorySearch": {
        "enabled": true,
        "provider": "openai",
        "model": "nomic-embed-text",
        "remote": {
          "baseURL": "http://localhost:11434/v1"
        }
      }
    }
  }
}
```

When enabled:
- The agent gains `memory_search` and `memory_get` tools for retrieving past context.
- clawlet indexes `MEMORY.md`, `memory.md`, and `memory/**/*.md` for retrieval.
- The index DB is created at `{workspace}/.memory/index.sqlite`.

When disabled (default):
- `memorySearch.enabled` defaults to `false`; the search tools are not exposed to the model.
- Memory files (`memory/MEMORY.md`, `memory/YYYY-MM-DD.md`) are still injected into context as usual.
- Normal chat behavior is otherwise unchanged.


## Security

### Secure Defaults
- `tools.restrictToWorkspace` defaults to `true` (tools can only access files inside the workspace directory)
- `clawlet gateway` listens on a Unix domain socket at `{dir}/.clawlet/gateway.sock` (or `~/.clawlet/workspace/.clawlet/gateway.sock` without `--dir`)
- Gateway no longer binds a TCP port or exposes external chat integrations.

### Security Checklist

| Item | Status | Details |
| --- | --- | --- |
| Gateway not publicly exposed | ✅ | Gateway uses a local Unix domain socket instead of a TCP listener. |
| Filesystem scoped (no `/`) | ✅ | File tools block root path, path traversal, encoded traversal, symlink escapes, and sensitive state paths. |
| Exec tool dangerous-command guard | ✅ | `exec` blocks unsafe shell constructs (command chaining, unsafe expansions, redirection/`tee`, dangerous patterns), blocks sensitive paths, and passes only allowlisted environment variables to subprocesses. |

## Tools

### Multimodal input (audio/image/attachments)

Internal inbound messages can include attachments. clawlet can:

- send images to vision-capable models,
- transcribe audio using the configured provider,
- and inline text-like file attachments into the user context.

Configure under `tools.media` (the values below are the current default values):

```json
{
  "tools": {
    "media": {
      "enabled": true,
      "audioEnabled": true,
      "imageEnabled": true,
      "attachmentEnabled": true,
      "maxAttachments": 4,
      "maxFileBytes": 20971520,
      "maxInlineImageBytes": 5242880,
      "maxTextChars": 12000,
      "downloadTimeoutSec": 20
    }
  }
}
```

## Gateway (TUI / internal clients)

External chat integrations (Discord / Slack / Telegram / WhatsApp) have been removed.
`clawlet gateway` now accepts only internal HTTP requests over a Unix domain socket.

```bash
# Project-scoped gateway
clawlet gateway --dir ./my-project

# Global default workspace
clawlet gateway
```

Socket path:

- with `--dir`: `{dir}/.clawlet/gateway.sock`
- without `--dir`: `~/.clawlet/workspace/.clawlet/gateway.sock`

HTTP endpoints over the Unix socket:

```http
GET /api/health
POST /api/chat
Content-Type: application/json

{"message":"hello","session_key":"default"}
```

`session_key` is optional. With `--dir`, the default is `default` and is shared with `clawlet agent --dir ...`. Without `--dir`, gateway defaults to `gateway:default` and CLI defaults to `cli:default`.

## CLI Reference

| Command | Description |
| --- | --- |
| `clawlet onboard` | Initialize a workspace and write a minimal config. |
| `clawlet status` | Print the effective configuration (after defaults and routing). |
| `clawlet agent` | Run the agent in CLI mode (interactive or single message). |
| `clawlet gateway [--dir DIR]` | Run the long-lived Unix-socket gateway (internal TUI/API + cron + heartbeat). |
| `clawlet cron list` | List scheduled jobs. |
| `clawlet cron add` | Add a scheduled job. |
| `clawlet cron remove` | Remove a scheduled job. |
| `clawlet cron toggle` | Enable/disable a scheduled job. |
| `clawlet cron run` | Run a job immediately. |

### `clawlet cron add` formats

`--message` is required, and exactly one of `--every`, `--cron`, or `--at` must be set.

```bash
# Every N seconds
clawlet cron add --message "summarize my inbox" --every 3600

# Cron expression (5-field)
clawlet cron add --message "daily standup notes" --cron "0 9 * * 1-5"

# Run once at a specific time (RFC3339)
clawlet cron add --message "remind me" --at "2026-02-10T09:00:00Z"

# Use a project and explicit session key
clawlet cron add --dir ./my-project --session default --message "ping" --every 600
```
## 🐳 Docker

### Using Pre-built Images

Pre-built images are available on GitHub Container Registry:

```yaml
# docker-compose.yml
services:
  clawlet:
    image: ghcr.io/mosaxiv/clawlet:latest
    volumes:
      - ~/.clawlet:/root/.clawlet
    command: gateway
    restart: unless-stopped
```

```bash
docker compose up -d
```

### Building Locally

```bash
# Build the image
docker build -t clawlet .

# Initialize config (first time only)
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet onboard

# Edit config on host to add API keys
vim ~/.clawlet/config.json

# Run the gateway
docker run -v ~/.clawlet:/root/.clawlet clawlet gateway

# Or run a single command
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet agent -m "Hello"
docker run -v ~/.clawlet:/root/.clawlet --rm clawlet status
```
