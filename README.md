# NXD (Nexus Dispatch)

**Offline-first AI agent orchestration. Hand off a requirement, walk away, come back to merged code.**

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml/badge.svg)](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml)

## Overview

NXD is a Go CLI that orchestrates autonomous AI agents through the full software development lifecycle — **entirely offline**. It uses local LLMs via [Ollama](https://ollama.com), local git merges (no GitHub dependency), and local coding runtimes like [Aider](https://github.com/paul-gauthier/aider).

Submit a natural-language requirement and NXD decomposes it into stories, assigns them to agents based on complexity, executes work in parallel waves, runs code review and QA, and merges them — all without network access or API keys.

![NXD system overview](docs/diagrams/system-overview.svg)

### Why Offline?

- **No API costs** — run as many agents as your hardware allows, for free
- **No rate limits** — local models don't throttle you
- **Full privacy** — your code never leaves your machine
- **No vendor lock-in** — swap models anytime via config
- **Works anywhere** — planes, trains, off-grid cabins

### Key Capabilities

- **Event-sourced state management** with append-only event log and SQLite projections
- **Full agile team hierarchy** — Tech Lead, Senior, Intermediate, Junior, QA, Supervisor
- **Local LLM inference** via Ollama (Gemma 4 coder + Qwen3-Coder reviewer)
- **Pluggable runtimes** — Aider (default), Claude Code, Codex, Gemini CLI
- **Local git merges** — no GitHub API required (optional GitHub mode available)
- **Wave-based parallel execution** with topological dependency resolution

## Prerequisites

1. **Go 1.26+** — [install](https://go.dev/dl/)
2. **Ollama** — [install](https://ollama.com) then pull the **two recommended models**:
   ```bash
   ollama pull qwen3-coder                 # ~19GB — reviewer + planner (262K context, SWE-bench 51.6%)
   ollama pull gemma4:e4b                  # ~6GB  — coder, native function calling
   ```
   NXD pairs different model families so the reviewer doesn't share the coder's blind spots. `qwen3-coder:30b` is a MoE model — inference speed tracks its 3.3B active params, not the 30B total. See [Model Selection](docs/guides/model-selection.md) for the rationale + GPU-swap trade-off, and [Gemma 4 Guide](docs/guides/gemma-4-guide.md) for hardware tuning.

   <details>
   <summary>Budget two-model split (24GB RAM)</summary>

   `qwen3-coder:30b` + `gemma4:e4b` needs ~25GB combined. On 24GB machines use the smaller reviewer:
   ```bash
   ollama pull qwen2.5-coder:14b           # ~9GB — reviewer + planner
   ollama pull gemma4:e4b                  # ~6GB — coder
   ```
   Update `nxd.yaml` to set `senior`, `tech_lead`, and `qa` to `model: qwen2.5-coder:14b`.
   </details>

   <details>
   <summary>Single-model fallback (16GB RAM)</summary>

   If you don't have VRAM for two models, use `gemma4:e4b` for every role. NXD will print a `same-model review` warning at startup — that's expected for this config.
   ```bash
   ollama pull gemma4:e4b
   ```
   </details>

   <details>
   <summary>Heavy setup (~37GB, for 64GB+ RAM machines)</summary>

   For maximum quality, pin both models in VRAM — upgrade the coder to the larger Gemma:
   ```bash
   ollama pull qwen3-coder                 # Reviewer / Tech Lead (~19GB)
   ollama pull gemma4:26b                  # Coder (~18GB)
   export OLLAMA_KEEP_ALIVE=24h            # eliminate model-swap latency
   export OLLAMA_MAX_LOADED_MODELS=2
   ```
   </details>

3. **MemPalace** — local-first semantic memory used by the planner / reviewer / native runtime to mine prior work and surface relevant context. Offline-first by design (ChromaDB local backend, zero API calls). Pinned in `requirements.txt`:
   ```bash
   make install-mempalace        # or: pip install -r requirements.txt
   make mempalace-check          # roundtrip smoke
   ```
   The bridge degrades gracefully if MemPalace is unavailable (status shows in `nxd doctor`), but core infrastructure expects it installed.
4. **Aider** (optional runtime, for non-Gemma models) — `pip install aider-chat`
5. **tmux** — `brew install tmux` (macOS) or `apt install tmux` (Linux)

### Hardware Recommendations

| Setup | RAM | GPU VRAM | Models | Disk Space |
|-------|-----|----------|--------|------------|
| Minimal | 16GB | 8GB | `gemma4:e4b` for all roles (single-model warning) | ~6GB |
| Budget | 24GB | 16GB | `qwen2.5-coder:14b` + `gemma4:e4b` (two-model split) | ~15GB |
| **Recommended** | **32GB+** | **24GB+** | **`qwen3-coder:30b` + `gemma4:e4b` (two-model split)** | **~25GB** |
| Heavy | 64GB+ | 48GB+ | `qwen3-coder:30b` + `gemma4:26b` pinned in VRAM | ~37GB |

All three setups run the complete NXD pipeline. The difference is **bug detection** (the two-model split catches mistakes a single model misses) and output quality. Start with the recommended split if you have the VRAM — single-model is a fallback, not the goal.

## Quick Start

```bash
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
pip install -r requirements.txt          # installs pinned MemPalace
nxd init
nxd req "Build a REST API for user management with CRUD endpoints"
nxd status
nxd dashboard
```

### Demo

![NXD Demo](https://vhs.charm.sh/vhs-5mraO4n9IyQSvr2M9Eq1Vf.gif)

To regenerate the demo locally, you'll need [VHS](https://github.com/charmbracelet/vhs) with `ffmpeg` and `ttyd`. On macOS:

```bash
brew install vhs ffmpeg ttyd
vhs docs/demo.tape
```

See the [full getting started guide](docs/guides/getting-started.md) for a step-by-step walkthrough.

## Features

- **Agent hierarchy with complexity-based routing** — Fibonacci scoring routes stories to the right tier
- **Ollama-powered LLM inference** — all planning, review, and supervision runs locally
- **Local git merge** — stories merge directly into your base branch, no PRs needed
- **Event-sourced architecture** — append-only event log with materialized SQLite projections
- **Pluggable runtimes via YAML config** — Aider (default), Claude Code, Codex, Gemini CLI
- **Watchdog monitoring** — stuck detection, permission bypass, context freshness checks
- **Supervisor oversight** — periodic drift detection and reprioritization
- **LLM-powered conflict resolution** — automatic merge conflict resolution during rebase using local Ollama or cloud LLMs
- **Senior code review** — automated review via local LLM
- **Automated QA pipeline** — lint, build, and test execution per story
- **Fatal error detection** — non-retryable API errors (401, 403, billing exhaustion) pause the requirement instead of retrying forever
- **Tiered cleanup** — worktree pruning, branch garbage collection with configurable retention
- **TUI dashboard** — single-pane Bubbletea interface with agents, pipeline, stories, activity, and escalations visible at once
- **Web dashboard** — browser-based dashboard (`nxd dashboard --web`) with real-time WebSocket updates, DAG visualization, and full control panel
- **Active controller** — periodic stuck detection with auto-cancel, auto-restart, or auto-reprioritize actions
- **Declarative success criteria** — file_exists, file_contains, test_passes, coverage_above, command_succeeds checks
- **Cost estimation** — per-token LLM cost tracking with client-facing quote generation
- **Story trace logs** — per-story JSONL trace with LLM exchanges, tool calls, and progress events
- **Reputation scoring** — per-agent performance tracking across assignments
- **Optional cloud mode** — swap to Anthropic/OpenAI APIs and GitHub PRs when online

## CLI Commands

| Command | Description |
|---------|-------------|
| `nxd init` | Initialize workspace, create `~/.nxd/` dirs, set up stores, check Ollama |
| `nxd req <requirement>` | Submit a requirement for Tech Lead decomposition into stories |
| `nxd status [--req ID]` | Show requirement and story status, optionally filtered by requirement |
| `nxd resume <req-id>` | Resume a paused pipeline, dispatch the next wave of ready stories |
| `nxd agents [--status S]` | List all agents with current story, session, and status |
| `nxd escalations` | List all escalation events with story, agent, reason, and status |
| `nxd gc [--dry-run]` | Garbage-collect merged branches and worktrees past retention |
| `nxd config show` | Pretty-print the current configuration as YAML |
| `nxd config validate` | Load and validate the configuration file |
| `nxd events [--type T] [--story S] [--limit N]` | List events from the event store, newest first |
| `nxd dashboard` | Launch the live TUI dashboard (single-pane) |
| `nxd dashboard --web [--port 8787]` | Launch the web dashboard in your browser |
| `nxd logs <story-id>` | Show trace log for a story (LLM calls, tool executions, progress) |
| `nxd diff <story-id>` | Show git diff for a story's worktree against the base branch |
| `nxd estimate <requirement>` | Estimate cost and effort for a requirement |
| `nxd report <req-id>` | Generate a client delivery report for a completed requirement |
| `nxd doctor` | Run preflight checks (Go, git, tmux, Ollama, config) |

## Configuration

Run `nxd init` to generate `nxd.yaml` with sensible defaults, then customize:

| Section | Purpose |
|---------|---------|
| `workspace` | State directory, backend (sqlite), log level and retention |
| `models` | LLM provider (ollama/anthropic/openai) and model per agent role |
| `routing` | Complexity thresholds, retry and escalation limits |
| `monitor` | Poll interval, stuck threshold, context freshness token limit |
| `cleanup` | Worktree pruning strategy, branch retention days |
| `merge` | Mode (local/github), auto-merge toggle, base branch |
| `runtimes` | CLI runtime definitions (command, args, model list, detection patterns) |
| `controller` | Active stuck detection: auto-restart, auto-reprioritize, cooldown |
| `billing` | Cost estimation rates, currency, per-token LLM cost tracking |
| `qa` | Declarative success criteria evaluated after agent task completion |
| `devdb` | Per-story ephemeral Postgres (planned 2026-05-21). Backend (`docker`/`null`), template DB, on-failure retention. Docker-only — NXD stays offline. |

### Ephemeral Databases (planned)

> **Status:** Design spec complete (2026-05-21). See `docs/superpowers/specs/2026-05-21-ephemeral-dbs-master-design.md`.

Each story can get its own throwaway local Postgres, forked from a template, deleted on completion. Inspired by [ghost.build](https://ghost.build) but Docker-backed and fully offline (NXD's design principle).

**Shines for:** per-story migration testing, schema-aware code generation, destructive SQL testing, multi-agent experimentation.

**Skip when:** pure-frontend stories, prod-touching ops, stories that finish in seconds.

**Minimum config:**

```yaml
devdb:
  provider: docker          # or null
  template: my-test-snapshot
  docker:
    image: postgres:16
    host_port_range: "5600-5699"
```

Agents read `DATABASE_URL` from `.nxd-db/connect.env` (auto-injected into the worktree). Humans use `nxd db list/connect/logs/delete` + dashboard's per-story DB column.

### Offline vs Cloud Mode

NXD defaults to fully offline operation. To switch to cloud mode, update `nxd.yaml`:

```yaml
# Switch models to cloud providers
models:
  tech_lead:
    provider: anthropic
    model: claude-opus-4-20250514

# Switch merge to GitHub PRs
merge:
  mode: github

# Switch runtime to Claude Code
runtimes:
  claude-code:
    command: claude
    args: ["--dangerously-skip-permissions"]
```

### Godmode (Autonomous Execution)

The `--godmode` flag skips permission prompts on agent runtimes that support it (Claude Code's `--dangerously-skip-permissions`, Codex's `--approval-mode full-auto`). Since NXD primarily uses Aider with Ollama, godmode has no effect on the default runtime but is available for users who configure Claude Code or Codex runtimes.

```bash
# One-off: enable via CLI flag
nxd req --godmode "Build a REST API for user management"
nxd resume --godmode 01JABCDEF

# Persistent: enable in nxd.yaml
planning:
  godmode: true
```

The CLI flag takes precedence over the config file. When neither is set, godmode defaults to `false`.

## Architecture

```
Requirement
    |
    v
[Intake] --> nxd req decomposes via Tech Lead (local Ollama)
    |
    v
[Planning] --> Stories with Fibonacci complexity + dependency DAG
    |
    v
[Dispatch] --> Wave-based parallel assignment (topo sort on DAG)
    |
    v
[Execution] --> Agents work in tmux sessions via Aider + Ollama
    |
    v
[Review] --> Senior agent reviews diff via local LLM
    |
    v
[QA] --> Lint + build + test pipeline (all local)
    |
    v
[Rebase] --> LLM-powered conflict resolution (if conflicts detected)
    |
    v
[Merge] --> Local git merge (or GitHub PR in cloud mode)
    |
    v
[Cleanup] --> Worktree prune + branch GC
```

Events are appended at every stage. SQLite projections materialize the current state for queries.

## Agent Roles

| Role | Default Model | Responsibility |
|------|---------------|----------------|
| Tech Lead | `qwen3-coder:30b` | Requirement decomposition, story planning, dependency graphs |
| Senior | `qwen3-coder:30b` | Code review of junior/intermediate work (different family from coder) |
| Intermediate | `gemma4:e4b` | Medium stories (3-5 points), native function calling |
| Junior | `gemma4:e4b` | Simple stories (1-3 points), native function calling |
| QA | `qwen3-coder:30b` | Lint, build, test execution per story; failure analysis |
| Supervisor | `gemma4:e4b` | Drift detection, reprioritization, escalation handling |

## Project Structure

```
cmd/nxd/              CLI entry point
internal/
  agent/              Role definitions, complexity scoring, prompts
  cli/                Cobra command implementations
  config/             YAML config loader and validation
  dashboard/          Bubbletea TUI (pipeline, agents, activity, escalations)
  engine/             Core orchestration
    planner.go        Tech Lead decomposition
    dispatcher.go     Wave-based parallel dispatch
    watchdog.go       Stuck detection, permission bypass
    supervisor.go     Drift detection, reprioritization
    conflict_resolver.go  LLM-powered merge conflict resolution
    reviewer.go       Senior code review
    qa.go             Lint/build/test pipeline
    merger.go         PR creation, auto-merge, or local merge
    reaper.go         Tiered cleanup and GC
  git/                Branch, worktree, local merge, and GitHub PR operations
  graph/              Dependency DAG with topological sort
  llm/                Ollama, Anthropic, and OpenAI clients + model registry
  runtime/            Pluggable runtime registry (Aider, Claude Code, Codex)
  state/              Event store (file-based) + SQLite projections
  tmux/               Session management (create, capture, send-keys)
migrations/           SQLite schema migrations
test/                 E2E tests
```

## Testing

```bash
go test ./...                    # Unit + integration
go test -tags e2e ./test/        # E2E tests
go test ./... -race -coverprofile=coverage.out  # With race detection + coverage
```

## Development

```bash
make build    # Build the nxd binary
make test     # Run tests with race detection and coverage
make lint     # Run golangci-lint
make clean    # Remove binary and coverage artifacts
make install  # Build and install to $GOPATH/bin
```

### Local Build & Install (macOS)

If you're building from source on macOS, you may need to complete the following setup steps before `make install` and `nxd` will work correctly.

**1. Ensure `~/go/bin` exists**

The `make install` target moves the built binary to your Go bin directory. If this directory doesn't exist yet, create it:

```bash
mkdir -p "$(go env GOPATH)/bin"
```

**2. Add `~/go/bin` to your PATH**

Add the following line to your `~/.zshrc` (or `~/.bash_profile` if using Bash):

```bash
export PATH="$HOME/go/bin:$PATH"
```

**3. Reload your shell**

```bash
source ~/.zshrc
```

**4. Build and install**

```bash
make install
nxd config validate   # Verify config is valid
nxd --help
```

> **Note:** These instructions are for macOS. Windows setup may differ — refer to the [Go installation docs](https://go.dev/doc/install) for platform-specific guidance on configuring `GOPATH` and `PATH`.

## Documentation

| Guide | Description |
|-------|-------------|
| [Getting Started](docs/guides/getting-started.md) | Installation, first run, step-by-step tutorial |
| [Gemma 4 Guide](docs/guides/gemma-4-guide.md) | Gemma 4 setup, hardware tuning, function calling, runtime choice |
| [Function Calling](docs/guides/function-calling.md) | Tool definitions, validation, graceful degradation |
| [Migration Guide](docs/guides/migration-from-v0.md) | Upgrading from DeepSeek/Qwen to Gemma 4 |
| [Configuration](docs/guides/configuration.md) | Full config reference with examples for every hardware tier |
| [Architecture Deep Dive](docs/guides/architecture.md) | Event sourcing, agent hierarchy, wave dispatch, monitoring |
| [Model Selection](docs/guides/model-selection.md) | Pick the right Ollama models for your hardware |
| [Troubleshooting](docs/guides/troubleshooting.md) | Common issues, diagnostics, and fixes |
| [CLI Reference](docs/reference/cli-reference.md) | Complete command, flag, and option reference |
| [Event Reference](docs/reference/event-reference.md) | All 31 event types with payloads and state transitions |

## Acknowledgements

NXD builds on ideas and patterns from several open-source projects. We're grateful for their pioneering work in AI agent orchestration:

| Project | Author | What We Learned |
|---------|--------|-----------------|
| [Gastown](https://github.com/steveyegge/gastown) | Steve Yegge | Git-backed persistence, runtime abstraction, convoy/formula system |
| [Beads](https://github.com/steveyegge/beads) | Steve Yegge | Hash-based task IDs, dependency-aware graph, memory decay patterns |
| [Dolt](https://github.com/dolthub/dolt) | DoltHub | Version-controlled SQL state, branch-per-agent isolation, row-level diffing |
| [Hungry Ghost Hive](https://github.com/nikrich/hungry-ghost-hive) | nikrich | Agile team hierarchy, complexity-based routing, micromanager daemon |
| [Wasteland](https://github.com/gastownhall/wasteland) | Gastown Hall | Reputation scoring, embedded web UI, tiered cleanup strategies |

If you're interested in AI agent orchestration, these projects are well worth studying.

## License

[Apache License 2.0](LICENSE)

---

Built with the philosophy: **orchestrate agents like a real agile team — no cloud required.**
