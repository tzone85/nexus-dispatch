# MemPalace Integration, Context Sharing, QA Feedback, Observability & Agent Intelligence Design Spec

**Date:** 2026-04-10
**Status:** Approved
**Branch:** `feat/mempalace-context-observability`

## Overview

Eight features in one cohesive design that transform NXD from isolated-agent execution to a context-aware, observable, self-improving system. MemPalace provides semantic memory. Context sharing lets stories see what previous stories built. QA feedback loops give agents error output to fix. Metrics track everything. Smarter retry, parallel awareness, and template detection make agents more intelligent.

### Features

| # | Feature | Key Benefit |
|---|---------|-------------|
| M | MemPalace integration | Persistent semantic memory across sessions |
| 1 | Agent context sharing | Stories see what previous stories built |
| 2 | QA feedback loop | Agents fix their own errors before escalation |
| 4 | Progressive context (via MemPalace) | Solved by M+1 — memory grows as stories complete |
| 5 | Observability & metrics | Token usage, timing, success rates, cost tracking |
| 6 | Smarter retry | Pattern-match errors before burning escalation tiers |
| 7 | `nxd watch` live progress | Real-time event streaming in terminal |
| 8 | Parallel story awareness | Stories in same wave know about each other |
| 9 | Template/skeleton detection | Agents follow existing codebase conventions |

### Constraints

- MemPalace is optional — NXD works without it (graceful degradation)
- Metrics are append-only JSONL (same pattern as events)
- No new external dependencies beyond MemPalace (Python, pip)
- All new PromptContext fields are empty strings when unused (backward compatible)

---

## Section 1: MemPalace Bridge

### Python Bridge Script

**New file:** `scripts/mempalace_bridge.py`

Thin wrapper that calls MemPalace's Python API and returns structured JSON. NXD calls it via subprocess.

Commands:

| Command | Purpose |
|---------|---------|
| `search --query Q --wing W --room R --results N` | Semantic search within a wing/room |
| `mine --wing W --room R --text T` | Store text into a specific wing/room |
| `mine-meta --text T` | Store cross-project learning into `nxd_meta` wing |
| `wake-up --wing W` | Get L0+L1 context (~170 tokens) |
| `health` | Check if MemPalace is installed and working |

JSON output format:

```json
{"status": "ok", "results": [{"text": "...", "wing": "...", "room": "...", "similarity": 0.92}]}
```

Error: `{"status": "error", "message": "mempalace not installed"}`

### Go Client

**New package:** `internal/memory/`

```go
type MemPalace struct {
    bridgePath string
    available  bool
}

func NewMemPalace() *MemPalace
func (m *MemPalace) IsAvailable() bool
func (m *MemPalace) Search(query, wing, room string, maxResults int) ([]SearchResult, error)
func (m *MemPalace) Mine(wing, room, text string) error
func (m *MemPalace) MineMeta(text string) error
func (m *MemPalace) WakeUp(wing string) (string, error)

type SearchResult struct {
    Text       string  `json:"text"`
    Wing       string  `json:"wing"`
    Room       string  `json:"room"`
    Similarity float64 `json:"similarity"`
}
```

**Graceful degradation:** If MemPalace is not installed, `IsAvailable()` returns false. All other methods return empty results without errors. NXD continues to work — memory is a bonus, not a dependency.

### Wing Structure

- **One wing per repo** (e.g., `wing_myapp`) — project-specific context (story diffs, review feedback, QA failures)
- **Shared `nxd_meta` wing** — cross-project learnings (decomposition patterns, common fixes, user preferences)
- **Rooms per requirement** (e.g., `room_req-abc123`) — scoped context within a project

### Config

```yaml
memory:
  enabled: true           # set false to disable MemPalace entirely
  palace_path: ""         # empty = default (~/.mempalace/palace)
```

---

## Section 2: Agent Context Sharing

### Three Integration Points

**Point 1: After story completion → mine the diff**

In `internal/engine/monitor.go`, after a story reaches "completed" status:

1. Capture git diff: `git diff main...<story-branch>`
2. Build summary: "Story {id} ({title}) completed. Files changed: {files}. Diff summary: {truncated_diff}"
3. Call `mempalace.Mine(wing=repoName, room=reqID, text=summary)`

**Point 2: Before story execution → query context**

In `internal/engine/executor.go`, when building PromptContext:

1. Query `mempalace.Search(query=storyTitle+" "+storyDescription, wing=repoName, room=reqID, results=5)`
2. Format results as "## Prior Work in This Requirement" section
3. Inject into `PromptContext.PriorWorkContext`

**Point 3: After review/completion → mine learnings**

- Review feedback mined: "Review of s-001: Approved. Good RWMutex usage."
- After requirement completes, mine meta learning: "Go project: 3 stories, all passed first review. Pattern: store → API → tests."

### New PromptContext Field

```go
PriorWorkContext string // formatted MemPalace search results
```

Injected into `GoalPrompt()`:

```
## Prior Work in This Requirement

The following stories have already been completed:
- s-001 "Build store package": Created store/store.go with Get, Set, Delete, List using sync.RWMutex
- s-002 "Add HTTP API": Created main.go with POST/GET/DELETE /kv/{key} endpoints

Build on this existing work. Do not recreate files that already exist.
```

---

## Section 3: QA Feedback Loop

### Modified Pipeline Flow

```
Current:  QA fails → emit QA_FAILED → escalate tier → new agent starts blind
New:      QA fails → capture error output → retry SAME agent with error → escalate only if retry fails
```

### Three Changes

**Change 1:** QA failure event payload includes output:

```json
{"story_id": "s-001", "qa_output": "go build: undefined: NewStore", "failed_checks": ["build"]}
```

**Change 2:** Monitor retries with QA context before escalating:

1. QA fails → capture qa_output from QAResult
2. If retry_count < max_retries: build retry prompt with error, re-dispatch to SAME tier
3. If retry_count >= max_retries: escalate as before

**Change 3:** Agent receives QA errors via existing `ReviewFeedback` field:

```go
promptCtx.ReviewFeedback = fmt.Sprintf(
    "QA FAILURE — fix this error:\n\n%s\n\nMake the minimal change to fix this. Do not rewrite files.",
    qaOutput,
)
```

No new PromptContext field needed — `ReviewFeedback` already serves this purpose.

QA failures also mined into MemPalace for future learning.

---

## Section 4: Observability & Metrics

### Metric Recording

**New package:** `internal/metrics/`

```go
type MetricEntry struct {
    Timestamp   time.Time `json:"timestamp"`
    ReqID       string    `json:"req_id"`
    StoryID     string    `json:"story_id,omitempty"`
    Phase       string    `json:"phase"`
    Role        string    `json:"role,omitempty"`
    Model       string    `json:"model"`
    TokensIn    int       `json:"tokens_in"`
    TokensOut   int       `json:"tokens_out"`
    DurationMs  int64     `json:"duration_ms"`
    Success     bool      `json:"success"`
    Escalated   bool      `json:"escalated"`
}
```

Persisted to `~/.nxd/metrics.jsonl` (append-only).

### MetricsClient Wrapper

```go
type MetricsClient struct {
    inner    llm.Client
    recorder *Recorder
    reqID    string
    phase    string
}
```

Wraps any `llm.Client`, records every `Complete()` call's tokens, duration, and success. Applied in `runReq()` and `runResume()`.

### `nxd metrics` Command

Reads `metrics.jsonl`, aggregates, and prints:

```
Requirements: 12 total (10 completed, 1 in-progress, 1 paused)
Stories:      47 total (42 merged, 3 in-progress, 2 failed)

Success rate:    89% first-pass (no escalation)
Avg stories/req: 3.9
Avg time/story:  8m 32s

Token usage (last 30 days):
  Planning:     145K tokens
  Investigation: 89K tokens
  Execution:    312K tokens
  Review:        67K tokens
  Total:        613K tokens
```

`nxd metrics --json` for scripting.

### `nxd watch` Command

Polls event store every 500ms, prints new events:

```
[10:42:01] REQ_SUBMITTED req-abc "Build REST API for user management"
[10:42:18] STORY_CREATED s-001 "User model" (complexity: 3)
[10:42:20] STORY_ASSIGNED s-001 → agent-junior-1 (wave 1)
[10:43:55] STORY_COMPLETED s-001
[10:44:02] STORY_REVIEW_PASSED s-001
[10:44:06] STORY_MERGED s-001
```

`Ctrl+C` to exit.

---

## Section 5: Smarter Retry, Parallel Awareness, Template Detection

### Smarter Retry (#6)

**New file:** `internal/engine/failure_analyzer.go`

Pattern-matching on error output before escalating:

```go
func AnalyzeFailure(qaOutput, reviewFeedback string) string
```

Returns a targeted hint based on ~15 common patterns:
- `"undefined:"` → "Build error: undefined symbol. Check imports and exported names."
- `"cannot find package"` → "Missing dependency. Run go mod tidy."
- `"test.*FAIL"` → "Test failure. Read the test output and fix the failing assertion."
- etc.

The hint replaces raw error output in the retry prompt — more actionable.

### Parallel Story Awareness (#8)

**New file:** `internal/engine/wave_brief.go`

Before wave dispatch, build a summary of all stories in the wave:

```go
type WaveBrief struct {
    WaveNumber int
    Stories    []WaveStoryInfo
}

type WaveStoryInfo struct {
    ID          string
    Title       string
    OwnedFiles  []string
}
```

**New PromptContext field:** `WaveBrief string`

Injected into GoalPrompt:

```
## Parallel Stories in This Wave

You are working in parallel with these other agents. Do NOT modify their files.

- s-002 "CRUD API endpoints" — owns: main.go, handlers/user.go
- s-003 "Integration tests" — owns: test/api_test.go

Your owned files: store/store.go, store/store_test.go
```

### Template/Skeleton Detection (#9)

**Enhancement to Investigator** — add Phase 7:

```
Phase 7: CONVENTION DETECTION
- Read 3 existing handler files → extract pattern
- Read 3 existing test files → extract testing style
- Detect frameworks (Gin, Echo, Chi, Express, Django)
- Produce: conventions[] with {area, pattern, example_file}
```

**New field on InvestigationReport:**

```go
type Convention struct {
    Area        string `json:"area"`
    Pattern     string `json:"pattern"`
    ExampleFile string `json:"example_file"`
}
```

Injected into agent prompts:

```
## Codebase Conventions (follow these)

- Handlers: use Chi router, return JSON with status wrapper. See: internal/handler/user.go
- Tests: table-driven with testify/assert. See: internal/handler/user_test.go
```

---

## Section 6: Files Changed Summary

### New Files (12)

| File | Purpose |
|------|---------|
| `scripts/mempalace_bridge.py` | Python bridge returning JSON |
| `internal/memory/mempalace.go` | Go client: Search, Mine, WakeUp, IsAvailable |
| `internal/memory/mempalace_test.go` | Tests with mock subprocess |
| `internal/metrics/recorder.go` | MetricEntry type, Recorder (append to JSONL) |
| `internal/metrics/reporter.go` | Aggregation: success rates, token totals |
| `internal/metrics/client.go` | MetricsClient wrapper for llm.Client |
| `internal/metrics/recorder_test.go` | Write/read, aggregation tests |
| `internal/cli/metrics.go` | `nxd metrics` command |
| `internal/cli/watch.go` | `nxd watch` command |
| `internal/engine/failure_analyzer.go` | Pattern-matching error analysis |
| `internal/engine/failure_analyzer_test.go` | Common error pattern tests |
| `internal/engine/wave_brief.go` | BuildWaveBrief for parallel awareness |

### Modified Files (12)

| File | Change |
|------|--------|
| `internal/engine/monitor.go` | Mine diffs to MemPalace, QA feedback retry, smarter retry |
| `internal/engine/executor.go` | Query MemPalace for prior work, inject wave brief |
| `internal/engine/investigator.go` | Phase 7: Convention Detection |
| `internal/engine/investigator_test.go` | Test conventions in report |
| `internal/agent/prompts.go` | Add PriorWorkContext, WaveBrief to PromptContext |
| `internal/agent/investigator.go` | Update system prompt + submit_report schema with conventions |
| `internal/cli/root.go` | Register metrics and watch commands |
| `internal/cli/req.go` | Wrap client with MetricsClient, initialize MemPalace |
| `internal/cli/resume.go` | Wrap client with MetricsClient |
| `internal/state/events.go` | Add EventStoryQAFailed if missing |
| `internal/config/config.go` | Add MemPalace config section |
| `internal/config/loader.go` | Add MemPalace defaults |

### Wiring Tests (7 new)

| Test | What it proves |
|------|---------------|
| MemPalaceSearchFlowsToPrompt | Search results appear in PriorWorkContext |
| QAFeedbackReachesAgent | QA error flows into retry prompt |
| WaveBriefInjected | Parallel stories see each other |
| ConventionsDetected | Investigator report includes conventions |
| MetricsRecorded | LLM calls produce metric entries |
| FailureAnalyzerPatterns | Error patterns produce targeted hints |
| MemPalaceGracefulDegradation | Unavailable → empty results, no errors |
