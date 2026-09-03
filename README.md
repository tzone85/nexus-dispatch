# NXD (Nexus Dispatch)

**Offline-first AI agent orchestration. Hand off a requirement, walk away, come back to merged code.**

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml/badge.svg)](https://github.com/tzone85/nexus-dispatch/actions/workflows/ci.yml)

NXD decomposes a natural-language requirement into stories, dispatches them in parallel waves to local-LLM-powered agents, runs code review + QA, and merges — **fully offline**, no API keys, no rate limits.

![NXD Demo](https://vhs.charm.sh/vhs-5mraO4n9IyQSvr2M9Eq1Vf.gif)

## Why offline?

- **No API costs** — run as many agents as your hardware allows
- **Full privacy** — code never leaves your machine
- **Works anywhere** — planes, off-grid, behind firewalls

## Quick start (5 min)

You need: Go 1.26+, [Ollama](https://ollama.com), [tmux](https://github.com/tmux/tmux), Python 3 for [MemPalace](https://github.com/milla-jovovich/mempalace).

```bash
# 1. Start Ollama (skip if already running as a service)
ollama serve &                        # or: brew services start ollama

# 2. Pull models
ollama pull qwen2.5-coder:14b         # planner + reviewer (~9 GB)
ollama pull gemma4:e4b                # coder (~6 GB)

# 3. Install NXD + MemPalace
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
pip install -r requirements.txt       # MemPalace

# 4. Run inside a git repo
nxd init
nxd req --background "Build a REST API for user management"
nxd req-logs <req-id>                 # tail the daemon log
nxd dashboard                         # TUI; add --web for the browser dashboard
```

The first run takes a few minutes while Ollama warms the models. `nxd req --background` self-daemonizes the pipeline; without it, `nxd req` plans then exits and you must run `nxd resume <req-id>` to dispatch agents. Follow progress via `nxd req-logs <req-id>` or the dashboard.

## Platform Support

| Platform | Build | Read-only commands | Full agent pipeline |
|----------|-------|--------------------|---------------------|
| macOS (Apple Silicon, Intel) | native | yes | yes |
| Linux (x86_64, arm64) | native | yes | yes |
| Windows 10/11 (native, no WSL) | `GOOS=windows go build` | yes | **no — requires tmux** |
| Windows + WSL2 (Ubuntu/Debian) | inside WSL | yes | yes |

NXD's agent execution pipeline depends on tmux for session isolation, recovery,
and live inspection, and tmux has no native Windows port. On native Windows the
binary still builds and runs — `nxd status`, `nxd dashboard`, `nxd doctor`,
`nxd config`, `nxd events`, and the report/metrics commands all work — but
`nxd req` / `nxd resume` will fail the tmux check with a clear pointer to
WSL2. The recommended Windows install path is WSL2 with Ubuntu, where the
Linux instructions in Quick start apply unchanged.

### Windows install (read-only CLI on native Windows)

```powershell
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
# Resulting binary lives at %USERPROFILE%\go\bin\nxd.exe — ensure that
# directory is on PATH (it is by default if you installed Go via the MSI).
nxd init
nxd doctor    # Will warn on the tmux check; everything else should pass.
```

State on Windows lives under `%USERPROFILE%\.nxd\` by default. The devdb
provider's Docker fallback dials `tcp://localhost:2375` (enable "Expose daemon
on tcp://localhost:2375 without TLS" in Docker Desktop) — or set `DOCKER_HOST`
explicitly. To pick a different host shell for the native runtime's
`run_command` tool and user metric/migration commands, set `NXD_SHELL=pwsh`
(or any shell available on PATH); the default is `cmd.exe`.

### Windows install (full pipeline via WSL2)

```powershell
# In an elevated PowerShell, one time:
wsl --install -d Ubuntu
```

```bash
# Inside the Ubuntu WSL shell:
sudo apt update && sudo apt install -y tmux git build-essential
# Install Go 1.26+ (apt's package may lag — see https://go.dev/dl).
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
nxd init && nxd doctor
```

## Core commands

| Command | What it does |
|---|---|
| `nxd init [--local-state]` | Create the state dir, generate `nxd.yaml`, check Ollama. `--local-state` keeps state in a git-ignored `./.nxd` per repo (recommended) |
| `nxd req "<requirement>"` | Submit a requirement; planning + dispatch |
| `nxd req --background` | Same, but self-daemonize; tail with `nxd req-logs` |
| `nxd resume <req-id>` | Continue a paused/planned requirement (after an approval, a fix, or a merged PR) |
| `nxd status` | Requirements + stories overview |
| `nxd dashboard [--web]` | Live TUI / browser dashboard |
| `nxd timeline [req-id]` | Chronological requirement history with per-story durations |
| `nxd approvals list\|approve\|reject` | Human approval queue — decide pending conflict/integration/security/merge items |
| `nxd cancel <req-id\|story-id>` | Stop a story (reset + kill its agent) or pause a whole requirement |
| `nxd state check\|repair\|compact\|rebuild` | Inspect and repair the event log / SQLite projection |
| `nxd doctor [--json]` | Preflight checks (Go, git, tmux, Ollama, config, sandbox, projection drift) |
| `nxd version` | Build version (same as `--version`) |

Every command accepts `--config <path>` and `--state-dir <path>` to target another project's config or state for one invocation.

Long runs can also push **notifications** (Slack-compatible webhook + macOS desktop) on completion/blocks/pauses, and a **budget guard** pauses a requirement before metered LLM spend exceeds `billing.budget_usd` — see [Configuration](docs/guides/configuration.md).

### Sandboxing

Commands that agents and QA run on your behalf — the native runtime's `run_command`, `command_succeeds`/`test_passes` criteria, the investigator's shell tool — go through a **command sandbox**. When Docker is available they execute in a throwaway container (`docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges`, worktree mounted at `/work`); otherwise they run on the host as a plain argv exec (no shell) with a one-time warning. Control it with `sandbox.mode: auto|docker|host` plus `sandbox.image`, `sandbox.network`, `sandbox.cpus`/`memory`. Every command is also checked against an argv-aware `command_allowlist`, and CLI agents (Claude Code, Codex, aider) can be confined with `runtimes.<name>.runner: docker|ssh`. `nxd doctor` reports where commands will run.

### Approvals

Risky decisions are not auto-resolved. An LLM-resolved rebase conflict, a post-merge integration build failure, a security-gate finding — and, if you opt in, every merge — create an **approval item** that pauses the requirement and blocks the story's merge until a human decides with `nxd approvals approve|reject <item-id> [--note ...]` or the dashboard's **Approvals** panel, followed by `nxd resume <req-id>`. Which kinds wait for you is `approvals.require_for` in `nxd.yaml`; items are persisted as `APPROVAL_REQUESTED` / `APPROVAL_RESOLVED` events, so every process sees the same queue.

Full CLI reference: [`docs/reference/cli-reference.md`](docs/reference/cli-reference.md).

## Where to next

| If you want to... | Read |
|---|---|
| Walk through your first run step-by-step | [Getting Started](docs/guides/getting-started.md) |
| Pick models for your RAM / VRAM | [Model Selection](docs/guides/model-selection.md) |
| Tune Gemma 4 or function calling | [Gemma 4 Guide](docs/guides/gemma-4-guide.md) |
| Edit `nxd.yaml` confidently | [Configuration](docs/guides/configuration.md) |
| Understand how the pipeline works | [Architecture Deep Dive](docs/guides/architecture.md) |
| Use ephemeral per-story Postgres DBs | [`docs/guides/configuration.md#devdb`](docs/guides/configuration.md) |
| Switch to cloud mode (Anthropic, GitHub PRs) | [Configuration](docs/guides/configuration.md) → `models` / `merge` |
| Fix a problem | [Troubleshooting](docs/guides/troubleshooting.md) |
| See every event type the system emits | [Event Reference](docs/reference/event-reference.md) |
| Build from source on macOS | [`docs/guides/getting-started.md#local-build`](docs/guides/getting-started.md) |

## Project layout (one-liner per package)

```
cmd/nxd/           CLI entry point (cobra)
internal/agent/    Role definitions, prompts, complexity scoring
internal/cli/      Cobra command implementations
internal/config/   YAML loader + validation
internal/dashboard/ TUI (Bubbletea), web (WebSocket)
internal/approvals/ Human approval queue (event-sourced)
internal/engine/   Planner, dispatcher, reviewer, QA, merger, watchdog, gates
internal/git/      Worktrees, branches, local merge, GitHub PRs
internal/llm/      Ollama / Anthropic / OpenAI / Google clients
internal/runtime/  Pluggable runtimes (gemma native, Aider, Claude Code, Codex), command sandbox, docker/ssh runners
internal/state/    Event store (JSONL) + SQLite projections + maintenance
internal/tmux/     Session lifecycle
```

Architecture deep dive: [`docs/guides/architecture.md`](docs/guides/architecture.md).

## Development

```bash
make build        # build nxd
make test         # tests with race + coverage
make install      # install to $GOPATH/bin
```

## Acknowledgements

Patterns and ideas borrowed from [Gastown](https://github.com/steveyegge/gastown), [Beads](https://github.com/steveyegge/beads), [Dolt](https://github.com/dolthub/dolt), [Hungry Ghost Hive](https://github.com/nikrich/hungry-ghost-hive), and [Wasteland](https://github.com/gastownhall/wasteland). Worth a read if you're into agent orchestration.

## License

[Apache License 2.0](LICENSE)
