# Clawlet Maintenance TODO

This document captures the current architectural and maintenance gaps in `clawlet`, with concrete reference points from both this repository and `picoclaw-origin`.

## Goals

- Keep `clawlet` small and understandable.
- Improve debuggability and observability before adding richer UI.
- Make future work such as TUI, tool-call visibility, and provider UX easier to implement safely.
- Prefer importing structural ideas from `picoclaw-origin`, not wholesale complexity.

## Priority 1: Unify The Turn Execution Path

### Problem

`clawlet` currently has two similar but separate execution paths:

- CLI agent path in `agent/agent.go`
- Gateway/bus path in `agent/loop.go`

This causes behavior drift and duplicated maintenance work.

### Current Clawlet References

- `agent/agent.go`
- `agent/loop.go`
- `cmd/clawlet/cmd_agent.go`
- `cmd/clawlet/cmd_gateway.go`

### Evidence

- Tool-call loop logic is duplicated in `agent/agent.go` and `agent/loop.go`.
- System prompt construction is duplicated in `agent/agent.go` and `agent/loop.go`.
- CLI and gateway already differ in prompt content and observability behavior.

### Proposal

- Extract a shared turn runner used by both CLI and gateway.
- Extract a shared system-prompt builder with mode-specific options instead of separate prompt implementations.
- Keep transport concerns outside the shared runner.

### Picoclaw-Origin References

- `pkg/agent/loop.go`
- `pkg/agent/turn.go`
- `pkg/agent/subturn.go`

### Notes

Do not copy PicoClaw's full loop complexity. Only borrow the idea that turn execution should be centralized.

### Status: ✅ DONE (2026-05-06)

- Extracted `TurnRunner` in `agent/turn.go` — shared turn execution used by both CLI (`Agent`) and gateway (`Loop`).
- Extracted `BuildSystemPrompt` in `agent/prompt.go` — shared system-prompt builder with mode-specific `PromptOpts`.
- `ToolEvent` / `TurnObserver` / `ToolPhase` types moved to `agent/turn.go`.
- `agent/agent.go` (185 lines) and `agent/loop.go` (280 lines) are now thin wrappers delegating to `TurnRunner.Run()`.
- Added `agent/turn_test.go` with 10 tests covering turn execution, prompt building, observer events, and max-iters limits.
- TurnRunner mutates session in-memory; callers handle persistence (CLI uses `session.Save`, gateway uses `sessions.Save`).

## Priority 2: Add Structured Events And An Event Bus

### Problem

`clawlet` mostly exposes runtime behavior through ad-hoc `stderr` prints. That is too weak for debug tooling, TUI, trace capture, or reliable test assertions.

### Current Clawlet References

- `agent/turn.go`
- `agent/agent.go`
- `agent/loop.go`

### Evidence

- Tool-call logging is a direct `fmt.Fprintf` in `cmd/clawlet/cmd_agent.go` via `TurnObserver`.
- Gateway verbose mode does not emit comparable per-step events.
- There is no structured runtime event type that UI, logs, and tests can all consume.

### Proposal

- Add a minimal event model and in-process event bus.
- Start with a small event set:
  - `turn_start`
  - `turn_end`
  - `llm_request`
  - `llm_response`
  - `tool_exec_start`
  - `tool_exec_end`
  - `warning`
  - `error`
  - `session_saved`
- Make `--verbose` an event subscriber instead of hard-coded print statements.
- Use the same events for future TUI panes and tests.

### Picoclaw-Origin References

- `pkg/agent/events.go`
- `pkg/agent/eventbus.go`
- `pkg/agent/hooks_test.go`
- `pkg/agent/eventbus_test.go`

### Notes

This is the single highest-leverage improvement for future maintainability.

## Priority 3: Make Tool Execution Observable

### Problem

`clawlet` only records tool names in session history. That is not enough for debugging tool behavior or building a tool-call UI.

### Current Clawlet References

- `tools/registry.go`
- `session/session.go`
- `agent/turn.go`

### Evidence

- `session.Message` only stores `ToolsUsed []string`.
- `tools.Registry.Execute` is a large central switch with no structured lifecycle hooks.
- There is no common representation for tool args preview, duration, status, or output preview.

### Proposal

- Introduce a `ToolExecution` or equivalent trace payload with:
  - tool name
  - args preview
  - start/end time
  - duration
  - error state
  - truncated output preview
- Emit tool lifecycle events before and after each tool execution.
- Decide separately whether full tool traces should be persisted or only exposed in memory.

### Picoclaw-Origin References

- `pkg/agent/events.go`
- `pkg/agent/loop.go`
- `pkg/tools/`

### Notes

The goal is observability, not necessarily long-term full transcript persistence.

## Priority 4: Improve Session Persistence And Failure Visibility

### Problem

Important persistence failures are currently ignored or underreported.

### Current Clawlet References

- `agent/agent.go`
- `agent/loop.go`
- `session/session.go`

### Evidence

- Session save results are ignored in `agent/agent.go` and `agent/loop.go`.
- A successful visible reply can still fail to persist, and the user may never know.

### Proposal

- Surface session save failures through events and verbose logging.
- Consider warning the user in CLI mode when persistence fails.
- Keep the current JSONL persistence shape unless there is a strong need to migrate.

### Picoclaw-Origin References

- `pkg/session/manager.go`
- `pkg/session/jsonl_backend.go`
- `pkg/session/jsonl_backend_test.go`

### Notes

PicoClaw's storage layer is more elaborate; the main lesson is to avoid silent failure and snapshot data before slow I/O.

## Priority 5: Add Better Provider And Model UX

### Problem

Provider authentication and model selection are functional but awkward for maintenance and day-to-day use.

### Current Clawlet References

- `cmd/clawlet/cmd_provider.go`
- `llm/openai_codex_oauth.go`
- `cmd/clawlet/cmd_agent.go`
- `cmd/clawlet/config.go`
- `config/config.go`

### Evidence

- `clawlet agent` cannot override provider/model from flags.
- OAuth state is imported from Codex CLI files, which can surprise users.
- There is no `provider whoami`, `provider logout`, or `provider login --force`.

### Proposal

#### A. Model visibility (`clawlet model`)

Today `clawlet status` is the only way to see the active model, and it's buried in a wall of config output.

- Add `clawlet model` (or `clawlet model show`) — concise one-line output:
  `model: openai-codex/gpt-5.2 (provider: openai-codex, base: https://chatgpt.com/backend-api)`
- Add `clawlet model list` — list known model prefixes and their default base URLs:
  ```
  openai/           → https://api.openai.com/v1
  openai-codex/     → https://chatgpt.com/backend-api (OAuth)
  openrouter/       → https://openrouter.ai/api/v1
  anthropic/        → https://api.anthropic.com
  gemini/           → https://generativelanguage.googleapis.com/v1beta
  ollama/ (local/)  → http://localhost:11434/v1
  shengsuanyun/     → https://router.shengsuanyun.com/api/v1
  novita/           → https://api.novita.ai/openai
  ```
- Add `clawlet model set <model>` — write into `agents.defaults.model` in config.json, then print the new effective config.

#### B. CLI flag override (`clawlet agent --model`)

- Add `clawlet agent --model` — overrides config for a single invocation without touching config.json.
- Optionally add `--max-tokens` and `--temperature` later if needed.
- `--model` takes the same `<prefix>/<name>` format (e.g. `openrouter/anthropic/claude-sonnet-4-5`).
- When `--model` is given, the OAuth flow is automatically selected for `openai-codex/`.

#### C. Provider management

- Add:
  - `clawlet provider whoami openai-codex`
  - `clawlet provider logout openai-codex`
  - `clawlet provider login openai-codex --force`
- Add `clawlet status --json` for easier debugging and future UI integration.

### Picoclaw-Origin References

- `pkg/agent/model_resolution.go`
- `pkg/providers/`
- `pkg/providers/codex_provider_test.go`
- `pkg/providers/codex_cli_credentials_test.go`
- `pkg/config/model_config_test.go`

### Notes

Do not import PicoClaw's full provider matrix. The main takeaway is that model/provider resolution deserves explicit, testable interfaces.

## Priority 6: Introduce A Minimal Command Layer

### Problem

`clawlet` currently relies heavily on free-form chat. That is fine for basic use, but maintenance-focused commands will become increasingly useful.

### Current Clawlet References

- `cmd/clawlet/cmd_agent.go`
- `cmd/clawlet/cmd_channels.go`
- `cmd/clawlet/cmd_provider.go`

### Proposal

- Introduce a small internal command registry for local slash-style commands and control actions.
- Candidate commands:
  - `/trace`
  - `/tools`
  - `/sessions`
  - `/provider`
  - `/model`

### Picoclaw-Origin References

- `pkg/commands/registry.go`
- `pkg/commands/definition.go`
- `pkg/commands/executor_test.go`

### Notes

This should stay much smaller than PicoClaw's command system.

## Priority 7: Add TUI On Top Of Events, Not Just Stdout

### Problem

The current interactive CLI is intentionally simple, but it is too limited for maintaining an agent system with tools and multiple runtime states.

### Current Clawlet References

- `cmd/clawlet/cmd_agent.go`
- `agent/agent.go`
- `channels/channels.go`
- `channels/manager.go`
- `bus/bus.go`

### Proposal

- Build the first TUI as an event subscriber plus local input surface.
- Initial panes/views should likely include:
  - conversation view
  - tool-call trace view
  - status/session view
  - error/warning view
- Keep the transport boundary clean so TUI can later act like a local channel if useful.

### Picoclaw-Origin References

- `pkg/agent/events.go`
- `pkg/agent/eventbus.go`
- `pkg/channels/manager.go`
- `web/backend/`

### Notes

Do not start with a web UI. TUI is the better first step for local maintenance and debugging.

## Priority 8: Evolve Channels Toward Optional Capabilities

### Problem

`clawlet` channels are intentionally minimal, but richer UX features need more than simple send/receive behavior.

### Current Clawlet References

- `channels/channels.go`
- `channels/manager.go`

### Proposal

- Keep `Channel` small, but add optional interfaces for richer features:
  - placeholder support
  - typing indicators
  - message editing
  - streaming output
- Make TUI the first consumer and implementation target.

### Picoclaw-Origin References

- `pkg/channels/base.go`
- `pkg/channels/manager.go`
- channel-specific packages under `pkg/channels/`

### Notes

Borrow the capability idea, not the full manager complexity.

## Priority 9: Improve Test Coverage Around Real Execution Paths

### Problem

`clawlet` has useful package tests, but its most important runtime paths are still under-tested.

### Current Clawlet References

- Existing tests under:
  - `agent/consolidation_test.go`
  - `channels/*_test.go`
  - `config/config_test.go`
  - `tools/*_test.go`
  - `session/session_test.go`
  - `llm/*_test.go`

### Gaps

- ✅ Shared turn runner test coverage added in `agent/turn_test.go` (10 tests).
- Weak coverage for CLI provider flows.
- Weak coverage for future event emission and trace behavior.

### Proposal

- Add tests for:
  - shared turn runner behavior
  - tool-call iteration and final response assembly
  - session persistence warnings/errors
  - provider login state transitions
  - model override behavior
  - event emission order and payloads
- Prefer fake LLM clients and fake tools over brittle integration-heavy tests.

### Picoclaw-Origin References

- `pkg/agent/loop_test.go`
- `pkg/agent/instance_test.go`
- `pkg/agent/registry_test.go`
- `pkg/providers/*_test.go`
- `pkg/channels/manager_test.go`

### Notes

PicoClaw is much more test-heavy. The key takeaway is not test count, but protecting important system boundaries.

## Priority 10: Keep Clawlet Small While Avoiding Centralized Complexity

### Problem

`clawlet` is still small enough to understand end-to-end, which is a major strength. That strength will be lost if new features are only added to existing central files.

### Current Clawlet Hotspots

- `tools/registry.go`
- `cmd/clawlet/config.go`
- `config/config.go`

Note: `agent/agent.go` and `agent/loop.go` were slimmed down in Priority 1; turn execution is centralized in `agent/turn.go` (149 lines).

### Proposal

- Use small, explicit abstractions only where they unlock future work.
- Avoid importing large frameworks or deep abstraction layers too early.
- When adding new features, prefer:
  - structured event interfaces
  - optional capabilities
  - small helper packages
- Avoid pushing more unrelated responsibilities into the existing large files.

### Picoclaw-Origin References

- `pkg/agent/`
- `pkg/channels/`
- `pkg/providers/`
- `pkg/commands/`

### Notes

The right goal is not to turn `clawlet` into PicoClaw. The right goal is to adopt only the structural ideas that solve real pain without losing simplicity.

## Recommended Execution Order

1. ✅ Extract a shared turn runner. (done: `agent/turn.go`, `agent/prompt.go`)
2. Add a minimal event model and event bus.
3. Route `--verbose` through event subscriptions.
4. Add tool execution trace payloads.
5. Add `agent --model` and better provider commands.
6. Surface persistence failures.
7. Add shared-turn and event-focused tests. (partial: `agent/turn_test.go` covers turn execution)
8. Build a minimal TUI on top of events.
9. Add optional channel capabilities only when the TUI or channel UX needs them.

## Anti-Goals

- Do not import PicoClaw's full loop, hook, routing, or provider complexity up front.
- Do not build web UI first.
- Do not add abstractions with no immediate consumer.
- Do not let debug features depend on fragile ad-hoc `stderr` parsing.

## Reference Repositories

### Clawlet

- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/agent/agent.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/agent/loop.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/tools/registry.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/session/session.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/channels/channels.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/channels/manager.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/cmd/clawlet/cmd_agent.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/cmd/clawlet/cmd_gateway.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/cmd/clawlet/cmd_provider.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/cmd/clawlet/config.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/config/config.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/llm/client.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/clawlet/llm/openai_codex_oauth.go`

### Picoclaw-Origin

- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/agent/events.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/agent/eventbus.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/agent/loop.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/agent/model_resolution.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/agent/loop_test.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/channels/base.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/channels/manager.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/session/manager.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/session/jsonl_backend.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/commands/registry.go`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/pkg/providers/`
- `/Users/shoichi-t/dev/my-hobby/openclaw/picoclaw-origin/web/backend/`
