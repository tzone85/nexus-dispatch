# Nexus Dispatch (NXD)

Multi-agent coding orchestrator that decomposes requirements into stories, dispatches them to LLM-powered agents in parallel waves, and runs a review/QA/merge pipeline.

## Architecture

```
nxd req → planner (LLM) → stories + DAG
nxd resume → dispatcher → executor → agents (parallel per wave)
                         → monitor → review → QA → merge
```

**Two runtime types:**
- **CLI runtimes** (aider, claude-code): run in tmux sessions, monitored via output polling
- **Native runtime** (Gemma): runs in-process goroutines calling Ollama via function calling, monitored via event store

## Key Packages

| Package | Purpose |
|---------|---------|
| `internal/engine/executor.go` | Spawns agents; `spawnNative` launches Gemma goroutines with semaphore-wrapped LLM client |
| `internal/engine/monitor.go` | Polls agents, drives post-execution pipeline (review→QA→merge), handles native agents via `pollNativeAgent` |
| `internal/engine/controller.go` | Periodic active controller with cancel/restart for stuck agents |
| `internal/runtime/gemma.go` | Native coding runtime with tool-calling loop, progress callbacks, scratchboard tools |
| `internal/llm/semaphore.go` | Concurrency limiter wrapping `llm.Client` (default 1 for single-GPU Ollama) |
| `internal/artifact/store.go` | Per-story artifact persistence (launch config, trace JSONL, diffs, QA/review results) |
| `internal/scratchboard/` | Cross-agent knowledge sharing (JSONL-backed, per-requirement) |
| `internal/criteria/` | Declarative success criteria (file_exists, file_contains, test_passes, coverage_above, command_succeeds) |
| `internal/web/eventbus.go` | In-process pub/sub for instant WebSocket event push |
| `internal/graph/export.go` | DAG export as JSON with nodes, edges, wave assignments |
| `internal/cli/resume.go` | Wires all features: artifact store, scratchboard, controller, semaphore |
| `internal/cli/dashboard.go` | Wires event bus into WebSocket hub |

## Build & Test

```bash
go build ./...                    # build everything
go test ./... -timeout 180s       # full test suite
go install ./cmd/nxd              # install binary to ~/go/bin/nxd
```

## Configuration

Config file: `nxd.yaml` in the project root. Key sections:

```yaml
runtimes:
  gemma:
    native: true
    max_iterations: 20
    concurrency: 1          # Ollama concurrency limit (default 1)
    models: [gemma4]
    command_allowlist: [go build ./..., go test ./..., go vet ./..., make]

controller:
  enabled: false            # set true to auto-restart stuck agents
  interval_s: 60
  max_stuck_duration_s: 300
  auto_restart: true
  max_actions_per_tick: 1
  cooldown_s: 120
```

## Conventions

- Go module: `github.com/tzone85/nexus-dispatch`
- Commit format: `<type>: <description>` (feat, fix, refactor, docs, test, chore)
- Event-sourced: all state changes go through `EventStore.Append()` → `ProjectionStore.Project()`
- Immutable data: create new objects, never mutate
- File size: 200-400 lines typical, 800 max
- Tests: 80%+ coverage target, TDD preferred

## Sibling Project

VXD (vortex-dispatch) at `~/Sites/misc/vortex-dispatch` is the CLI-only variant (no native Gemma runtime). Shares: artifact store, scratchboard, DAG export, criteria, event store patterns. Does NOT share: semaphore, native runtime, controller, event bus.

## Smoke Test

Test project at `~/Sites/misc/nxd-smoke-test` with `nxd.yaml` configured for `gemma4:e4b` via Ollama. Clear state before re-running:
```bash
kill <stale-pid>
rm -f ~/.nxd/nxd.lock ~/.nxd/events.jsonl ~/.nxd/nxd.db
```

## In-Progress Work

- Controller disabled by default, needs `nxd.yaml` config to enable
- Test coverage at 51.9% (target 80%) — largest gap is CLI commands at 0%

## Recent Additions

- **Controller hardening**: `ActionReprioritize` implemented, `CONTROLLER_STUCK_DETECTED` events, `AutoReprioritize` config, full test suite (19 tests)
- **DAG visualization**: SVG renderer in web dashboard with wave-grouped layout, status-colored nodes, dependency edges
- **CLI commands**: `nxd logs <story-id>` (trace log viewer with `--follow`), `nxd diff <story-id>` (worktree diff)
- **Cost estimation**: `CalculateLLMCost()` and `CalculateCostWithTokens()` wire actual token usage into billing
- **Native runtime**: criteria evaluation after `task_complete`, scratchboard tools already present
- **CI**: coverage threshold check and `go vet` step added to workflow
