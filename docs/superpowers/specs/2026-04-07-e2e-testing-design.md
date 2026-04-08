# E2E Testing Design Spec

**Date:** 2026-04-07
**Status:** Approved
**Branch:** `feat/e2e-testing`

## Overview

Build a hybrid E2E test suite that exercises the full NXD pipeline (plan → dispatch → execute → review → QA → merge) in two modes: **replay** (deterministic, CI-safe, scripted LLM responses) and **live** (real Ollama/Gemma 4 inference against a throwaway Go project). Each test scenario is a struct describing what to test; the runner decides how based on build tags.

### Goals

- Test the full pipeline lifecycle, not just individual components
- Cover failure paths (review rejection, QA failure, escalation) not just happy paths
- Validate the Gemma 4 function calling integration produces real working code
- Run deterministic tests in CI with zero external dependencies
- Run live smoke tests locally against real Ollama when available

### Constraints

- No modifications to production code — tests only
- Throwaway fixture repos (no risk to real projects)
- Live tests skip gracefully when Ollama is unavailable
- Replay tests must be fully deterministic (no flakiness)

---

## Section 1: Test Architecture — Hybrid Runner

### Mode Selection

```bash
go test -tags e2e ./test/              # Replay mode (CI, fast, deterministic)
go test -tags live ./test/             # Live Ollama mode (requires gemma4:26b)
go test -tags "e2e live" ./test/       # Both: each dual-mode scenario runs twice
```

### Scenario Definition

```go
type Scenario struct {
    Name        string           // Human-readable name
    Requirement string           // Natural language requirement to submit
    Fixture     FixtureConfig    // Git repo template
    Assertions  []Assertion      // Structural checks (run in both modes)
    Replay      *ReplayConfig    // Canned LLM responses (nil = live only)
    LiveOnly    bool             // true = skip in replay mode
    ReplayOnly  bool             // true = skip in live mode
}

type Assertion struct {
    Phase string                 // "plan", "dispatch", "review", "qa", "merge"
    Check func(state TestState) error
}

type Mode string
const (
    ModeReplay Mode = "replay"
    ModeLive   Mode = "live"
)
```

### Runner Flow

```
For each scenario:
  1. Create throwaway git repo from scenario.Fixture
  2. Initialize NXD stores (FileStore + SQLiteStore) in temp dir
  3. Build LLM client:
     - REPLAY: ReplayClient from scenario.Replay
     - LIVE: OllamaClient targeting gemma4:26b (skip if unavailable)
  4. Build config with test-appropriate defaults
  5. Run pipeline phase by phase:
     a. Plan: Planner.Plan() with the requirement
     b. Dispatch: Dispatcher.DispatchWave() for ready stories
     c. Execute: For LIVE mode, run native Gemma runtime; for REPLAY, simulate completion
     d. Review: Reviewer.Review() on the diff
     e. QA: Compile + test the code in the worktree
     f. Merge: Verify merge-ready state
  6. After each phase: run that phase's Assertions against TestState
  7. Cleanup temp dirs via t.Cleanup
```

### Assertion Flexibility

Assertions are the same functions in both modes but with different thresholds:

| Check | Replay Mode | Live Mode |
|-------|-------------|-----------|
| Story count | `== 3` (exact) | `>= 2` (structural) |
| Story titles | Exact match | Non-empty strings |
| Complexity | `== [2, 3, 5]` | Each in range 1-13 |
| Dependencies | Exact graph | Valid story IDs, no cycles |
| Review verdict | `== "approve"` | One of approve/request_changes/reject |
| Code compiles | Yes | Yes |
| Tests pass | Yes | Yes |

---

## Section 2: Fixture Repo and Test Requirement

### Fixture Repository

Each scenario creates a fresh throwaway Go module:

```
/tmp/nxd-test-XXXXX/
├── go.mod              # module testproject
├── main.go             # package main with minimal starter code
└── .git/               # initialized repo with one commit
```

Starter `main.go`:

```go
package main

import "fmt"

func main() {
    fmt.Println("testproject")
}
```

A minimal Go module gives the model language/structure signals without constraining the implementation.

### Test Requirement (Live Suite)

> "Build a key-value store package with the following: a `store` package with `Store` struct that supports `Set(key, value string)`, `Get(key string) (string, bool)`, `Delete(key string)`, and `List() []string` (returns sorted keys). The store must be safe for concurrent access. Add an HTTP API in `main.go` with endpoints `POST /kv/{key}` (body is the value), `GET /kv/{key}`, `DELETE /kv/{key}`, and `GET /kv` (list all keys). Include unit tests for the store package and integration tests for the HTTP endpoints."

This requirement is meaningful because:
- **Planner must decompose** into 2-3+ stories with real dependencies (store package → HTTP API → tests)
- **Code must compile** — requires `sync.RWMutex`, proper imports, `net/http` handlers
- **Tests must pass** — generated tests actually exercise the store and HTTP API
- **Reviewer has substance** — concurrency safety, error handling, API design

For replay mode, the same requirement is used but with canned LLM responses that produce known-good code.

---

## Section 3: Scenario Catalog

### 8 Scenarios

| # | Name | Tests | Replay | Live |
|---|------|-------|--------|------|
| 1 | **Happy path (multi-story)** | Plan → dispatch → execute → review → QA → merge with 3 stories and dependencies | Yes | Yes |
| 2 | **Review failure + retry** | Story fails review, gets feedback, re-executes, passes on second attempt | Yes | No |
| 3 | **QA failure + escalation** | Junior story fails QA (build error), escalates to senior tier, senior fixes | Yes | No |
| 4 | **Pause and resume** | Submit requirement, pause mid-pipeline after wave 1, resume, verify wave 2 dispatches | Yes | No |
| 5 | **Diamond dependency chain** | 4 stories: s-1 ← (s-2, s-3) ← s-4. Verify waves [s-1], [s-2, s-3], [s-4] | Yes | No |
| 6 | **Function calling round-trip** | Planner uses `create_story` tool calls, reviewer uses `submit_review` tool, validate structured JSON output | Yes | Yes |
| 7 | **Fallback client (quota exhaustion)** | Google AI returns 429 on first call, fallback to Ollama transparently, pipeline continues | Yes | No |
| 8 | **Live full round-trip** | Real requirement → Gemma 4 → real code → compiles → tests pass → review completes | No | Yes |

### Rationale

**Scenarios 2-5, 7 are replay-only:** They test specific failure/edge paths requiring precise control of LLM responses. You cannot reliably make a live model fail a review on the first attempt but pass on the second.

**Scenario 8 is live-only:** The "smoke test against reality" — no scripts, verify the real pipeline doesn't crash and produces meaningful output.

**Scenarios 1 and 6 run in both modes:** The happy path and function calling should work deterministically AND against real Ollama. Replay uses exact assertions; live uses structural validation.

### Scenario Details

#### Scenario 1: Happy Path (Multi-Story)

Requirement: Key-value store (see Section 2).

Replay config: 3 canned responses:
1. Tech Lead: creates s-001 (store package), s-002 (HTTP API, depends on s-001), s-003 (tests, depends on s-001 and s-002). Waves: [[s-001], [s-002], [s-003]].
2. Code execution: pre-written Go code for each story that compiles and passes tests.
3. Reviewer: approves all three stories.

Assertions:
- Plan phase: 3 stories created, dependency graph is acyclic, waves are [[s-001], [s-002], [s-003]]
- Dispatch phase: stories assigned to correct tiers by complexity
- Review phase: all reviews pass
- QA phase: `go build` and `go test` succeed in worktree
- Merge phase: all stories reach "merged" status
- Events: REQ_SUBMITTED, STORY_CREATED (x3), REQ_PLANNED, STORY_ASSIGNED (x3), STORY_REVIEW_PASSED (x3), STORY_QA_PASSED (x3)

#### Scenario 2: Review Failure + Retry

Replay config: Reviewer rejects first story with specific feedback ("missing error handling in Get"), then approves on second review after code is updated.

Assertions:
- STORY_REVIEW_FAILED event emitted with feedback
- Story status transitions: assigned → review → assigned (retry) → review → qa
- Feedback from first review is included in the agent's retry prompt
- Second review passes

#### Scenario 3: QA Failure + Escalation

Replay config: Junior agent produces code that fails `go build`. Escalation triggers senior agent. Senior produces working code.

Assertions:
- STORY_QA_FAILED event emitted
- STORY_ESCALATED event emitted with escalation tier
- Story reassigned from junior to senior role
- Senior's code passes QA

#### Scenario 4: Pause and Resume

Replay config: 2 waves. After wave 1 completes, pipeline is paused. Then resumed.

Assertions:
- REQ_PAUSED event emitted
- Wave 2 stories remain in "draft" status while paused
- REQ_RESUMED event emitted
- Wave 2 dispatches correctly after resume
- All stories eventually reach "merged"

#### Scenario 5: Diamond Dependency Chain

Replay config: 4 stories with diamond dependency: s-1 → s-2, s-1 → s-3, s-2 → s-4, s-3 → s-4.

Assertions:
- Wave 1: [s-1] only
- Wave 2: [s-2, s-3] (parallel, both depend on s-1)
- Wave 3: [s-4] (depends on both s-2 and s-3)
- No story dispatched before its dependencies are complete

#### Scenario 6: Function Calling Round-Trip

Assertions (both modes):
- Planner response contains ToolCalls (not just text content)
- ToolCalls include `create_story` calls with valid JSON arguments
- `set_wave_plan` tool call present
- Reviewer response contains `submit_review` tool call with verdict field
- In replay mode: exact tool call names and argument shapes
- In live mode: structural validation (tool calls present, valid JSON, correct tool names)

#### Scenario 7: Fallback Client (Quota Exhaustion)

Setup: Build FallbackClient with a mock GoogleClient that returns QuotaError on first call.

Assertions:
- First LLM call triggers fallback to Ollama
- Pipeline continues without error
- Subsequent calls go directly to fallback (skip primary)
- After cooldown: primary is retried
- No pipeline interruption visible to the caller

#### Scenario 8: Live Full Round-Trip

Requirement: Key-value store (same as scenario 1).

Assertions (structural only):
- At least 2 stories created
- All complexity scores in range 1-13
- Dependencies reference valid story IDs
- No dependency cycles
- Code in worktree compiles: `go build ./...` exits 0
- Tests pass: `go test ./...` exits 0
- Review completes with a valid verdict
- All stories reach a terminal status

---

## Section 4: Test Helpers and Infrastructure

### Shared Helpers

```go
// Fixture repo lifecycle
CreateFixtureRepo(t *testing.T, cfg FixtureConfig) string
    → temp dir, git init, go mod init, write files, initial commit
    → auto-cleaned via t.Cleanup, returns repo path

// NXD store initialization
CreateTestStores(t *testing.T) (state.EventStore, *state.SQLiteStore, string)
    → temp dir with FileStore + SQLiteStore
    → auto-cleaned, returns stores + state dir

// Config builder with test defaults
TestConfig(stateDir string, opts ...ConfigOption) config.Config
    → ConfigOption: WithProvider, WithModel, WithRuntime, WithMergeMode

// Pipeline runner
RunScenario(t *testing.T, scenario Scenario, mode Mode)
    → orchestrates the full test lifecycle per scenario

// Ollama detection
RequireOllama(t *testing.T)
    → checks localhost:11434, checks gemma4:26b is pulled
    → t.Skip() if unavailable (not t.Fatal)
```

### TestState

Captured after each pipeline phase for assertions:

```go
type TestState struct {
    Events       []state.Event
    Requirements []state.Requirement
    Stories      []state.Story
    RepoPath     string
    StoreDir     string
    Config       config.Config
    Mode         Mode
}
```

### Assertion Functions

```go
AssertStoriesCreated(min, max int) Assertion
AssertComplexityRange(low, high int) Assertion
AssertDependenciesValid() Assertion
AssertNoCycles() Assertion
AssertWaveOrder(expected [][]string) Assertion          // replay: exact; live: skip
AssertReviewCompleted(validVerdicts ...string) Assertion
AssertCodeCompiles(repoPath string) Assertion
AssertTestsPass(repoPath string) Assertion
AssertEventsEmitted(types ...state.EventType) Assertion
AssertStoryStatuses(expected map[string]string) Assertion
AssertToolCallsUsed(toolNames ...string) Assertion      // function calling verification
AssertNoErrors() Assertion                               // no panics, no unhandled errors
```

### File Organization

```
test/
├── scenario_test.go        # Scenario definitions + table-driven runner entry point
├── scenarios_replay.go     # ReplayConfig with canned LLM responses per scenario
├── scenarios_live.go       # Live-specific config + RequireOllama
├── helpers_test.go         # CreateFixtureRepo, CreateTestStores, TestConfig
├── assertions_test.go      # All assertion functions
├── runner_test.go          # RunScenario engine + per-phase execution logic
└── testdata/
    └── fixture/
        └── main.go         # Template starter file for fixture repos
```

---

## Files Changed Summary

### New Files (7)

| File | Purpose |
|------|---------|
| `test/scenario_test.go` | 8 scenario definitions + table-driven test entry |
| `test/scenarios_replay.go` | Canned LLM responses for deterministic scenarios |
| `test/scenarios_live.go` | Live Ollama config + model detection |
| `test/helpers_test.go` | Fixture repo, store creation, config builder |
| `test/assertions_test.go` | Assertion functions for both modes |
| `test/runner_test.go` | RunScenario engine with phase-by-phase execution |
| `test/testdata/fixture/main.go` | Template Go file for fixture repos |

### No Modified Files

This is a pure addition — no production code changes.
