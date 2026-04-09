# NXD Configuration Guide

NXD is configured via `nxd.yaml` in your project directory. Run `nxd init` to generate a default config, or copy `nxd.config.example.yaml`.

## Configuration File Location

NXD looks for config in this order:
1. `--config <path>` flag (any command)
2. `nxd.yaml` in the current directory

## Full Reference

### workspace

```yaml
workspace:
  state_dir: ~/.nxd           # Where NXD stores events, DB, logs
  backend: sqlite              # "sqlite" (offline) or "dolt" (version-controlled)
  log_level: info              # debug, info, warn, error
  log_retention_days: 30       # How long to keep session logs
```

**state_dir** is expanded from `~` at runtime. All NXD state lives here: events.jsonl, nxd.db, logs/, worktrees/.

### models

Maps each agent role to a specific LLM provider and model.

```yaml
models:
  tech_lead:
    provider: ollama                     # ollama, google+ollama, google, anthropic, or openai
    model: gemma4:26b                    # Model name (Ollama tag or API model ID)
    google_model: gemma-4-26b-a4b-it     # Google AI model ID (used by google+ollama and google providers)
    max_tokens: 16000                    # Max output tokens
  senior:
    provider: ollama
    model: gemma4:26b
    max_tokens: 8000
  intermediate:
    provider: ollama
    model: gemma4:26b
    max_tokens: 4000
  junior:
    provider: ollama
    model: gemma4:26b
    max_tokens: 4000
  qa:
    provider: ollama
    model: gemma4:26b
    max_tokens: 8000
  supervisor:
    provider: ollama
    model: gemma4:26b
    max_tokens: 4000
  fallback_cooldown_s: 60                # Seconds to wait before retrying cloud provider after quota error
```

**Providers:**

| Provider | Endpoint | Auth | Offline? | Notes |
|----------|----------|------|----------|-------|
| `ollama` | `http://localhost:11434` | None | Yes | Default, fully offline |
| `google+ollama` | Google AI + Ollama fallback | `GOOGLE_AI_API_KEY` | Partial | Uses Google AI first, falls back to Ollama on 429 |
| `google` | `https://generativelanguage.googleapis.com` | `GOOGLE_AI_API_KEY` | No | Google AI only (no fallback) |
| `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` | No | |
| `openai` | `https://api.openai.com` | `OPENAI_API_KEY` | No | |

**Google AI setup (optional):**

```bash
# Get a free API key at https://ai.google.dev
export GOOGLE_AI_API_KEY=your-key-here
```

When using `google+ollama`, NXD sends requests to Google AI first. If the free tier quota is exhausted (HTTP 429), it automatically falls back to the local Ollama model and retries Google AI after `fallback_cooldown_s` seconds. If `GOOGLE_AI_API_KEY` is not set, the `google+ollama` provider behaves identically to `ollama`.

The `google_model` field specifies the model name for Google AI API calls (e.g., `gemma-4-26b-a4b-it`). This is separate from the `model` field, which is the Ollama tag.

> **Authentication note:** These API keys are used for NXD's **internal operations** only -- planning, code review, and QA. They are **not** passed to spawned coding agents. If you use Claude Code as a runtime, it authenticates via its own OAuth session (your Max/Pro subscription via `claude login`), so spawned agents incur no additional API cost. The API key is only consumed by the lightweight internal LLM calls (a few per story per stage).

You can mix providers -- for example, use Ollama for juniors and Google AI for the Tech Lead.

### routing

Controls how stories are assigned to agent tiers based on Fibonacci complexity scores.

```yaml
routing:
  junior_max_complexity: 3              # Stories 1-3 go to Junior
  intermediate_max_complexity: 5        # Stories 4-5 go to Intermediate
  max_retries_before_escalation: 2      # Retry count before escalating
  max_qa_failures_before_escalation: 3  # QA fails before escalating
```

**Complexity scoring (Fibonacci):**

| Score | Tier | Example Task |
|-------|------|-------------|
| 1 | Junior | Fix a typo, update a constant |
| 2 | Junior | Add a simple utility function |
| 3 | Junior | Create a basic CRUD endpoint |
| 5 | Intermediate | Implement a service with validation |
| 8 | Senior | Design a new subsystem |
| 13 | Senior (decompose first) | Major architectural change |

Stories scored 9-13 are automatically decomposed further by the Senior before assignment.

### monitor

Controls the Watchdog and Supervisor monitoring loops.

```yaml
monitor:
  poll_interval_ms: 10000       # How often Watchdog checks sessions (10s)
  stuck_threshold_s: 120        # Seconds of no output before "stuck" (2min)
  context_freshness_tokens: 150000  # Token limit before context refresh
```

**How stuck detection works:**
1. Watchdog captures the last 30 lines of each tmux pane
2. Computes a SHA-256 fingerprint of the output
3. If the fingerprint hasn't changed after `stuck_threshold_s`, the agent is flagged as stuck
4. Stuck agents are escalated (Junior -> Senior -> Tech Lead -> Human)

**Watchdog also auto-handles:**
- Permission prompts (`[Y/n]`) — auto-approves with "Y"
- Plan mode (`Plan mode`) — sends Escape to exit

### cleanup

Controls post-merge cleanup behavior.

```yaml
cleanup:
  worktree_prune: immediate     # "immediate" (delete after merge) or "deferred"
  branch_retention_days: 7      # Days to keep merged branches (0 = delete immediately)
  log_archive: file             # "file", "dolt", or "none"
```

**Cleanup timeline:**
1. **Immediate:** Worktree deleted right after merge
2. **Deferred:** Worktree kept until `nxd gc` runs
3. **Branch GC:** `nxd gc` deletes branches older than `branch_retention_days`

### updates

Controls automatic model update checks.

```yaml
update_check: true                # Enable/disable model update checks on startup
update_interval_hours: 168        # Hours between checks (default: 168 = weekly)
```

When enabled, NXD runs `nxd models check` on startup to see if newer versions of your configured models are available in Ollama. Disable with `update_check: false` for fully offline environments.

### merge

Controls how completed stories are integrated.

```yaml
merge:
  auto_merge: true         # Automatically merge after review + QA pass
  base_branch: main        # Target branch for merges
  mode: local              # "local" (offline git merge) or "github" (push + PR)
  pr_template: |           # Template for PR body (github mode only)
    ## Story: {story_id}
    {description}
    ### Acceptance Criteria
    {acceptance_criteria}
```

**Merge modes:**

| Mode | What Happens | Network? |
|------|-------------|----------|
| `local` | `git merge --no-ff <branch>` into base | No |
| `github` | Push branch, create PR via `gh`, auto-merge | Yes |

In `local` mode, stories still emit `STORY_PR_CREATED` and `STORY_MERGED` events for consistent tracking (with `pr_url: "local://merged"`).

### runtimes

Defines CLI tools that agents use to write code. NXD spawns each in a tmux session.

```yaml
runtimes:
  gemma:                                       # Native runtime (built into NXD)
    native: true                               # No external CLI dependency
    models: ["gemma4"]                         # Auto-selected for Gemma 4 models
    max_iterations: 10                         # Max edit-test cycles per story
    command_allowlist:                          # Shell commands the native runtime may execute
      - "go build ./..."
      - "go test ./..."
      - "npm test"
      - "npm run build"
  aider:                                       # External runtime
    command: aider                             # CLI executable
    args: ["--model", "ollama_chat/gemma4:26b", "--no-auto-commits"]
    models: ["gemma4", "deepseek-coder-v2", "qwen2.5-coder"]   # Models this runtime supports
    detection:
      idle_pattern: "^>"                       # Regex: agent is idle/ready
      permission_pattern: "\\[Y/n\\]"          # Regex: agent is asking for permission
```

**Native runtime (`gemma`):** Built into NXD, requires no external dependencies. Auto-selects for Gemma 4 models. Uses function calling for structured code edits. The `command_allowlist` restricts which shell commands the runtime can execute for safety. The `max_iterations` field limits edit-test cycles to prevent runaway loops.

**Detection patterns** are compiled as Go regexps and matched against the last 30 lines of tmux pane output. The Watchdog uses these to determine agent status.

**Adding a new runtime:**
Just add another block to `runtimes:` with the command, args, and detection patterns. No code changes needed.

## Example Configurations

### Gemma 4 Default (24GB+ RAM, recommended)

```yaml
models:
  tech_lead: { provider: ollama, model: gemma4:26b, max_tokens: 16000 }
  senior:    { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  junior:    { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  qa:        { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  supervisor: { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
update_check: true                # Check for model updates on startup
update_interval_hours: 168        # Check weekly (168 hours)
```

### Minimal (16GB RAM laptop)

```yaml
models:
  tech_lead: { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  senior:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  intermediate: { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  junior:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  qa:        { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  supervisor: { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
```

### Google AI + Ollama Fallback (free tier)

```yaml
models:
  tech_lead: { provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b-a4b-it, max_tokens: 16000 }
  senior:    { provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b-a4b-it, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  junior:    { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  qa:        { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  supervisor: { provider: google+ollama, model: gemma4:26b, google_model: gemma-4-26b-a4b-it, max_tokens: 4000 }
  fallback_cooldown_s: 60
```

### Hybrid (Offline workers, Cloud planning)

```yaml
models:
  tech_lead: { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 16000 }
  senior:    { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  junior:    { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  qa:        { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  supervisor: { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 4000 }
merge:
  mode: github
```

### Full Cloud

```yaml
models:
  tech_lead: { provider: anthropic, model: claude-opus-4-20250514, max_tokens: 16000 }
  senior:    { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 8000 }
  intermediate: { provider: anthropic, model: claude-haiku-4-5-20251001, max_tokens: 4000 }
  junior:    { provider: openai, model: gpt-4o-mini, max_tokens: 4000 }
  qa:        { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 8000 }
  supervisor: { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 4000 }
merge:
  mode: github
runtimes:
  claude-code:
    command: claude
    args: ["--dangerously-skip-permissions"]
    models: ["opus-4", "sonnet-4", "haiku-4"]
    detection:
      idle_pattern: "^\\$\\s*$"
      permission_pattern: "\\[Y/n\\]"
      plan_mode_pattern: "Plan mode"
```

## Validating Your Config

```bash
# Check for errors
nxd config validate

# View the active config
nxd config show
```
