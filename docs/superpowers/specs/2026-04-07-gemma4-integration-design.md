# Gemma 4 Integration Design Spec

**Date:** 2026-04-07
**Status:** Approved
**Branch:** `feat/gemma4-integration`

## Overview

Integrate Google's Gemma 4 into NXD as the new default model family, replacing DeepSeek Coder V2 and Qwen2.5-Coder. This is a three-layer change: provider infrastructure, structured function calling across all LLM-facing roles, and a native Gemma coding runtime.

### Target Model

`gemma4:26b` (Mixture-of-Experts) — 25.2B total params, 3.8B active per token, 256K context, Apache 2.0 license. Available via Ollama and Google AI Studio free tier.

### Target Hardware

MacBook Pro M4, 24GB unified memory. The 26B MoE model at Q4_K_M uses ~10GB total (weights + runtime overhead), leaving ~14GB for macOS and applications.

### Constraints

- **Offline-first remains top priority.** Local Ollama is the primary inference path.
- **Free usage only.** Google AI Studio free tier is a convenience, not a dependency. No paid APIs.
- **Backward compatible.** Existing DeepSeek/Qwen configs continue to work unchanged. Function calling degrades gracefully to free-text parsing for non-Gemma models.

---

## Section 1: Provider Layer — Google AI Client + FallbackClient

### Google AI Client

**New file:** `internal/llm/google.go`

Implements the existing `llm.Client` interface targeting the Google AI Studio REST API (`generativelanguage.googleapis.com/v1beta`). Maps NXD's `CompletionRequest` to Google's `generateContent` format. Authentication via `GOOGLE_AI_API_KEY` environment variable.

Key responsibilities:
- Map `CompletionRequest.Messages` to Google's `contents` array format
- Map `CompletionRequest.Tools` to Google's `functionDeclarations` format
- Parse `functionCall` responses into `ToolCall` structs
- Handle Google-specific error codes (429 rate limit, 403 quota exhausted)

### FallbackClient

**New file:** `internal/llm/fallback.go`

Wraps two `llm.Client` implementations with automatic failover:

```
FallbackClient
  primary:        GoogleClient (free tier)
  fallback:       OllamaClient (local)
  quotaExhausted: atomic bool
  resetTimer:     resets quotaExhausted after configurable cooldown
```

Behavior:
1. Calls primary. On success, returns normally.
2. On HTTP 429 or 403 from primary, sets `quotaExhausted = true`, logs a warning, retries the **same request** on fallback. Caller never sees the error.
3. While `quotaExhausted` is true, all calls go directly to fallback (skip primary).
4. After cooldown period (default 60s, configurable via `fallback_cooldown_s`), resets `quotaExhausted` to false and tries primary again on next call.

### Config

New provider value `google+ollama` triggers FallbackClient construction. When `GOOGLE_AI_API_KEY` is not set, degrades to `ollama` only with an info-level log message (not an error).

```yaml
models:
  tech_lead:
    provider: google+ollama
    model: gemma4:26b           # Ollama model name
    google_model: gemma-4-26b   # Google AI Studio model name
    max_tokens: 16000
    fallback_cooldown_s: 60
```

Existing provider values (`ollama`, `anthropic`, `openai`) continue to work unchanged.

### ModelConfig Changes

**File:** `internal/config/config.go`

```go
type ModelConfig struct {
    Provider          string `yaml:"provider"`
    Model             string `yaml:"model"`
    GoogleModel       string `yaml:"google_model"`       // NEW
    MaxTokens         int    `yaml:"max_tokens"`
    FallbackCooldownS int    `yaml:"fallback_cooldown_s"` // NEW, default 60
}
```

### Client Construction

**File:** `internal/cli/req.go` — `buildLLMClient()` updated:

```
switch provider:
  "ollama"         → OllamaClient
  "anthropic"      → AnthropicClient
  "openai"         → OpenAIClient
  "google"         → GoogleClient
  "google+ollama"  → FallbackClient(GoogleClient, OllamaClient)
```

---

## Section 2: Function Calling Framework

### Tool Protocol Types

**New file:** `internal/llm/tools.go`

```go
type ToolDefinition struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

type ToolCall struct {
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
}
```

### Extended Request/Response

**File:** `internal/llm/client.go` — Add to existing structs:

```go
type CompletionRequest struct {
    // ... existing fields ...
    Tools      []ToolDefinition // Available tools for this request
    ToolChoice string           // "auto", "required", or specific tool name
}

type CompletionResponse struct {
    // ... existing fields ...
    ToolCalls []ToolCall // Structured calls from model
}
```

### Provider-Specific Handling

- **Google AI** — Native. Maps `ToolDefinition` to `functionDeclarations`. Parses `functionCall` from response.
- **Ollama** — Native (v0.20+). Uses OpenAI-compatible tool format. Gemma 4's `<|tool_call|>` tokens handled transparently by Ollama.
- **Anthropic** — Native. Maps to Anthropic's `tools` format.
- **OpenAI** — Native. Already uses the same format.

### Graceful Degradation

**New file:** `internal/llm/tool_compat.go`

```go
func HasToolSupport(provider, model string) bool
```

If true: send tools natively via the provider's tool API.

If false: inject the tool schema into the system prompt as JSON instructions ("You must respond with JSON matching this schema...") and parse the text response as JSON. This keeps everything working for older models (DeepSeek Coder V2, Qwen2.5-Coder, CodeLlama) that don't support tool calling.

### Validation and Retry

Every tool call is validated against its schema before execution. Invalid calls (e.g., complexity of 15, unknown verdict) return an error message to the model in a follow-up message, giving it a chance to self-correct. Maximum 2 retries per invalid call, then fall back to free-text parsing for that request.

---

## Section 3: Role-Specific Tool Schemas

### Tech Lead (Planner)

**New file:** `internal/engine/planner_tools.go`

```
create_story(title, description, complexity, acceptance_criteria, dependencies[])
    complexity: integer 1-13 (Fibonacci)
    dependencies: array of story IDs (must reference previously created stories)
    Called once per story during decomposition.

set_wave_plan(waves[][story_ids])
    Defines execution order. Each wave is an array of story IDs.
    Called once after all stories are created.

request_clarification(question, context)
    Called if the requirement is ambiguous.
    NXD pauses the pipeline and surfaces the question to the user.
```

### Reviewer (Senior)

**New file:** `internal/engine/reviewer_tools.go`

```
submit_review(verdict, summary, file_comments[], suggested_changes[])
    verdict: "approve" | "request_changes" | "reject"
    file_comments: [{file: string, line: int, severity: "critical"|"major"|"minor", message: string}]
    suggested_changes: [{file: string, old_text: string, new_text: string}]

request_more_context(files[], reason)
    Ask for additional file contents before completing the review.
    NXD reads the requested files and sends them in a follow-up message.
```

### Supervisor

**New file:** `internal/engine/supervisor_tools.go`

```
report_drift(story_id, drift_type, severity, recommendation)
    drift_type: "scope_creep" | "stuck" | "quality_regression" | "dependency_blocked"
    severity: "low" | "medium" | "high" | "critical"
    recommendation: "continue" | "reassign" | "escalate" | "pause"

reprioritize(story_id, new_wave, reason)
    Move a story to a different execution wave.
    NXD validates the new wave doesn't create circular dependencies.
```

### Manager

**New file:** `internal/engine/manager_tools.go`

```
escalation_decision(story_id, action, reason, assigned_to)
    action: "reassign_higher_tier" | "split_story" | "mark_blocked" | "retry" | "abandon"
    assigned_to: agent ID (only for reassign_higher_tier)

split_story(original_story_id, new_stories[]{title, description, complexity})
    Breaks a stuck story into smaller pieces.
    NXD creates the new stories, links dependencies, and re-plans waves.
```

---

## Section 4: Native Gemma Runtime

### Runtime Implementation

**New file:** `internal/runtime/gemma.go`

A coding runtime that talks directly to Ollama (or Google AI via FallbackClient) using Gemma 4's function calling. No external CLI dependency.

### Coding Tools

```
read_file(path) → returns file contents
write_file(path, content) → creates or overwrites a file
edit_file(path, old_text, new_text) → surgical text replacement
run_command(command) → execute shell command, return stdout/stderr
task_complete(summary, files_changed[]) → signal work is done
```

### Execution Loop

1. NXD creates a tmux session (same as Aider runtime).
2. Injects system prompt: story description, acceptance criteria, repo context, file tree.
3. Sends initial message with available coding tools to Gemma 4.
4. Model responds with tool calls (`read_file` -> `edit_file` -> `run_command` -> ...).
5. NXD executes each tool call, returns results to model.
6. Loop continues until model calls `task_complete` or max iterations reached.
7. NXD captures the git diff for the review stage.

### Safety Guardrails

- `write_file` and `edit_file` restricted to the story's git worktree (path traversal rejected).
- `run_command` has a configurable allowlist. Default: `go build ./...`, `go test ./...`, `npm test`, `npm run build`, `make`, `make test`. Commands not on the allowlist are rejected with an error returned to the model.
- Max iterations per story: 20 (configurable).
- Max total tokens per session: configurable, default based on model's context window.

### Config

```yaml
runtimes:
  gemma:
    command: ""
    native: true
    max_iterations: 20
    command_allowlist:
      - "go build ./..."
      - "go test ./..."
      - "npm test"
      - "npm run build"
      - "make"
      - "make test"
    models:
      - "gemma4"
  aider:
    # ... existing config unchanged ...
```

### Runtime Selection

The runtime registry selects `gemma` when:
1. The configured runtime is `gemma`, OR
2. The model name starts with `gemma4` and no explicit runtime is set (auto-detection).

Aider remains available for non-Gemma models or when explicitly configured.

---

## Section 5: Config, Model Registry, and Defaults

### New Defaults

All roles default to `gemma4:26b` via `google+ollama` provider:

```yaml
models:
  tech_lead:     {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 16000}
  senior:        {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 8000}
  intermediate:  {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 4000}
  junior:        {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 4000}
  qa:            {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 8000}
  supervisor:    {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 4000}
  manager:       {provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b, max_tokens: 8000}
```

### Updated Model Registry

**File:** `internal/llm/models.go`

Gemma 4 family added to `RecommendedModels()`:

| Model | Params | Min RAM | Recommended Role | Notes |
|-------|--------|---------|-----------------|-------|
| `gemma4:26b` | 26B (3.8B active) | 12GB | All roles | MoE, best quality/VRAM ratio |
| `gemma4:31b` | 31B | 20GB | tech_lead, senior | Dense, highest quality |
| `gemma4:e4b` | 4.5B | 6GB | junior | Lightweight, fast |
| `gemma4:e2b` | 2.3B | 4GB | junior | Constrained devices only |

Legacy models (DeepSeek, Qwen) retained in registry for backward compatibility.

### Hardware Tier Tables

| Setup | RAM | Model Config | Disk | Notes |
|-------|-----|-------------|------|-------|
| Minimal | 16GB | `gemma4:e4b` all roles | ~10GB | Function calling works, lower code quality |
| **Recommended** | **24GB** | **`gemma4:26b` all roles** | **~18GB** | **Best quality/perf ratio** |
| Full Team | 64GB+ | `gemma4:31b` (lead/senior) + `gemma4:26b` (rest) | ~38GB | Maximum quality |
| Legacy | Any | DeepSeek/Qwen | Varies | No function calling, free-text parsing |

### Config Validation Updates

**File:** `internal/config/config.go`

- Add `"google"` and `"google+ollama"` to valid provider values.
- Validate `google_model` is non-empty when provider contains `google`.
- Validate `fallback_cooldown_s` is positive when set (default 60).
- Validate native runtime `max_iterations` is positive when set.
- Validate `command_allowlist` is non-empty for native runtimes.

---

## Section 6: Documentation

### New Documents

**`docs/guides/gemma-4-guide.md`** — Primary onboarding tutorial:
1. Why Gemma 4 (MoE efficiency, function calling, Apache 2.0, offline-first)
2. Quick start (`ollama pull gemma4:26b` -> `nxd init` -> `nxd req`)
3. Google AI free tier setup (optional) — API key, env var, fallback behavior
4. Hardware tuning for M4/24GB — Ollama settings, expected inference speed
5. Function calling explained — what it is, why outputs are more reliable, how to observe it in event logs
6. Choosing a runtime — `gemma` native vs `aider`, comparison table
7. Troubleshooting — model too large, Ollama timeout, Google AI quota hit

**`docs/guides/function-calling.md`** — Technical reference:
- All tool definitions per role with parameter types and example payloads
- Validation and retry behavior
- Graceful degradation for non-Gemma models
- Adding custom tools (advanced users)

**`docs/guides/migration-from-v0.md`** — Upgrade guide for existing users:
- What changed and why
- Step-by-step migration (pull model, update config, verify)
- Config diff: old defaults vs new defaults
- "Keep your existing setup" section — existing configs work unchanged

### Updated Documents

- **`docs/guides/getting-started.md`** — Quick Start rewritten for Gemma 4, DeepSeek/Qwen moved to "Alternative Setup"
- **`docs/guides/model-selection.md`** — Gemma 4 family added, comparison tables updated
- **`docs/guides/configuration.md`** — Document `google+ollama` provider, `google_model`, `fallback_cooldown_s`, native runtime config
- **`docs/guides/troubleshooting.md`** — Gemma 4 and Google AI specific issues added
- **`README.md`** — Prerequisites, Quick Start, Hardware Recommendations, and Agent Roles table updated

---

## Files Changed Summary

### New Files (12)

| File | Purpose |
|------|---------|
| `internal/llm/google.go` | Google AI Studio client |
| `internal/llm/fallback.go` | FallbackClient with automatic failover |
| `internal/llm/tools.go` | Tool calling protocol types |
| `internal/llm/tool_compat.go` | Graceful degradation for non-tool models |
| `internal/engine/planner_tools.go` | Tech Lead tool schemas |
| `internal/engine/reviewer_tools.go` | Reviewer tool schemas |
| `internal/engine/supervisor_tools.go` | Supervisor tool schemas |
| `internal/engine/manager_tools.go` | Manager tool schemas |
| `internal/runtime/gemma.go` | Native Gemma coding runtime |
| `docs/guides/gemma-4-guide.md` | User onboarding tutorial |
| `docs/guides/function-calling.md` | Tool calling reference |
| `docs/guides/migration-from-v0.md` | Upgrade guide |

### Modified Files (13)

| File | Change |
|------|--------|
| `internal/llm/client.go` | Add Tools/ToolCalls to request/response |
| `internal/llm/models.go` | Add Gemma 4 to registry |
| `internal/llm/ollama.go` | Handle tool calling in requests/responses |
| `internal/llm/anthropic.go` | Handle tool calling in requests/responses |
| `internal/llm/openai.go` | Handle tool calling in requests/responses |
| `internal/config/config.go` | Add ModelConfig fields, validation |
| `internal/config/loader.go` | Update defaults to Gemma 4 |
| `internal/engine/planner.go` | Use tool calling when available |
| `internal/engine/reviewer.go` | Use tool calling when available |
| `internal/engine/supervisor.go` | Use tool calling when available |
| `internal/engine/manager.go` | Use tool calling when available |
| `internal/runtime/registry.go` | Register native Gemma runtime |
| `internal/cli/req.go` | Handle google/google+ollama providers |

### Updated Docs (5)

| File | Change |
|------|--------|
| `README.md` | Prerequisites, Quick Start, Hardware, Agent Roles |
| `docs/guides/getting-started.md` | Gemma 4 as primary, legacy as alternative |
| `docs/guides/model-selection.md` | Gemma 4 family, comparison tables |
| `docs/guides/configuration.md` | New provider, fields, native runtime |
| `docs/guides/troubleshooting.md` | Gemma 4 specific issues |

### Test Files (new)

| File | Purpose |
|------|---------|
| `internal/llm/google_test.go` | Google AI client unit tests |
| `internal/llm/fallback_test.go` | Fallback behavior and quota tests |
| `internal/llm/tools_test.go` | Tool serialization and validation |
| `internal/llm/tool_compat_test.go` | Degradation path tests |
| `internal/engine/planner_tools_test.go` | Planner tool schema validation |
| `internal/engine/reviewer_tools_test.go` | Reviewer tool schema validation |
| `internal/runtime/gemma_test.go` | Native runtime loop and guardrail tests |
| `internal/llm/models_test.go` | Updated for new recommended models |
| `internal/config/config_test.go` | Updated for new defaults and validation |
