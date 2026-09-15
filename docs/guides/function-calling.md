# Function Calling Reference

NXD uses function calling to get structured outputs from LLMs instead of parsing free text.

## How It Works

1. NXD sends tool definitions (JSON Schema) in the LLM request
2. The model responds with `tool_calls` — structured name + arguments
3. NXD validates each call against the schema
4. NXD executes the tool (creates a story, submits a review, etc.)

## Supported Models

Native tool calling: Gemma 4 (all variants) via Ollama, Anthropic (Claude), OpenAI (GPT), Google AI.

For other models (DeepSeek, Qwen, CodeLlama) via Ollama: schemas are injected into the system prompt and JSON is parsed from text output. This is the path a qwen reviewer takes when you configure one (`qwen3-coder:30b`, or the budget `qwen2.5-coder:14b`), and is fully supported — not a legacy/degraded mode. JSON parsing has been hardened with markdown-fence stripping and per-field validation (see `ParseToolCallsFromText` in `internal/llm/tool_compat.go`).

## Tool Definitions by Role

### Tech Lead (Planner)

| Tool | Description |
|------|-------------|
| `create_story` | Create a story with title, description, complexity (1-13), acceptance criteria, dependencies |
| `set_wave_plan` | Define parallel execution waves |
| `request_clarification` | Pause pipeline for user input |

Example:
```json
{"name": "create_story", "arguments": {"title": "Auth module", "description": "JWT authentication", "complexity": 5, "acceptance_criteria": "Login returns token", "dependencies": []}}
```

### Reviewer (Senior)

| Tool | Description |
|------|-------------|
| `submit_review` | Verdict (approve/request_changes/reject), summary, file comments, suggested changes |
| `request_more_context` | Request additional files before review |

### Supervisor

Not called today: the supervisor is never constructed (`NewSupervisor` has no callers).

| Tool | Description |
|------|-------------|
| `report_drift` | Report scope_creep/stuck/quality_regression/dependency_blocked with severity and recommendation |
| `reprioritize` | Move story to different execution wave |

### Manager

Not called by `nxd resume` today: the monitor's Manager is not attached, so tier-2 stories are re-dispatched to Senior (see the Escalation Ladder section of [Architecture](architecture.md)).

| Tool | Description |
|------|-------------|
| `escalation_decision` | Decide action: reassign_higher_tier/split_story/mark_blocked/retry/abandon |
| `split_story` | Break stuck story into smaller pieces |

### Native Runtime (Coding)

| Tool | Description |
|------|-------------|
| `read_file(path)` | Read file contents |
| `write_file(path, content)` | Create or overwrite file |
| `edit_file(path, old_text, new_text)` | Surgical text replacement |
| `run_command(command)` | Execute allowlisted shell command |
| `task_complete(summary)` | Signal work done; accepted only after `qa.success_criteria` pass |
| `write_scratchboard(category, content)` | Share a discovery with other agents on the same requirement |
| `read_scratchboard(category?)` | Read shared discoveries, optionally filtered by category |

If a model writes tool calls as plain JSON objects (`{"name": ..., "arguments": ...}`) in its reply text instead of structured `tool_calls`, the native runtime extracts and executes them (`extractInlineToolCalls` in `internal/runtime/inline_tools.go`).

## Validation and Retry

1. Required fields validated against JSON Schema
2. Enum fields validated (e.g., verdict must be approve/request_changes/reject)
3. Invalid calls: error returned to model, max 2 retries
4. After max retries: falls back to free-text parsing

## Graceful Degradation

`HasToolSupport(provider, model)` auto-detects tool support. No config changes needed.

- Gemma 4 / Anthropic / OpenAI: Native structured outputs
- DeepSeek / Qwen / CodeLlama: Text-based JSON parsing (same as before)
