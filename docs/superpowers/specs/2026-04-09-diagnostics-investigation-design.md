# Diagnostics, Investigation & Existing Codebase Support Design Spec

**Date:** 2026-04-09
**Status:** Approved
**Branch:** `feat/diagnostics-investigation`

## Overview

Transform NXD from a greenfield-only build system into one that can intelligently debug, refactor, and maintain existing codebases. Three layers: (1) classify repos and requirements before planning, (2) investigate existing codebases with a dedicated Investigator agent, (3) inject diagnostic playbooks into agent prompts based on classification.

### Goals

- Detect whether a repo is greenfield or existing (heuristic, no LLM)
- Classify requirements as feature/bugfix/refactor/infrastructure (one LLM call)
- Run a full codebase investigation before planning on existing repos (dedicated agent)
- Inject mandatory debugging/refactoring workflows into agent prompts
- Persist classification and investigation results for resume/monitor access
- Zero impact on greenfield workflows (all new stages skipped when IsExisting=false)

### Constraints

- Investigation is SKIPPED for greenfield repos — no added latency for new projects
- Playbooks are mandatory workflows, not suggestions — agents must follow them before coding
- Classification flags are persisted in events/projections so resume doesn't re-run investigation
- Backward compatible — existing configs and workflows unchanged

---

## Section 1: Requirement Classification

### Stage 1: Repo-State Heuristic

**New file:** `internal/engine/classifier.go`

`ClassifyRepo(repoPath string) RepoProfile` scans the target repo with zero LLM calls:

```go
type RepoProfile struct {
    IsExisting      bool
    Language        string
    BuildTool       string
    SourceFileCount int
    TestFileCount   int
    CommitCount     int
    TopDirs         []string
    HasCI           bool
    HasDocker       bool
    BuildHealthy    bool
    TestsExist      bool
}
```

**Heuristic rules:**
- `IsExisting = SourceFileCount > 5 && CommitCount > 10`
- `HasCI` = `.github/workflows/` or `.gitlab-ci.yml` exists
- `HasDocker` = `Dockerfile` or `docker-compose.yml` exists
- `BuildHealthy` = build command exits 0 (30-second timeout)
- Language/BuildTool from existing `ScanRepo()`

### Stage 2: Requirement Intent Analysis

When `IsExisting` is true, one LLM call classifies the requirement:

```go
type RequirementClassification struct {
    Type       string   // "feature", "bugfix", "refactor", "infrastructure"
    Confidence float64  // 0.0-1.0
    Signals    []string // ["mentions error message", "references stack trace"]
}
```

The LLM receives requirement text + RepoProfile summary. For greenfield repos, defaults to `Type: "feature"` with no LLM call.

### Combined Output

```go
type RequirementContext struct {
    Repo           RepoProfile
    Classification RequirementClassification
    Report         *InvestigationReport // nil for greenfield
    IsExisting     bool
    IsBugFix       bool
    IsRefactor     bool
    IsInfra        bool
}
```

---

## Section 2: Investigator Role

### New Role: `RoleInvestigator`

The 8th agent role, dedicated to understanding existing codebases before the Tech Lead plans. Runs as a hybrid agent (CLI for file reading/commands, API for synthesis). Only activated when `RepoProfile.IsExisting == true`.

### Investigation Phases

| Phase | What | How | Output |
|-------|------|-----|--------|
| 1. Orientation | Project purpose, entry points, config | Read README, CLAUDE.md, main files, .env.example | `purpose`, `entry_points[]`, `config_files[]` |
| 2. Architecture | Module structure, key abstractions, hotspots | List source files, find largest files (>200 lines), read package/module boundaries | `modules[]`, `hotspots[]`, `architecture_style` |
| 3. Health Check | Build status, test suite status, coverage | Run `go build`, `go test -cover`, lint if available | `build_passes`, `test_passes`, `test_count`, `coverage_pct` |
| 4. Dependency Graph | Internal and external dependencies | `go mod graph` / `package.json` deps, import analysis | `internal_deps[]`, `external_deps[]` |
| 5. Code Smells | Files >500 lines, missing tests, TODOs, dead code, deep nesting | Static analysis via file scanning + grep | `smells[]` with file, line, severity, description |
| 6. Risk Assessment | Areas most likely to break, untested paths, recent churn | `git log --since=30d --name-only`, cross-reference with test coverage | `risk_areas[]` with file, reason, severity |

### Output: InvestigationReport

```go
type InvestigationReport struct {
    Summary          string
    EntryPoints      []string
    Modules          []ModuleInfo
    Hotspots         []FileInfo
    BuildStatus      HealthStatus
    TestStatus       HealthStatus
    CodeSmells       []CodeSmell
    RiskAreas        []RiskArea
    InternalDeps     []DepEdge
    ExternalDeps     []ExternalDep
    Recommendations  []string
}

type ModuleInfo struct {
    Name      string
    Path      string
    FileCount int
    LineCount int
    HasTests  bool
}

type FileInfo struct {
    Path      string
    LineCount int
    Reason    string // "largest file", "most complex", "most imports"
}

type HealthStatus struct {
    Passes   bool
    Output   string
    Count    int     // test count (for TestStatus)
    Coverage float64 // coverage pct (for TestStatus)
}

type CodeSmell struct {
    File        string
    Line        int
    Severity    string // "high", "medium", "low"
    Description string
}

type RiskArea struct {
    File     string
    Reason   string
    Severity string
}

type DepEdge struct {
    From string
    To   string
}

type ExternalDep struct {
    Name    string
    Version string
    Purpose string
}
```

### Execution

The Investigator uses the native Gemma runtime (tool calling) with `read_file`, `run_command`, and `task_complete`. The execution loop is the same as for coding agents. The report is returned as the `task_complete` summary, parsed into `InvestigationReport`.

### Model Config

New entry in `ModelsConfig`:
```go
Investigator ModelConfig `yaml:"investigator"`
```

Default: `gemma4:26b`, 16000 max tokens, `google+ollama` provider.

---

## Section 3: Diagnostic Playbooks

**New file:** `internal/agent/diagnostics.go`

Four structured methodologies injected into agent system prompts based on classification flags.

### Playbook 1: CodebaseArchaeology

Injected when: `IsExisting && role == TechLead`

6-step orientation focused on planning context:
1. Read the Investigation Report
2. Identify which modules are affected by this requirement
3. Check test coverage of affected modules — untested modules need a "add test coverage" story first
4. Check build health — if build is broken, first story must fix it
5. Map file ownership carefully — existing files must not be owned by multiple stories
6. Plan stories that follow existing patterns, not introduce new ones

### Playbook 2: BugHuntingMethodology

Injected when: `IsBugFix && role in [Senior, Intermediate]`

5 mandatory phases:
1. **REPRODUCE** — Write failing test, see it fail, document exact input/expected/actual
2. **ISOLATE** — Stack trace (bottom-to-top), targeted logging, binary search, git blame
3. **UNDERSTAND ROOT CAUSE** — Answer 5 questions: expected vs actual, specific line, WHY, when introduced
4. **MINIMAL FIX** — Fix only the bug, no refactoring, failing test must pass, full suite passes
5. **VERIFY** — Race detection, edge cases, add tests for edge cases

Common bug patterns checklist: nil pointers, race conditions, off-by-one, type coercion, environment-dependent behavior, state mutation, error swallowing, zero-value fields, resource leaks.

### Playbook 3: InfrastructureDebugging

Injected when: `IsInfra`, ALL roles

Diagnostic toolkit organized by domain:
- Docker issues (ps, logs, inspect, compose config, disk, network)
- Database issues (PostgreSQL, SQLite, MySQL health checks)
- CI/CD issues (gh run list/view, secrets, dependency drift)
- Network issues (curl -v, lsof, DNS, TLS)
- Environment issues (env vars, PATH, versions, disk/memory)
- Log analysis (grep patterns, frequency, tail, journalctl)
- Common failures (port conflicts, permissions, disk full, DNS, TLS, memory, timeouts)

### Playbook 4: LegacyCodeSurvival

Injected when: `IsExisting && role in [Senior, Intermediate, Junior]`

Safe refactoring rules:
- 5 golden rules (never rewrite, make easy then change, characterization tests first, small steps, commit often)
- Working with unfamiliar code (trace entry points, grep, read tests, git blame, follow patterns)
- Safe refactoring steps in order of risk (extract → rename → remove dead code → add types → add error handling → restructure)
- What NOT to do (no directory restructuring in bug fixes, no formatting changes, no future abstractions, no unrelated bugs)
- When tests don't exist (characterization tests first, separate commit, then change, then targeted tests)

---

## Section 4: Prompt Injection Logic

### Extended PromptContext

**Modified file:** `internal/agent/prompts.go`

```go
type PromptContext struct {
    // ... existing fields ...
    IsExistingCodebase  bool
    IsBugFix            bool
    IsRefactor          bool
    IsInfrastructure    bool
    InvestigationReport *InvestigationReport
}
```

### Injection Rules in SystemPrompt()

| Flag | Role | Playbook Injected |
|------|------|-------------------|
| `IsExisting` | TechLead | CodebaseArchaeology |
| `IsExisting` | Senior | BugHuntingMethodology + LegacyCodeSurvival |
| `IsExisting` | Intermediate | LegacyCodeSurvival |
| `IsExisting` | Junior | LegacyCodeSurvival |
| `IsBugFix` (and not already existing) | Senior, Intermediate | BugHuntingMethodology |
| `IsInfra` | ALL roles | InfrastructureDebugging |

### Investigation Report Injection

When `InvestigationReport` is non-nil, formatted as markdown and appended to Tech Lead's system prompt with: summary, build/test status, modules list, code smells, risk areas.

### GoalPrompt Additions

- `IsExisting` → 7-step orient-before-coding workflow
- `IsBugFix` → 5-step reproduce-isolate-rootcause-fix-verify workflow
- `IsInfra` → 5-step check-services-logs-config-resources-fix workflow

---

## Section 5: Pipeline Integration

### Modified `nxd req` Flow

```
1. ClassifyRepo(repoPath) → RepoProfile                          [milliseconds, no LLM]
2. If IsExisting: ClassifyRequirement(ctx, client, req) → Classification  [one LLM call]
3. If IsExisting: investigator.Investigate(ctx, reqID, repoPath) → Report [2-5 min, agent]
4. Emit REQ_CLASSIFIED event with RequirementContext
5. If report: Emit INVESTIGATION_COMPLETED event with InvestigationReport
6. planner.PlanWithContext(ctx, reqID, requirement, repoPath, reqCtx)
```

For greenfield repos, steps 2-3 and 5 are skipped. The planner runs immediately.

### Event Persistence

New event types:
- `REQ_CLASSIFIED` — payload: `RequirementContext` (type, flags, repo profile)
- `INVESTIGATION_COMPLETED` — payload: serialized `InvestigationReport`

Projected into `requirements` table with new columns: `req_type`, `is_existing`, `investigation_report_json`.

### Resume Support

`nxd resume` reads the persisted flags from the projection store. No re-classification or re-investigation needed.

### Executor Flag Population

When spawning agents, the executor reads persisted flags and populates `PromptContext`:

```go
promptCtx.IsExistingCodebase = reqContext.IsExisting
promptCtx.IsBugFix           = reqContext.IsBugFix
promptCtx.IsRefactor         = reqContext.IsRefactor
promptCtx.IsInfrastructure   = reqContext.IsInfra
promptCtx.InvestigationReport = reqContext.Report
```

This triggers playbook injection in `SystemPrompt()` and mandatory workflows in `GoalPrompt()`.

---

## Section 6: Files Changed Summary

### New Files (8)

| File | Purpose |
|------|---------|
| `internal/engine/classifier.go` | `ClassifyRepo()` (heuristic) + `ClassifyRequirement()` (LLM) |
| `internal/engine/classifier_test.go` | Tests with temp repos + mock LLM |
| `internal/engine/investigator.go` | Investigator engine — 6-phase investigation, produces InvestigationReport |
| `internal/engine/investigator_test.go` | Tests with fixture repos, verify report structure |
| `internal/agent/diagnostics.go` | Four playbook constants |
| `internal/agent/diagnostics_test.go` | Tests playbooks are non-empty and contain key sections |
| `internal/agent/investigator.go` | `RoleInvestigator` definition, system prompt, investigation tool schemas |
| `internal/agent/investigator_test.go` | Tests for investigator prompts and tools |

### Modified Files (10)

| File | Change |
|------|--------|
| `internal/agent/prompts.go` | Add flags to PromptContext, conditional playbook injection, workflow steps in GoalPrompt |
| `internal/agent/roles.go` | Add `RoleInvestigator` with `ExecHybrid` mode |
| `internal/config/config.go` | Add `Investigator` to `ModelsConfig` |
| `internal/config/loader.go` | Add `Investigator` default model |
| `internal/config/config_test.go` | Test investigator config |
| `internal/cli/req.go` | Insert classification + investigation before planning |
| `internal/cli/resume.go` | Read persisted flags from projection store |
| `internal/engine/planner.go` | New `PlanWithContext()` accepting RequirementContext |
| `internal/state/events.go` | Add `EventReqClassified`, `EventInvestigationCompleted` |
| `internal/state/sqlite.go` | Add `req_type`, `is_existing`, `investigation_report_json` columns |

### Unchanged

Reviewer, supervisor, manager, QA, merger, dispatcher, monitor, watchdog — they consume flags via PromptContext but their engine logic is unchanged. Playbooks are injected at the prompt level.
