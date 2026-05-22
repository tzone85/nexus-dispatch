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

You need: Go 1.26+, [Ollama](https://ollama.com), [tmux](https://github.com/tmux/tmux), Python 3 for [MemPalace](https://github.com/yourorg/mempalace).

```bash
ollama pull qwen2.5-coder:14b         # planner + reviewer (~9 GB)
ollama pull gemma4:e4b                # coder (~6 GB)

go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
pip install -r requirements.txt       # MemPalace
nxd init
nxd req "Build a REST API for user management"
nxd dashboard                         # watch it work
```

The first run takes a few minutes while Ollama warms the models. After `nxd req` returns, agents continue in the background — use `nxd dashboard` (TUI) or `nxd dashboard --web` (browser) to follow along.

## Core commands

| Command | What it does |
|---|---|
| `nxd init` | Create `~/.nxd/`, generate `nxd.yaml`, check Ollama |
| `nxd req "<requirement>"` | Submit a requirement; planning + dispatch |
| `nxd req --background` | Same, but self-daemonize; tail with `nxd req-logs` |
| `nxd status` | Requirements + stories overview |
| `nxd dashboard [--web]` | Live TUI / browser dashboard |
| `nxd doctor` | Preflight checks (Go, git, tmux, Ollama, config) |

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
internal/engine/   Planner, dispatcher, reviewer, QA, merger, watchdog
internal/git/      Worktrees, branches, local merge, GitHub PRs
internal/llm/      Ollama / Anthropic / OpenAI clients
internal/runtime/  Pluggable runtimes (gemma native, Aider, Claude Code, Codex)
internal/state/    Event store (JSONL) + SQLite projections
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

---

_Made with ❤️ by [Vortex Dispatch](https://github.com/tzone85/vortex-dispatch) (NXD is the offline-first sibling). Remove this line freely._
