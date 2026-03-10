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
    provider: ollama                     # ollama, anthropic, or openai
    model: deepseek-coder-v2:latest      # Model name (Ollama tag or API model ID)
    max_tokens: 16000                    # Max output tokens
  senior:
    provider: ollama
    model: qwen2.5-coder:32b
    max_tokens: 8000
  intermediate:
    provider: ollama
    model: qwen2.5-coder:14b
    max_tokens: 4000
  junior:
    provider: ollama
    model: qwen2.5-coder:7b
    max_tokens: 4000
  qa:
    provider: ollama
    model: qwen2.5-coder:14b
    max_tokens: 8000
  supervisor:
    provider: ollama
    model: deepseek-coder-v2:latest
    max_tokens: 4000
```

**Providers:**

| Provider | Endpoint | Auth | Offline? |
|----------|----------|------|----------|
| `ollama` | `http://localhost:11434` | None | Yes |
| `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` | No |
| `openai` | `https://api.openai.com` | `OPENAI_API_KEY` | No |

You can mix providers — for example, use Ollama for juniors and Anthropic for the Tech Lead.

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

Defines external CLI tools that agents use to write code. NXD spawns each in a tmux session.

```yaml
runtimes:
  aider:                                    # Runtime name (referenced internally)
    command: aider                          # CLI executable
    args: ["--model", "ollama_chat/deepseek-coder-v2:latest", "--no-auto-commits"]
    models: ["deepseek-coder-v2", "qwen2.5-coder"]   # Models this runtime supports
    detection:
      idle_pattern: "^>"                    # Regex: agent is idle/ready
      permission_pattern: "\\[Y/n\\]"       # Regex: agent is asking for permission
```

**Detection patterns** are compiled as Go regexps and matched against the last 30 lines of tmux pane output. The Watchdog uses these to determine agent status.

**Adding a new runtime:**
Just add another block to `runtimes:` with the command, args, and detection patterns. No code changes needed.

## Example Configurations

### Minimal (8GB RAM laptop)

```yaml
models:
  tech_lead: { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  senior:    { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  intermediate: { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  junior:    { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  qa:        { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  supervisor: { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
```

### Hybrid (Offline workers, Cloud planning)

```yaml
models:
  tech_lead: { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 16000 }
  senior:    { provider: anthropic, model: claude-sonnet-4-20250514, max_tokens: 8000 }
  intermediate: { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 4000 }
  junior:    { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  qa:        { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
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
