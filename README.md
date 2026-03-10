# NXD (Nexus Dispatch)

**Offline-first AI agent orchestration. Hand off a requirement, walk away, come back to merged code.**

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml/badge.svg)](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml)

## Overview

NXD is a Go CLI that orchestrates autonomous AI agents through the full software development lifecycle — **entirely offline**. It uses local LLMs via [Ollama](https://ollama.com), local git merges (no GitHub dependency), and local coding runtimes like [Aider](https://github.com/paul-gauthier/aider).

Submit a natural-language requirement and NXD decomposes it into stories, assigns them to agents based on complexity, executes work in parallel waves, runs code review and QA, and merges them — all without network access or API keys.

### Why Offline?

- **No API costs** — run as many agents as your hardware allows, for free
- **No rate limits** — local models don't throttle you
- **Full privacy** — your code never leaves your machine
- **No vendor lock-in** — swap models anytime via config
- **Works anywhere** — planes, trains, off-grid cabins

### Key Capabilities

- **Event-sourced state management** with append-only event log and SQLite projections
- **Full agile team hierarchy** — Tech Lead, Senior, Intermediate, Junior, QA, Supervisor
- **Local LLM inference** via Ollama (DeepSeek Coder V2, Qwen2.5-Coder, CodeLlama)
- **Pluggable runtimes** — Aider (default), Claude Code, Codex, Gemini CLI
- **Local git merges** — no GitHub API required (optional GitHub mode available)
- **Wave-based parallel execution** with topological dependency resolution

## Prerequisites

1. **Go 1.23+** — [install](https://go.dev/dl/)
2. **Ollama** — [install](https://ollama.com) then pull models:
   ```bash
   ollama pull deepseek-coder-v2:latest    # Tech Lead + Supervisor
   ollama pull qwen2.5-coder:32b           # Senior (if you have 24GB+ RAM)
   ollama pull qwen2.5-coder:14b           # Intermediate + QA
   ollama pull qwen2.5-coder:7b            # Junior
   ```
3. **Aider** (recommended runtime) — `pip install aider-chat`
4. **tmux** — `brew install tmux` (macOS) or `apt install tmux` (Linux)

### Hardware Recommendations

| Setup | RAM | GPU VRAM | Models You Can Run |
|-------|-----|----------|--------------------|
| Minimum | 16GB | 8GB | 7B models (Junior only) |
| Recommended | 32GB | 16GB | Up to 14B (Junior + Intermediate) |
| Full Team | 64GB+ | 24GB+ | Up to 32B (all roles) |

## Quick Start

```bash
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
nxd init
nxd req "Build a REST API for user management with CRUD endpoints"
nxd status
nxd dashboard
```

### Demo

Generate an animated demo with [VHS](https://github.com/charmbracelet/vhs).

VHS requires `ffmpeg` and `ttyd` to record terminal sessions. On macOS, install all prerequisites with Homebrew:

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
- **Senior code review** — automated review via local LLM
- **Automated QA pipeline** — lint, build, and test execution per story
- **Tiered cleanup** — worktree pruning, branch garbage collection with configurable retention
- **TUI dashboard** — 4-panel Bubbletea interface (pipeline, agents, activity, escalations)
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
| `nxd dashboard` | Launch the live TUI dashboard |

## Configuration

Copy `nxd.config.example.yaml` to `nxd.yaml` (or run `nxd init`) and customize:

| Section | Purpose |
|---------|---------|
| `workspace` | State directory, backend (sqlite), log level and retention |
| `models` | LLM provider (ollama/anthropic/openai) and model per agent role |
| `routing` | Complexity thresholds, retry and escalation limits |
| `monitor` | Poll interval, stuck threshold, context freshness token limit |
| `cleanup` | Worktree pruning strategy, branch retention days |
| `merge` | Mode (local/github), auto-merge toggle, base branch |
| `runtimes` | CLI runtime definitions (command, args, model list, detection patterns) |

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
[Merge] --> Local git merge (or GitHub PR in cloud mode)
    |
    v
[Cleanup] --> Worktree prune + branch GC
```

Events are appended at every stage. SQLite projections materialize the current state for queries.

## Agent Roles

| Role | Default Model | Responsibility |
|------|---------------|----------------|
| Tech Lead | DeepSeek Coder V2 (16B) | Requirement decomposition, story planning, dependency graphs |
| Senior | Qwen2.5-Coder (32B) | Complex stories (5+ points), code review of junior/intermediate work |
| Intermediate | Qwen2.5-Coder (14B) | Medium stories (3-5 points) |
| Junior | Qwen2.5-Coder (7B) | Simple stories (1-3 points) |
| QA | Qwen2.5-Coder (14B) | Lint, build, test execution per story |
| Supervisor | DeepSeek Coder V2 (16B) | Drift detection, reprioritization, escalation handling |

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

**4. Set up the config file**

NXD requires a `nxd.yaml` config file in the project root. You can either let `nxd init` create it for you, or copy it manually:

```bash
cp nxd.config.example.yaml nxd.yaml
```

Then customize it as needed (see [Configuration](docs/guides/configuration.md) for details).

**5. Build and install**

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
