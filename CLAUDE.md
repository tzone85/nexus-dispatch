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
| `internal/engine/executor.go` | Spawns agents; `spawnNative` launches Gemma goroutines with semaphore-wrapped LLM client, wires criteria from QA config |
| `internal/engine/monitor.go` | Polls agents, drives post-execution pipeline (review→QA→merge), handles native agents via `pollNativeAgent` |
| `internal/engine/controller.go` | Periodic active controller with cancel/restart/reprioritize for stuck agents, emits `CONTROLLER_STUCK_DETECTED` events |
| `internal/engine/cost.go` | Cost estimation: `CalculateCost`, `CalculateLLMCost`, `CalculateCostWithTokens` with per-token billing |
| `internal/engine/report.go` | Client delivery reports with actual token cost via `sumTokenUsage()` from metrics.jsonl |
| `internal/runtime/gemma.go` | Native coding runtime with tool-calling loop, progress callbacks, scratchboard tools, criteria evaluation |
| `internal/llm/semaphore.go` | Concurrency limiter wrapping `llm.Client` (default 1 for single-GPU Ollama) |
| `internal/artifact/store.go` | Per-story artifact persistence (launch config, trace JSONL, diffs, QA/review results) |
| `internal/scratchboard/` | Cross-agent knowledge sharing (JSONL-backed, per-requirement) |
| `internal/criteria/` | Declarative success criteria (file_exists, file_contains, test_passes, coverage_above, command_succeeds) |
| `internal/web/eventbus.go` | In-process pub/sub for instant WebSocket event push |
| `internal/web/static/app.js` | Web dashboard frontend: DAG SVG visualization, agents, pipeline, stories, activity, review gates |
| `internal/graph/export.go` | DAG export as JSON with nodes, edges, wave assignments |
| `internal/cli/resume.go` | Wires all features: artifact store, scratchboard, controller, semaphore |
| `internal/cli/logs.go` | `nxd logs <story-id>` — trace JSONL viewer with `--follow`, `--lines`, `--raw` |
| `internal/cli/diff.go` | `nxd diff <story-id>` — worktree diff against base branch with `--stat`, `--cached` |
| `internal/cli/dashboard.go` | Wires event bus into WebSocket hub |

## Build & Test

```bash
go build ./...                    # build everything
go test ./... -timeout 180s       # full test suite
go vet ./...                      # static analysis
go install ./cmd/nxd              # install binary to ~/go/bin/nxd
make test                         # tests with race detection + coverage report
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
  enabled: false            # set true to auto-manage stuck agents
  interval_s: 60
  max_stuck_duration_s: 300
  auto_restart: true        # reset stuck stories to draft
  auto_reprioritize: false  # escalate tier + reset (takes priority over restart)
  auto_cancel: false        # just cancel, no reset
  max_actions_per_tick: 1
  cooldown_s: 120

billing:
  default_rate: 150
  currency: USD
  llm_costs:
    mode: per_token          # or "subscription" for $0 LLM cost
    rates:
      gemma4:
        input_per_1k: 0.0    # free via Ollama
        output_per_1k: 0.0

qa:
  success_criteria:          # auto-evaluated by native runtime after task_complete
    - kind: file_exists
      path: go.mod
    - kind: command_succeeds
      value: go build ./...
```

## Conventions

- Go module: `github.com/tzone85/nexus-dispatch`
- Go version: 1.26+ (see `go.mod`)
- Commit format: `<type>: <description>` (feat, fix, refactor, docs, test, chore)
- Event-sourced: all state changes go through `EventStore.Append()` → `ProjectionStore.Project()`
- Immutable data: create new objects, never mutate
- File size: 200-400 lines typical, 800 max
- Tests: 80%+ coverage target, TDD preferred
- CI: `go vet`, `go test -race`, coverage threshold (50% floor, ratcheting up)

## Sibling Project

VXD (vortex-dispatch) at `~/Sites/misc/vortex-dispatch` is the CLI-only variant (no native Gemma runtime). Shares: artifact store, scratchboard, DAG export, criteria, event store patterns. Does NOT share: semaphore, native runtime, controller, event bus.

## Smoke Test

Test project at `~/Sites/misc/nxd-smoke-test` with `nxd.yaml` configured for `gemma4:e4b` via Ollama. Clear state before re-running:
```bash
kill <stale-pid>
rm -f ~/.nxd/nxd.lock ~/.nxd/events.jsonl ~/.nxd/nxd.db
```

## Current State (2026-04-12)

- **Coverage**: 63.6% total (target 80%); CLI 57%, web 62%, engine 64%, llm 85%
- **CI**: test + vet + build pass; lint non-blocking (golangci-lint doesn't support Go 1.26 yet)
- **Controller**: disabled by default, production-ready with reprioritize/restart/cancel + 19 tests
- **Web dashboard**: DAG SVG visualization, review gates, metrics, recovery log, investigations
- **Native runtime**: criteria evaluation wired from `config.QA.SuccessCriteria`, results in `STORY_COMPLETED` payload
- **Cost estimation**: `CalculateLLMCost` and `CalculateCostWithTokens` wired into report builder with actual metrics data
- **Remaining gap to 80%**: primarily functions requiring external processes (tmux, git CLI, Ollama) — monitor pipeline methods, runtime spawn/detect, git/github PR operations

## Test Infrastructure

CLI tests use a shared test environment (`internal/cli/testenv_test.go`):
- `setupTestEnv(t)` — creates temp dir with `nxd.yaml`, event store, and SQLite projection store
- `seedTestReq`, `seedTestStory`, `seedTestAgent`, `seedTestEscalation` — populate stores with test data
- `execCmd(t, cmd, cfgPath, args...)` — Cobra testing helper that sets config flag, captures output, and executes
- `InsertAgent` on `SQLiteStore` — direct SQL insert for agents (AGENT_SPAWNED events are not projected)
- `withMockLLM(t, responses...)` — injects `ReplayClient` via `buildLLMClientFunc` for testing orchestration commands without API access
- `initTestRepo(t, dir)` — creates minimal git repo with one commit for commands that need worktrees

Test files and counts:
- `cli/commands_test.go` — 40+ tests: status (text+JSON), agents, events, escalations, pause, approve, reject, report, config, gc, metrics, logs, diff, registration, and utility functions
- `cli/orchestration_test.go` — 11 tests: runReq (greenfield+review), archive, buildLLMClient providers (ollama, anthropic, openai, google, google+ollama, unsupported), watch with context cancellation
- `engine/controller_test.go` — 19 tests: decideAction priority chain, lastProgressTime, cancelStory, resetStoryToDraft, reprioritizeStory, tick with stuck detection/cooldown/max actions, RunLoop lifecycle
- `engine/helpers_test.go` — 12 tests: stripCodeFences, truncateDiff, tierForRole (7 roles), configCriteriaToRuntime, executor setters
- `llm/errors_test.go` — 20+ tests: IsFatalAPIError, IsInsufficientBalance, IsRateLimited, IsOverloaded, IsRetryable, RetryAfterSeconds, APIError.Error, QuotaError
- `web/server_test.go` — 29 tests: all 11 HandleCommand actions with success/error paths
- `web/data_test.go` — 10 tests: BuildSnapshot (empty/data/pipeline/gates/events), SnapshotJSON, mapStatusToBucket, intFromPayload
- `web/eventbus_test.go` — 5 tests: subscribe+publish, multiple subscribers, unsubscribe, slow consumer drop, no subscribers
- `web/metrics_test.go` — 3 tests: convertSummary, zero calls safety, MemPalaceCheck nil
- `state/sqlite_test.go` — extended with: ListRequirementsFiltered, ListRequirements, InsertAgent, ListAgents, ArchiveRequirement, ArchiveStoriesByReq, ListStoryDeps, DecodePayload

## Event Types

Controller events (added 2026-04-12):
- `CONTROLLER_ANALYSIS` — emitted each tick with stories_checked and actions_taken counts
- `CONTROLLER_ACTION` — emitted per corrective action with kind (cancel/restart/reprioritize) and reason
- `CONTROLLER_STUCK_DETECTED` — emitted when a story exceeds stuck threshold, includes stuck_duration_s and escalation_tier
