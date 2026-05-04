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
| `internal/runtime/gemma.go` | Native coding runtime with tool-calling loop, criteria-gated completion, self-correction, rejection budget, scratchboard tools |
| `internal/routing/bayesian.go` | Bayesian adaptive routing: Beta distribution priors per role/complexity, update rules, decay, persistence |
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
make setup                        # one-shot bootstrap (Go deps + MemPalace + doctor)
make mempalace-check              # smoke the MemPalace bridge end-to-end
```

## Core Infrastructure: MemPalace

MemPalace is the local-first semantic memory layer NXD relies on for
mining diffs / review feedback / QA failures and retrieving prior work
as agent prompt context. **Offline-first by design** (ChromaDB local
backend, zero API calls — see https://github.com/milla-jovovich/mempalace).
Pinned at `mempalace==2.0.0` in `requirements.txt`. The Python bridge
lives at `scripts/mempalace_bridge.py` and is wrapped by
`internal/memory/mempalace.go`.

Install: `pip install -r requirements.txt` (or `make install-mempalace`).
CI verifies the bridge contract via the `mempalace` workflow job.
Argv-mismatch regressions are caught by
`internal/memory/bridge_args_test.go`.

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
  success_criteria:          # evaluated BEFORE accepting task_complete (criteria-gated)
    - kind: command_succeeds
      value: go build ./...
    - kind: command_succeeds
      value: go vet ./...
    - kind: test_passes
      value: go test ./...
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

## Current State (2026-04-17)

- **Coverage**: 73.8% total (target 80%); 15 packages above 80%
- **CI**: test + vet + build pass; lint non-blocking (golangci-lint doesn't support Go 1.26 yet)
- **DryRunClient**: `--dry-run` flag on `nxd req` and `nxd resume` simulates full pipeline without API calls
- **Controller**: disabled by default, production-ready with reprioritize/restart/cancel + 19 tests
- **Web dashboard**: DAG SVG visualization, review gates, metrics, recovery log, investigations
- **Native runtime**: criteria-gated completion with self-correction loop; agents cannot declare "done" until `go test`/`go vet`/`go build` pass in worktree
- **Cost estimation**: `CalculateLLMCost` and `CalculateCostWithTokens` wired into report builder with actual metrics data
- **Bayesian routing**: adaptive role assignment based on Beta distribution priors; persisted to `bayesian_priors.json`; wired to dispatcher and monitor
- **Security**: 7/8 vulnerabilities resolved (command injection, path traversal, input validation); SG-7 (secrets manager) deferred to Phase 2
- **Anti-hallucination**: criteria-gated completion + rejection budget (max 2 retries) + escalation; reviewer text fallback scans for rejection keywords; same-model review warning
- **Live-tested**: full end-to-end pipeline validated on `tzone85/project-x` with gemma4 — requirement → PR #25 merged in 3 minutes

### Per-Package Coverage

Above 80%: memory (99%), config (97%), metrics (97%), graph (96%), plugin (90%), llm (91%), routing (89%), criteria (88%), runtime (87%), agent (86%), codegraph (86%), scratchboard (85%), git (82%), artifact (82%), repolearn (81%), state (81%), web (81%)
Below 80%: engine (72%), update (68%), tmux (59%), dashboard (57%), cli (56%)
Remaining gap: cli/dashboard (Cobra/Bubbletea coupling), tmux (requires live sessions), engine (monitor paths needing full pipeline)

## Test Infrastructure

### LLM Test Clients
- `llm.ReplayClient` — returns pre-configured responses in sequence; used for component-level tests
- `llm.DryRunClient` — inspects system prompts to return role-appropriate canned responses (classify, investigate, plan, review, manager, supervisor); used for E2E pipeline tests and `--dry-run` CLI flag
- `llm.ErrorClient` — always returns configured error; used for error path tests
- `buildLLMClientFunc` — package-level function variable; tests override it to inject mock clients without API keys

### CLI Test Helpers (`internal/cli/testenv_test.go`)
- `setupTestEnv(t)` — creates temp dir with `nxd.yaml`, event store, and SQLite projection store
- `seedTestReq`, `seedTestStory`, `seedTestAgent`, `seedTestEscalation` — populate stores with test data
- `execCmd(t, cmd, cfgPath, args...)` — Cobra testing helper that sets config flag, captures output, and executes
- `withMockLLM(t, responses...)` — injects `ReplayClient` via `buildLLMClientFunc`
- `initTestRepo(t, dir)` — creates minimal git repo with one commit for commands that need worktrees

### Test Files
- `cli/commands_test.go` — 40+ tests: status, agents, events, escalations, pause, approve, reject, report, config, gc, metrics, logs, diff, registration, utilities
- `cli/orchestration_test.go` — 12 tests: runReq (greenfield, review, dry-run), archive, buildLLMClient providers, watch
- `engine/controller_test.go` — 19 tests: decideAction, lastProgressTime, cancelStory, resetStoryToDraft, reprioritizeStory, tick, RunLoop
- `engine/helpers_test.go` — 12 tests: stripCodeFences, truncateDiff, tierForRole, configCriteriaToRuntime, executor setters
- `llm/dryrun_test.go` — 15 tests: all response types, delay, cancellation, call tracking, model passthrough, usage, interface
- `llm/errors_test.go` — 20+ tests: all error classification functions
- `runtime/tools_test.go` — 24 tests: safePath, execReadFile, execWriteFile, execEditFile, execRunCommand, scratchboard ops, executeTool, CodingTools
- `web/server_test.go` — 29 tests: all HandleCommand actions
- `web/data_test.go` — 10 tests: BuildSnapshot, SnapshotJSON, mapStatusToBucket, intFromPayload
- `web/eventbus_test.go` — 5 tests: pub/sub, unsubscribe, slow consumer
- `web/metrics_test.go` — 3 tests: convertSummary, MemPalaceCheck
- `agent/prompts_plugin_test.go` — 12 tests: SetPluginState, plugin overrides, playbook injection, GoalPrompt branches
- `criteria/evaluator_test.go` — extended with: evalTestPasses (real Go projects), evalCoverageAbove, unknown type
- `git/conflict_test.go` — 8 tests: ConflictError, IsConflict, isConflict patterns
- `state/filestore_append_test.go` — 3 tests: After filter, OnAppend callback, empty list
- `state/sqlite_test.go` — extended with: ListRequirementsFiltered, InsertAgent, ListAgents, Archive, ListStoryDeps, DecodePayload
- `test/dryrun_test.go` — 2 tests: full planner pipeline with DryRunClient, dispatch wave ordering

## Event Types

Controller events (added 2026-04-12):
- `CONTROLLER_ANALYSIS` — emitted each tick with stories_checked and actions_taken counts
- `CONTROLLER_ACTION` — emitted per corrective action with kind (cancel/restart/reprioritize) and reason
- `CONTROLLER_STUCK_DETECTED` — emitted when a story exceeds stuck threshold, includes stuck_duration_s and escalation_tier
