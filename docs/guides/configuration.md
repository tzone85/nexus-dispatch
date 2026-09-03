# NXD Configuration Guide

NXD is configured via `nxd.yaml` in your project directory. Run `nxd init` to generate a default config, or copy `nxd.config.example.yaml`.

## Configuration File Location

NXD looks for config in this order:
1. `--config <path>` flag (any command)
2. `nxd.yaml` in the current directory
3. `~/.nxd/config.yaml` (only when `--config` is omitted)

> [!IMPORTANT]
> When `--config` is passed explicitly, NXD fails loudly if the file is missing or unparseable. The home-directory fallback only kicks in when `--config` is omitted entirely — this prevents `nxd --config /etc/nxd/prod.yaml ...` from quietly loading the wrong config.

## Full Reference

### schema version

```yaml
version: "1.0"          # required pin — silences the "no version field" hint
```

NXD validates this against `CurrentSchemaVersion` at startup:

- **Equal** → silent.
- **Empty / unset** → logs a one-line hint suggesting you pin it.
- **Older minor/patch** → advisory log, runs in compat mode.
- **Older major** → warning log, runs in compat mode.
- **Newer major** → error, refuses to start (your YAML expects features this binary doesn't have — upgrade NXD or downgrade the YAML).

### workspace

```yaml
workspace:
  state_dir: ~/.nxd-myproject  # Where NXD stores events, DB, logs (one PER project)
  backend: sqlite              # "sqlite" (offline) or "dolt" (version-controlled)
  log_level: info              # debug, info, warn, error
  log_format: text             # "text" (default) or "json" (structured/slog)
  log_retention_days: 30       # How long to keep session logs
  max_event_bytes: 1048576     # Cap on one events.jsonl line (default 1 MiB)
  fsync_events: true           # fsync after every appended event (default true)
```

**state_dir** semantics:
- `~` is expanded and the path is made **absolute at load time**, so every consumer (CLI, engine, reports) sees the same directory.
- A **relative** value (e.g. `.nxd`) is resolved against the directory containing the config file — not the working directory. This is what `nxd init --local-state` writes, giving each repo its own `./.nxd`.
- The global `--state-dir <path>` flag overrides the config value for one invocation (relative paths resolve against the working directory).
- All NXD state lives here: events.jsonl, nxd.db, nxd.lock, logs/, worktrees/, improvements.json.

> [!WARNING]
> **Use a separate `state_dir` per project.** Two projects sharing `~/.nxd` will fight over `nxd.lock` and corrupt each other's events. Use `nxd init --local-state` (repo-local `./.nxd`, git-ignored) or the convention `~/.nxd-<projectname>`.

**max_event_bytes** caps the encoded size of a single event line. QA payloads can embed whole test logs; a line above the cap has its longest string values truncated (suffix `…[truncated N bytes]`, payload key `truncated: true`) until it fits. The event itself is never dropped. Readers accept lines up to 16 MiB regardless. `0` means the default.

**fsync_events** flushes each append to stable storage before the command continues. The event log is the source of truth — a lost tail after a crash desyncs every derived view — so leave this on; set `false` only for throwaway benchmarks. A torn final line left by a crash is skipped by readers and quarantined on the next append (see `nxd state check`).

**log_format** = `"json"` switches the stdlib + slog output to one JSON object per line — useful when piping into a log aggregator. Override at runtime via `NXD_LOG_FORMAT=json` env var. Same goes for `log_level` via `NXD_LOG_LEVEL=debug`.

### models

```yaml
models:
  ollama_host: 10.0.0.5:11434   # optional; Ollama endpoint for `nxd doctor` / health checks
```

**ollama_host** overrides the Ollama endpoint probed by `nxd doctor` and `nxd init`. `host:port` without a scheme is accepted (`http://` is prepended). The `OLLAMA_HOST` environment variable takes precedence when set; unset both and `localhost:11434` is used.

Maps each agent role to a specific LLM provider and model.

```yaml
models:
  tech_lead:
    provider: ollama                     # ollama, google+ollama, google, anthropic, or openai
    model: gemma4:26b                    # Model name (Ollama tag or API model ID)
    google_model: gemma-4-26b-a4b-it     # Google AI model ID (used by google+ollama and google providers)
    max_tokens: 16000                    # Max output tokens
    num_ctx: 32768                       # Ollama context window (options.num_ctx); omit for the model default
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

The `google_model` field specifies the model name for Google AI API calls (e.g., `gemma-4-26b-a4b-it`). This is separate from the `model` field, which is the Ollama tag. When `google_model` is set it is what the Google client sends; the `model` (Ollama tag) is only used by the Ollama fallback.

`num_ctx` sets the Ollama context window for that role (`options.num_ctx`); `temperature` requested by a pipeline stage is also forwarded to Ollama. Both are ignored by cloud providers.

**Provider behaviour notes:** every provider returns a structured API error for non-2xx responses (status code, provider, redacted body snippet, `Retry-After`) so the pipeline can distinguish fatal auth/billing failures (401/403/402) from transient rate limits (429) and overload (5xx/529). `anthropic` and `openai` requests time out after 120 s by default; Ollama retries transient 5xx with context-aware back-off. Native tool calling is supported on `anthropic` (`tool_use`/`tool_result` blocks), `openai` (`tools`/`tool_calls`), `google` (`functionCall`/`functionResponse`) and tool-capable Ollama models.

> **Authentication note:** These API keys are used for NXD's **internal operations** only -- planning, code review, and QA. They are **not** passed to spawned coding agents. If you use Claude Code as a runtime, it authenticates via its own OAuth session (your Max/Pro subscription via `claude login`), so spawned agents incur no additional API cost. The API key is only consumed by the lightweight internal LLM calls (a few per story per stage).

You can mix providers -- for example, use Ollama for juniors and Google AI for the Tech Lead.

> [!IMPORTANT]
> **Use different model families for `senior` and `junior`.** When the same model writes and reviews code, the reviewer shares the coder's hallucinations and confidence patterns. NXD logs a `WARNING` at config-load time when `models.senior.model == models.junior.model` (or `== intermediate.model`).
>
> Recommended split: `qwen3-coder:30b` for `senior`/`tech_lead`/`qa` (32GB+ machines), `gemma4:e4b` for `junior`/`intermediate`/`supervisor`. Budget alternative on 24GB: `qwen2.5-coder:14b` + `gemma4:e4b`. See [Model Selection](model-selection.md) for the full rationale + GPU-swap trade-off.

### memory (MemPalace)

NXD's offline-first semantic memory layer. Used by the planner + reviewer to recall prior diffs, QA failures, and review feedback when handling related work.

```yaml
memory:
  enabled: true                                 # default false; flip on after `pip install -r requirements.txt`
  palace_path: ~/.mempalace                     # optional override; defaults to $HOME/.mempalace
```

> [!IMPORTANT]
> **Offline-first guarantee.** MemPalace ships with the ChromaDB local backend pinned at `mempalace==2.0.0` — zero API calls, all embeddings computed locally. Enabling it does NOT introduce any network traffic. The Python bridge lives at `scripts/mempalace_bridge.py` and is wrapped by `internal/memory/mempalace.go`.

Run `make mempalace-check` to verify the bridge end-to-end before the first real session. CI runs the same check, and `internal/memory/bridge_args_test.go` pins the argv contract so a future bridge refactor cannot silently desync Go and Python.

### improver (`nxd improve`)

The self-improvement module reads `metrics.jsonl` + the local state directory and emits a JSON file (`<state_dir>/improvements.json`) that the dashboard surfaces as popups.

It has no `nxd.yaml` section — it's a CLI on top of analyzers. Flags:

```bash
nxd improve                                    # offline only — scans metrics + state
nxd improve --feed https://example.com/tips.json   # optional online tips feed (HTTPS, JSON array of Suggestion)
nxd improve --json                             # machine-readable output for tooling
```

The default offline-only run never makes network calls. `--feed` is opt-in. Suggestions are persisted to `<state_dir>/improvements.json` so the dashboard can render them across sessions without re-running the analyzers on each WebSocket tick.

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

### controller

The **active controller** is an opt-in supervisor loop that detects stuck stories and corrects them (cancel / restart / escalate tier). Disabled by default — turn on for unattended long runs.

```yaml
controller:
  enabled: false            # off by default; flip on for unattended runs
  interval_s: 60            # how often the controller tick fires
  max_stuck_duration_s: 300 # consider a story "stuck" after 5 minutes of no progress
  auto_restart: true        # reset stuck stories to draft
  auto_reprioritize: false  # escalate tier + reset (takes priority over restart)
  auto_cancel: false        # cancel only, no reset
  max_actions_per_tick: 1   # rate-limit corrective actions
  cooldown_s: 120           # min seconds between actions on the same story
```

Each tick emits `CONTROLLER_ANALYSIS`, `CONTROLLER_ACTION`, and (when triggered) `CONTROLLER_STUCK_DETECTED` events. The web dashboard shows these on the activity timeline.

### routing — Bayesian role assignment

Beyond the Fibonacci complexity router above, NXD also keeps **Beta-distribution priors** per (role, complexity) cell and uses them to nudge tier assignment when historical success rates skew. The priors are persisted to `<state_dir>/bayesian_priors.json` and updated after every story outcome.

There's no YAML knob for this — it's always on, decays slowly (`ApplyDecay()` per resume tick), and starts from a uniform prior. To reset: delete `bayesian_priors.json`. To inspect: read the JSON file directly — it's a simple map of `(role, complexity) → {alpha, beta}` pairs.

### scratchboard

Each requirement has a cross-agent JSONL scratchboard at `<state_dir>/scratchboards/<req-id>.jsonl`. Agents can `read_scratchboard()` / `write_scratchboard()` via tool calls — useful for sharing intermediate findings (test failures, gotchas, design decisions) across waves of the same requirement.

No YAML config; on by default. Wipe a requirement's scratchboard by deleting its file.

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

Controls background model update checks. **Disabled by default** — NXD's offline-first promise is that a default run makes zero outbound calls, so the periodic registry check is strictly opt-in.

```yaml
update_check: false               # Default. Set true to opt in to background checks
update_interval_hours: 48         # Hours between checks once opted in (<= 0 also disables)
```

When you opt in, NXD periodically compares your configured models against the Ollama registry (and Google AI, if an API key is set) and prints a notice when newer versions exist. `NXD_UPDATE_CHECK=false` in the environment force-disables it regardless of config, and the user-initiated `nxd models check` always works either way.

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

### qa — success criteria and gates

```yaml
qa:
  success_criteria:                     # evaluated before an agent may declare a story done
    - kind: command_succeeds
      value: go build ./...
    - kind: test_passes
      value: go test ./...
  criteria_authoritative: false         # true = passing criteria outrank the LLM reviewer's veto
  disable_completion_gate: false        # true = skip the composed-mainline verification before REQ_COMPLETED
  completion_fix_cycles: 2              # auto-fix cycles against a red mainline before REQ_BLOCKED
  pause_on_integration_failure: true    # pause the requirement when the post-merge build of the base branch fails
```

`pause_on_integration_failure` (default `true`) controls what happens when a story merges cleanly but the base branch no longer builds (`STORY_INTEGRATION_FAILED`). By default the requirement is paused — with the Tech Lead's fix suggestion recorded on the event — so the next wave is not branched from a red mainline; fix the base branch and `nxd resume`. Set it to `false` to only record the failure and keep dispatching.

### review

```yaml
review:
  max_diff_bytes: 204800   # cap on the diff sent to the LLM reviewer (default 200 KB)
```

Diffs longer than `max_diff_bytes` are cut and end with an explicit `[diff truncated: N more bytes]` marker that the reviewer prompt tells the model about. The review gate fails closed: a reply with no tool call and no parseable JSON verdict is recorded as a failed review with feedback `reviewer returned no structured verdict` — prose is never interpreted as a pass.

### billing — LLM budget guard

```yaml
billing:
  llm_costs:
    mode: per_token          # budget enforcement needs metered costs
    rates:
      claude-sonnet:
        input_per_1k: 0.003
        output_per_1k: 0.015
  budget_usd: 25             # hard cap on actual LLM spend per requirement (0 = off)
  budget_warn_pct: 80        # emit REQ_BUDGET_WARNING at this % of the cap (default 80)
```

Rates are resolved per model deterministically: an exact key match first, then the **longest** key that is a prefix of the model name (`claude-sonnet` prices `claude-sonnet-4-20250514`), then a `default` key when you define one. A model that matches none of these is **unpriced**: its spend is not counted, and the budget guard and `nxd report` log an "unpriced model" warning so you can add a rate.

The guard prices the requirement's **actual** token usage (`metrics.jsonl`) with your configured rates before each story's post-execution pipeline. Crossing the warning threshold emits `REQ_BUDGET_WARNING` once; reaching the cap emits `REQ_BUDGET_EXCEEDED` and pauses the requirement so no further tokens burn. Raise the budget (or accept the spend) and `nxd resume` to continue. In `mode: subscription` spend is always $0 and the guard never trips — it exists for metered API keys, not local Ollama.

### notifications

```yaml
notifications:
  enabled: true
  webhook_url: https://hooks.slack.com/services/T000/B000/XXXX
  format: slack              # "json" (default, structured envelope) or "slack" ({"text": ...})
  desktop: true              # native macOS notification (no-op on other platforms)
  timeout_s: 5               # per-delivery bound (default 5s)
  # events: [REQ_COMPLETED, REQ_BLOCKED]   # optional override of the default set
```

NXD runs unattended for long stretches; notifications tell you the moment a run finishes or needs you. By default it fires on `REQ_COMPLETED`, `REQ_BLOCKED`, `REQ_PAUSED`, `HUMAN_REVIEW_NEEDED`, `STORY_SECURITY_FAILED`, `REQ_BUDGET_WARNING`, and `REQ_BUDGET_EXCEEDED`. Delivery is asynchronous and best-effort: a slow or failing endpoint is logged and dropped, never blocking the pipeline. The `slack` format also works with Discord's `/slack` webhook endpoint.

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

**Native runtime (`gemma`):** Built into NXD, requires no external dependencies. Auto-selects for Gemma 4 models. Uses function calling for structured code edits. The `command_allowlist` restricts which commands the runtime can execute; matching is **argv-aware** (the entry's tokens must equal the command's leading tokens — `go test` matches `go test ./...` but not `go testevil`), shell metacharacters are rejected, and so are exec-style flags (`-exec`, `-toolexec`, `find -execdir`), env-var prefixes (`FOO=x cmd`), and any absolute / `~` / `..` path that leaves the worktree. An empty allowlist denies everything. Commands run inside the [sandbox](#sandbox). The `max_iterations` field limits edit-test cycles to prevent runaway loops.

**Runner (`runner: tmux|docker|ssh`):** CLI runtimes run in a tmux session on this host by default. `runner: docker` (with `docker.image`, `docker.network: none|bridge`, allowlisted `docker.extra_flags` such as `--cpus`/`--memory`) or `runner: ssh` (`ssh.host`, `ssh.key_file`, `ssh.remote_dir`) confines the agent instead. The unattended-mode flags (`claude --dangerously-skip-permissions`, `codex --full-auto`) are **no longer in the default args**: NXD appends them automatically only when the runner is docker/ssh; on the host the agent keeps its permission prompts and `nxd doctor` warns "agents run unsandboxed on this host". Secrets (API keys, `env_vars`) never appear in a command line or `ps`: they are written to a 0600 `.nxd-prompts/env.sh` in the worktree that the session sources and deletes.

### sandbox

Where **native tool commands** run: the gemma runtime's `run_command`, the criteria evaluator's `command_succeeds` / `test_passes`, and the investigator's `run_command`.

```yaml
sandbox:
  mode: auto                    # auto | docker | host
  image: golang:1.26-alpine     # container image for the docker sandbox
  network: none                 # none | bridge
  cpus: "2"                     # docker --cpus
  memory: 2g                    # docker --memory
  extra_mounts: []              # "<worktree-relative-src>:<abs container path>[:ro]"
  # auto_approve_prompts: false # watchdog auto-answers CLI permission prompts (default: only when sandboxed)
```

| Key | Default | Description |
|-----|---------|-------------|
| `mode` | `auto` | `docker`: every command runs as `docker run --rm --network <network> -v <worktree>:/work -w /work --cpus … --memory … --cap-drop ALL --security-opt no-new-privileges <image> <argv>` (argv passed directly — no shell on the host or in the container). `host`: run on this machine (argv exec, no shell). `auto`: docker if `docker info` succeeds (probed once per process), otherwise host **with a loud one-time warning** naming the risk and this override. `docker` fails hard when the daemon is unreachable. |
| `image` | `golang:1.26-alpine` | **Must contain the toolchain your allowlist needs.** The default covers Go only — a Node/Python/Make project needs an image with those tools (build your own or pick e.g. `node:22-alpine`). |
| `network` | `none` | Container network. `none` blocks all egress (module downloads must already be vendored/cached inside the worktree); `bridge` allows outbound access. |
| `cpus`, `memory` | `2`, `2g` | Resource limits passed to `docker run`. |
| `extra_mounts` | `[]` | Additional bind mounts. Sources are validated to be **worktree-relative** (no absolute paths, `~` or `..`); destinations must be absolute container paths; the only option is `ro`. |
| `auto_approve_prompts` | unset | Whether the watchdog answers `Y` to a CLI agent's permission prompt. Unset ⇒ `true` for runtimes whose `runner` is docker/ssh, `false` on the host. |

`coverage_above` criteria still run on the host (they need a coverage profile in a temp directory outside the worktree). `nxd doctor` reports the effective mode.

### approvals

The human approval queue. Instead of auto-resolving risky decisions the pipeline records an approval item, pauses the requirement and blocks the story's merge until you decide (`nxd approvals list|approve|reject`, or the dashboard **Approvals** panel), then `nxd resume <req-id>`.

```yaml
approvals:
  require_for:                  # which decisions need a human
    - conflict_resolution       # LLM-resolved or escalated rebase conflicts
    - integration_failure       # post-merge build failed on the mainline
    - security_finding          # security gate finding at/above gate_severity
    # - merge                   # opt-in: every merge waits for approval
  timeout_action: pause         # what happens while an item is pending (only "pause" today)
```

| Key | Default | Description |
|-----|---------|-------------|
| `require_for` | all three shown | Kinds that create an approval item. Remove a kind to let the pipeline auto-proceed for it. `merge` gates every merge on an explicit OK. |
| `timeout_action` | `pause` | Pending items pause the requirement (`REQ_PAUSED`) rather than expiring; there is no auto-approve. |

Items are persisted as `APPROVAL_REQUESTED` / `APPROVAL_RESOLVED` events (see the event reference).

### investigation

```yaml
investigation:
  command_allowlist: ["ls", "find", "wc", "grep", "cat", "head", "tail", "git log", "git status", "git diff", "go build", "go test", "make"]
```

Commands the investigator (`nxd req` / `nxd plan` on an existing repo) may run. Same argv-aware matcher and sandbox as the native runtime: `cat /etc/passwd`, `cat ~/.aws/credentials`, `find / -name '*.pem'` and `find … -exec` are denied even with `cat`/`find` listed, and an **empty list denies everything**. `read_file` refuses symlinks that resolve outside the repository.

**Detection patterns** are compiled as Go regexps and matched against the last 30 lines of tmux pane output. The Watchdog uses these to determine agent status.

**Adding a new runtime:**
Just add another block to `runtimes:` with the command, args, and detection patterns. No code changes needed.

## Example Configurations

### Recommended (32GB+ RAM, offline, two-model split)

The default for 32GB+ machines. `qwen3-coder:30b` reviews (262K context, SWE-bench 51.6%), `gemma4:e4b` writes — different model families, different blind spots.

```yaml
version: "1.0"
models:
  tech_lead:    { provider: ollama, model: qwen3-coder:30b, max_tokens: 16000 }
  senior:       { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  qa:           { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  supervisor:   { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
update_check: false               # Background update checks are opt-in (offline-first)
update_interval_hours: 48         # Used only once update_check is true
```

> [!NOTE]
> On a single-GPU machine, swapping between qwen3-coder + gemma4 adds ~3-5s per role switch. The blind-spot coverage is worth it. On 24GB machines, use `qwen2.5-coder:14b` instead (see budget config below). For raw throughput on simple projects, see [Minimal](#minimal-16gb-ram-laptop-or-single-model).

### Budget (24GB RAM, offline, two-model split)

Same two-model principle, smaller reviewer that fits within 24GB (qwen3-coder:30b + gemma4:e4b = ~25GB, over the limit).

```yaml
version: "1.0"
models:
  tech_lead:    { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 16000 }
  senior:       { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  qa:           { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  supervisor:   { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
```

### Minimal (16GB RAM laptop, or single-model)

Single-family setup for low-RAM laptops or quick experiments. NXD will log a `WARNING` at startup about reviewer/coder overlap — this is expected for this config.

```yaml
models:
  tech_lead:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  senior:       { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  intermediate: { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  qa:           { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  supervisor:   { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
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
    args: ["--dangerously-skip-permissions"]   # only safe with runner: docker|ssh — on the host NXD leaves permission prompts on
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

## Sections not covered above

The blocks below complete the reference: every `nxd.yaml` key NXD reads is either documented in a section above, in one of these, or in the [complete key index](#complete-key-index) at the end. `internal/config/docs_coverage_test.go` fails the build when a key is added to `config.Config` without landing here.

### planning

Controls how the Tech Lead decomposes a requirement into stories.

```yaml
planning:
  sequential_file_patterns: ["package.json", "*.config.*", "src/core/*"]  # files that force sequential (not parallel) waves
  max_story_complexity: 5           # planner rejects stories scored above this; also caps split children
  godmode: false                    # config default for the --godmode flag on req/resume/plan
  emit_integration_story: true      # append a final integration story wiring all components together
  emit_scribe_story: true           # append a final documentation story owning README.md + docs/
```

| Key | Default | Description |
|-----|---------|-------------|
| `planning.sequential_file_patterns` | `package.json`, `*.config.*`, `src/core/*` | Glob patterns (matched against the full path and the base name) of files that two stories must not edit in the same wave; the dispatcher serialises stories that own a matching file. |
| `planning.max_story_complexity` | `5` | The planner fails a plan whose story exceeds this Fibonacci score, and `ValidateSplit` uses it to bound the children of a manager-initiated story split. |
| `planning.godmode` | `false` | Default value for `--godmode` on `nxd req`, `nxd resume` and `nxd plan` (the flag wins when passed). It is threaded to the LLM client builder, which currently runs every client non-interactively — the key changes nothing else today. |
| `planning.emit_integration_story` | `true` | Every persisted plan gets a final story that depends on every code story, wires the pieces into the real entry point and adds an end-to-end smoke test. Ephemeral estimates skip it. |
| `planning.emit_scribe_story` | `true` | Every persisted plan gets a final documentation story owning `README.md` + `docs/` (pre-existing READMEs are only edited inside `<!-- nxd:scribe:start/end -->` markers). |

### methodology

Design/testing defaults injected into the planner prompt. A requirement can opt out with `methodology: relaxed` (also `none`, `off`, `tdd-only`, `ddd-only`) in its text when `allow_override` is true.

```yaml
methodology:
  ddd: true                 # Domain-Driven Design guidance in the planner prompt
  tdd: true                 # tests-first guidance + "every code story owns a test file" check
  min_coverage_pct: 80      # reserved — not yet implemented
  allow_override: true      # honour the per-requirement "methodology: …" directive
```

| Key | Default | Description |
|-----|---------|-------------|
| `methodology.ddd` | `true` | Adds the Domain-Driven Design section (domain layer separation, ubiquitous language, bounded contexts) to the planner's mandatory-methodology directive. |
| `methodology.tdd` | `true` | Adds the tests-first section and makes the planner flag stories that own source files without a paired test file. |
| `methodology.min_coverage_pct` | `80` | **Reserved — not yet implemented.** Intended as the merge coverage gate when TDD is on; nothing reads it today (use a `coverage_above` success criterion for an enforced floor). |
| `methodology.allow_override` | `true` | When false the `methodology: …` directive in requirement text is ignored and the config values always apply. |

### devdb

Ephemeral per-story Postgres databases (Docker provider only; NXD is offline-first and does not support the cloud `ghost` provider). Empty or `null` disables the feature.

```yaml
devdb:
  provider: docker                  # "docker" | "null" (default: unset = disabled)
  template: myapp_template          # source database cloned per story (required for docker)
  on_failure:
    keep_db: false                  # keep the story DB when the story fails
    retain_hours: 24                # how long a kept DB survives before gc
  docker:
    image: postgres:16
    container_name: nxd-devdb-pg16
    template_volume: ~/.nxd/devdb-data
    network: nxd-devdb
    host_port_range: 5500-5599
    host: localhost                 # override for Colima / VM setups, e.g. 192.168.64.3
```

| Key | Default | Description |
|-----|---------|-------------|
| `devdb.provider` | unset | `docker` provisions a database per story from the template; `null`/unset disables devdb. `ghost` is rejected. |
| `devdb.template` | — | Name of the template database forked for each story. Required with `provider: docker`; must match `^[a-z][a-z0-9-]{0,62}$` because it is interpolated into `CREATE DATABASE … WITH TEMPLATE`. |
| `devdb.on_failure.keep_db` | `false` | Keep a failed story's database for inspection instead of dropping it on release. |
| `devdb.on_failure.retain_hours` | `24` | Retention for kept databases; `nxd db gc` and resume-time orphan recovery drop older ones. |
| `devdb.docker.image` | `postgres:16` | Postgres image for the shared devdb container. |
| `devdb.docker.container_name` | `nxd-devdb-pg16` | Name of that container. |
| `devdb.docker.template_volume` | `~/.nxd/devdb-data` | Host directory mounted as the Postgres data volume. |
| `devdb.docker.network` | `nxd-devdb` | Docker network the container joins. |
| `devdb.docker.host_port_range` | `5500-5599` | Host port range the container's 5432 is published on (bound to 127.0.0.1). |
| `devdb.docker.host` | `localhost` | Host agents use in the connection string; override when Docker runs in a VM. |

Story databases are injected into the worktree as `.nxd-db/connect.env` and tracked by the `story_databases` projection (`nxd db list`, dashboard **Databases** panel).

### security

The security agent: the per-story pre-merge gate, `nxd security scan`, and the self-upskilling knowledge base.

```yaml
security:
  disable_gate: false       # true turns the per-story pre-merge gate off (scan still works)
  gate_severity: critical   # critical | high | medium | low — pause threshold
  auto_learn: true          # grow the knowledge base from confirmed high+ findings
  gate_scope: changed       # changed | repo — what the per-story gate blocks on
  llm_findings_block: false # true lets an LLM-only finding block a story
  kb_path: ""               # default <state_dir>/security/knowledge.json
```

| Key | Default | Description |
|-----|---------|-------------|
| `security.disable_gate` | `false` | Skip the per-story security review (deterministic scanners + LLM threat model) that runs after QA and before merge. |
| `security.gate_severity` | `critical` | A finding at or above this severity pauses the requirement (`STORY_SECURITY_FAILED`, then an approval item when `approvals.require_for` lists `security_finding`). |
| `security.auto_learn` | `true` | Confirmed high+ findings of a new vulnerability class are added to the knowledge base (`SECURITY_RULE_LEARNED`). |
| `security.gate_scope` | `changed` | `changed` only counts findings in files the story modified; `repo` blocks on any finding in the worktree (whole-repo auditing is `nxd security scan`'s job). |
| `security.llm_findings_block` | `false` | LLM threat-model findings are advisory unless a scanner corroborates them on the same file; `true` restores strict LLM blocking. |
| `security.kb_path` | `<state_dir>/security/knowledge.json` | Where the knowledge base persists. |

### plugins

Plugin manifests discovered under `<state_dir>/plugins/` (see `nxd doctor`). The four sections are lists/maps merged from every installed plugin — you rarely write them by hand in `nxd.yaml`.

```yaml
plugins:
  playbooks: []      # {name, file, inject_when, roles} — role playbooks injected into agent prompts
  prompts: {}        # <prompt-name>: <file> — overrides for built-in prompt templates
  qa: []             # {name, file, after} — extra QA checks run after a built-in stage
  providers: {}      # <name>: {command, models} — external model providers
```

### Reserved keys — accepted, validated, not yet wired

These keys are part of the schema (and validated where noted) but **nothing outside `internal/config` reads them today**. They are kept so existing configs stay valid; do not expect behaviour from them until a CHANGELOG entry says otherwise.

| Key | Default | Status |
|-----|---------|--------|
| `workspace.backend` | `sqlite` | Validated to be `sqlite` or `dolt`; only SQLite projections exist. `dolt` is **reserved — not yet implemented**. |
| `workspace.log_retention_days` | `30` | **Reserved — not yet implemented.** No log pruning runs; `nxd gc` handles branches/worktrees only. |
| `cleanup.log_archive` | `file` | Validated (`file`/`dolt`/`none`) but **reserved — not yet implemented**. |
| `routing.max_qa_failures_before_escalation` | `3` | **Reserved — not yet implemented.** QA failures escalate through `max_retries_before_escalation` today. |
| `monitor.context_freshness_tokens` | `150000` | **Reserved — not yet implemented.** No context-refresh trigger reads it. |
| `merge.pr_template` | story/description/criteria template | **Reserved — not yet implemented.** PR bodies are rendered by the merger's built-in format. |
| `methodology.min_coverage_pct` | `80` | **Reserved — not yet implemented** (see [methodology](#methodology)). |
| `memory.enabled` | `true` | **Reserved — not yet implemented.** MemPalace is enabled by whether the Python bridge is installed (`nxd doctor` reports it); this flag is not consulted. |
| `memory.palace_path` | unset | **Reserved — not yet implemented.** The bridge uses MemPalace's own default (`$HOME/.mempalace`). |

`nxd.config.example.yaml` is generated byte-for-byte from `config.DefaultYAML()` (a test enforces this), so it cannot carry comments — this table is the authoritative note.

## Complete key index

Every leaf key, with its default. `*` in `models.*` stands for any role block: `tech_lead`, `senior`, `intermediate`, `junior`, `qa`, `supervisor`, `manager`, `investigator`. All eight are validated and listed by `nxd models`, but today the pipeline builds its clients from three of them: `tech_lead` (planning, classification, manager diagnosis and re-planning), `senior` (the shared post-execution client: review, QA, conflict resolution, security and completion gates) and `investigator` (`nxd req` / `nxd plan` on an existing repository); `junior.provider` selects the coding runtime's provider. The remaining role blocks are accepted for forward compatibility and do not yet pick a distinct model.

| Key | Default | Meaning |
|-----|---------|---------|
| `version` | `"1.0"` | Schema version pin (see [schema version](#schema-version)). |
| `workspace.state_dir` | `~/.nxd` | State directory (events, projection DB, logs, worktrees). |
| `workspace.backend` | `sqlite` | Projection backend; `dolt` reserved. |
| `workspace.log_level` | `info` | slog level filter. |
| `workspace.log_format` | `text` | `text` or `json`. |
| `workspace.log_retention_days` | `30` | Reserved. |
| `workspace.update_check` | `false` | Opt-in background model update check (see [updates](#updates); the key lives under `workspace:`). |
| `workspace.update_interval_hours` | `48` | Hours between update checks once opted in. |
| `workspace.max_event_bytes` | `1048576` | Cap on one events.jsonl line. |
| `workspace.fsync_events` | `true` | fsync every appended event. |
| `models.ollama_host` | unset | Ollama endpoint for doctor/health checks. |
| `models.*.provider` | `ollama` | `ollama`, `google+ollama`, `google`, `anthropic`, `openai`. |
| `models.*.model` | `gemma4:e4b` | Model name / Ollama tag. |
| `models.*.max_tokens` | 16000 (tech_lead, investigator), 8000 (senior, qa, manager), 4000 (others) | Max output tokens. |
| `models.*.google_model` | unset | Google AI model ID (required for `google*` providers). |
| `models.*.fallback_cooldown_s` | `0` | **Reserved — not yet implemented.** The `google+ollama` fallback client currently uses a fixed 60 s cooldown; the key is accepted but not read. |
| `models.*.num_ctx` | `0` (model default) | Ollama `options.num_ctx` for the role. |
| `routing.junior_max_complexity` | `3` | Highest complexity routed to Junior. |
| `routing.intermediate_max_complexity` | `5` | Highest complexity routed to Intermediate. |
| `routing.max_retries_before_escalation` | `2` | Review/QA retries at a tier before escalating. |
| `routing.max_qa_failures_before_escalation` | `3` | Reserved. |
| `routing.max_senior_retries` | `2` | Retries granted at the Senior tier (tier 1 of the escalation machine) before the Manager diagnoses. |
| `routing.max_manager_attempts` | `2` | Manager diagnosis attempts (tier 2) before the Tech Lead re-plans / human review. |
| `monitor.poll_interval_ms` | `10000` | Watchdog poll interval. |
| `monitor.stuck_threshold_s` | `120` | Seconds of unchanged output before `AGENT_STUCK`. |
| `monitor.context_freshness_tokens` | `150000` | Reserved. |
| `monitor.pipeline_timeout_s` | `900` | Bound on the per-story review → QA → merge pipeline. |
| `cleanup.worktree_prune` | `immediate` | `immediate` or `deferred` worktree removal. |
| `cleanup.branch_retention_days` | `7` | Age after which `nxd gc` deletes merged branches. |
| `cleanup.log_archive` | `file` | Reserved. |
| `cleanup.delete_dangling_branches` | `true` | On requirement completion delete local+remote branches of non-merged stories (auto-closes their PRs). |
| `merge.auto_merge` | `true` | Merge automatically after review + QA (+ security) pass. |
| `merge.review_before_merge` | `false` | Stop the pipeline at `STORY_MERGE_READY` (story status `merge_ready`) and wait for a human `nxd review` / `nxd merge <story-id>` instead of merging automatically. |
| `merge.base_branch` | `""` (detect) | Integration branch; empty detects the repo default (`main`/`master`). |
| `merge.mode` | `local` | `local` git merge or `github` push + PR. |
| `merge.pr_template` | built-in | Reserved. |
| `planning.sequential_file_patterns` | see [planning](#planning) | Files that serialise waves. |
| `planning.max_story_complexity` | `5` | Planner/split complexity cap. |
| `planning.godmode` | `false` | Default for `--godmode`. |
| `planning.emit_integration_story` | `true` | Append the integration story. |
| `planning.emit_scribe_story` | `true` | Append the documentation story. |
| `billing.default_rate` | `150` | Hourly rate used by `nxd estimate` / `nxd report` for the human-equivalent quote. |
| `billing.currency` | `USD` | Currency label on estimates and reports. |
| `billing.hours_per_point` | `1:[0.5,1] 2:[1,2] 3:[2,3] 5:[3,5] 8:[5,8] 13:[8,13]` | Low/high hour range per Fibonacci point used to turn complexity into an effort estimate. |
| `billing.llm_costs.mode` | `subscription` | `subscription` ($0) or `per_token`. |
| `billing.llm_costs.rates` | `{}` | Per-model `input_per_1k` / `output_per_1k` prices (see [billing](#billing--llm-budget-guard)). |
| `billing.llm_costs.rates.*.input_per_1k` | — | USD per 1K input tokens. |
| `billing.llm_costs.rates.*.output_per_1k` | — | USD per 1K output tokens. |
| `billing.budget_usd` | `0` (off) | Per-requirement spend cap. |
| `billing.budget_warn_pct` | `80` | Warning threshold as % of the cap. |
| `controller.enabled` | `false` | Active controller on/off. |
| `controller.interval_s` | `60` | Tick interval. |
| `controller.max_stuck_duration_s` | `300` | Stuck threshold. |
| `controller.auto_cancel` | `false` | Cancel stuck stories. |
| `controller.auto_restart` | `true` | Reset stuck stories to draft. |
| `controller.auto_reprioritize` | `false` | Escalate tier + reset. |
| `controller.max_actions_per_tick` | `1` | Corrective actions per tick. |
| `controller.cooldown_s` | `120` | Minimum seconds between actions on one story. |
| `memory.enabled` | `true` | Reserved. |
| `memory.palace_path` | unset | Reserved. |
| `investigation.command_allowlist` | see [investigation](#investigation) | Commands the investigator may run (empty denies all). |
| `qa.success_criteria` | `go build`, `go vet`, `go test` (Go projects) | Declarative criteria evaluated before a story may complete; `nxd init` tailors them to the detected project type. |
| `qa.success_criteria[].kind` | — | One of `output_contains`, `output_not_contains`, `file_exists`, `file_contains`, `file_not_empty`, `exit_code_zero`, `test_passes`, `coverage_above`, `command_succeeds`, `migration_succeeds`, `schema_changed`, `sql_query_returns`. |
| `qa.success_criteria[].value` | — | Command / expected text / threshold, depending on `kind`. |
| `qa.success_criteria[].path` | — | File path for the `file_*` kinds. |
| `qa.success_criteria[].message` | — | Human-readable failure message. |
| `qa.success_criteria[].command` | — | `migration_succeeds`: shell command to run. |
| `qa.success_criteria[].sql` | — | `sql_query_returns`: query to execute against the story DB. |
| `qa.success_criteria[].expected_rows` | unset | `sql_query_returns`: optional exact row count. |
| `qa.success_criteria[].schema_baseline` | — | `schema_changed`: path to the baseline schema file. |
| `qa.disable_completion_gate` | `false` | Skip the composed-mainline verification before `REQ_COMPLETED`. |
| `qa.completion_fix_cycles` | `2` | Auto-fix cycles before `REQ_BLOCKED` (negative = hard gate). |
| `qa.criteria_authoritative` | `false` | Passing criteria outrank the LLM reviewer's veto. |
| `qa.pause_on_integration_failure` | `true` | Pause the requirement when the post-merge base-branch build fails. |
| `review.max_diff_bytes` | `204800` | Cap on the diff sent to the reviewer. |
| `security.disable_gate` | `false` | See [security](#security). |
| `security.gate_severity` | `critical` | Pause threshold. |
| `security.auto_learn` | `true` | Knowledge-base learning. |
| `security.gate_scope` | `changed` | `changed` or `repo`. |
| `security.llm_findings_block` | `false` | LLM findings block on their own. |
| `security.kb_path` | `<state_dir>/security/knowledge.json` | Knowledge-base location. |
| `runtimes.*.command` | per runtime | CLI executable (`aider`, `claude`, `codex`). |
| `runtimes.*.args` | per runtime | Extra CLI arguments; unattended flags are appended automatically only for docker/ssh runners. |
| `runtimes.*.models` | per runtime | Model names that select this runtime. |
| `runtimes.*.detection.idle_pattern` | per runtime | Regex: agent is idle/ready. |
| `runtimes.*.detection.permission_pattern` | per runtime | Regex: agent asks for permission. |
| `runtimes.*.detection.plan_mode_pattern` | unset | Regex: agent entered plan mode (Escape is sent). |
| `runtimes.*.native` | `false` (`true` for `gemma`) | In-process tool-calling runtime, no external CLI. |
| `runtimes.*.max_iterations` | `20` (gemma) | Edit-test cycles per story for native runtimes (must be > 0). |
| `runtimes.*.max_criteria_retries` | `0` → 2 | Self-correction attempts after a criteria rejection before the story escalates. |
| `runtimes.*.command_allowlist` | per runtime | Argv-aware allowlist for the native `run_command` tool (required, non-empty, for native runtimes). |
| `runtimes.*.concurrency` | `0` → 1 | Parallel agents for this runtime (semaphore around the shared LLM client). |
| `runtimes.*.runner` | `tmux` | `tmux`, `docker` or `ssh` execution target for CLI runtimes. |
| `runtimes.*.docker.image` | — | Image for `runner: docker`. |
| `runtimes.*.docker.network` | `none` | `none` or `bridge` (host is never allowed). |
| `runtimes.*.docker.extra_flags` | `[]` | Allowlisted extra `docker run` flags (`--cpus`, `--memory`, …). |
| `runtimes.*.ssh.host` | — | `user@host` for `runner: ssh`. |
| `runtimes.*.ssh.key_file` | unset | Private key for the ssh runner. |
| `runtimes.*.ssh.remote_dir` | unset | Remote working directory the worktree is synced to. |
| `runtimes.*.ssh.extra_flags` | `[]` | Extra `ssh` flags. |
| `plugins.playbooks` | `[]` | Plugin playbooks (see [plugins](#plugins)). |
| `plugins.prompts` | `{}` | Prompt-template overrides contributed by plugins. |
| `plugins.qa` | `[]` | Plugin QA checks. |
| `plugins.providers` | `{}` | Plugin model providers. |
| `methodology.ddd` | `true` | See [methodology](#methodology). |
| `methodology.tdd` | `true` | Tests-first guidance + test-file check. |
| `methodology.min_coverage_pct` | `80` | Reserved. |
| `methodology.allow_override` | `true` | Honour `methodology:` directives. |
| `devdb.provider` | unset | See [devdb](#devdb). |
| `devdb.template` | — | Template database. |
| `devdb.on_failure.keep_db` | `false` | Keep failed story DBs. |
| `devdb.on_failure.retain_hours` | `24` | Retention for kept DBs. |
| `devdb.docker.image` | `postgres:16` | Postgres image. |
| `devdb.docker.container_name` | `nxd-devdb-pg16` | Container name. |
| `devdb.docker.template_volume` | `~/.nxd/devdb-data` | Data volume. |
| `devdb.docker.network` | `nxd-devdb` | Docker network. |
| `devdb.docker.host_port_range` | `5500-5599` | Published port range. |
| `devdb.docker.host` | `localhost` | Connection host. |
| `notifications.enabled` | `false` | See [notifications](#notifications). |
| `notifications.webhook_url` | unset | Webhook endpoint. |
| `notifications.format` | `json` | `json` or `slack`. |
| `notifications.desktop` | `false` | macOS desktop notification. |
| `notifications.events` | default watched set | Override of the watched event types. |
| `notifications.timeout_s` | `5` | Per-delivery timeout. |
| `sandbox.mode` | `auto` | See [sandbox](#sandbox). |
| `sandbox.image` | `golang:1.26-alpine` | Sandbox container image. |
| `sandbox.network` | `none` | Sandbox network. |
| `sandbox.extra_mounts` | `[]` | Extra worktree-relative bind mounts. |
| `sandbox.cpus` | `2` | `docker run --cpus`. |
| `sandbox.memory` | `2g` | `docker run --memory`. |
| `sandbox.auto_approve_prompts` | unset (derived) | Watchdog auto-answers permission prompts; defaults to true only for docker/ssh runners. |
| `approvals.require_for` | `conflict_resolution`, `integration_failure`, `security_finding` | Decisions that create an approval item (see [approvals](#approvals)). |
| `approvals.timeout_action` | `pause` | Behaviour while an item is pending (only `pause`). |
