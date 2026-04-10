# GitHub Actions Integration & Web Dashboard Update Design Spec

**Date:** 2026-04-10
**Status:** Approved
**Branch:** `feat/integration-dashboard`

## Overview

Two features: (1) GitHub Actions workflow that automatically implements issues labeled `nxd` by running the NXD pipeline and creating PRs, (2) full web dashboard update adding metrics, MemPalace status, review gates with action buttons, investigation reports, and recovery logs.

---

## Section 1: GitHub Actions — Issue-to-PR Automation

### Workflow

**New file:** `.github/workflows/nxd-automate.yml`

Triggered when a GitHub issue is labeled `nxd`. The workflow:

1. Checks out the repo
2. Installs Go 1.23 + Ollama + pulls `gemma4:26b`
3. Runs `nxd init` + `nxd req --review` with the issue title and body as the requirement
4. Reads the plan via `nxd status --json`
5. Approves the plan via `nxd approve`
6. Runs `nxd resume` to execute
7. Creates a branch `nxd/issue-<number>`, pushes, creates PR
8. Comments on the original issue linking the PR

### Requirements

- Self-hosted runner with GPU for Ollama inference, OR `GOOGLE_AI_API_KEY` repo secret for cloud fallback
- The workflow uses `--review` mode so plans are visible before execution
- `nxd status --json` flag needed for programmatic output

### New: `nxd status --json`

Add `--json` flag to `internal/cli/status.go` that outputs structured JSON:

```json
{
  "requirements": [
    {"id": "req-abc", "title": "...", "status": "pending_review", "story_count": 3}
  ],
  "stories": [
    {"id": "s-001", "title": "...", "status": "draft", "complexity": 3}
  ]
}
```

---

## Section 2: Web Dashboard — Full Update

### Extended StateSnapshot

The WebSocket `StateSnapshot` gains 5 new sections:

```go
type StateSnapshot struct {
    // Existing
    Agents       []state.Agent
    Stories      []state.Story
    Pipeline     PipelineCounts
    Events       []EventSummary
    Escalations  []state.Escalation
    Requirements []state.Requirement

    // NEW
    Metrics         *MetricsSummary     `json:"metrics"`
    MemPalaceStatus *MemPalaceStatus    `json:"mempalace_status"`
    ReviewGates     []ReviewGateItem    `json:"review_gates"`
    Investigations  []InvestigationItem `json:"investigations"`
    RecoveryLog     []RecoveryItem      `json:"recovery_log"`
}
```

**New types:**

```go
type MetricsSummary struct {
    TotalRequirements int                     `json:"total_requirements"`
    TotalStories      int                     `json:"total_stories"`
    TotalTokens       int                     `json:"total_tokens"`
    SuccessRate       float64                 `json:"success_rate"`
    AvgLatencyMs      int64                   `json:"avg_latency_ms"`
    EscalationCount   int                     `json:"escalation_count"`
    ByPhase           map[string]PhaseMetrics `json:"by_phase"`
}

type PhaseMetrics struct {
    Count  int `json:"count"`
    Tokens int `json:"tokens"`
}

type MemPalaceStatus struct {
    Available bool   `json:"available"`
    Wing      string `json:"wing,omitempty"`
    RoomCount int    `json:"room_count,omitempty"`
}

type ReviewGateItem struct {
    ID     string `json:"id"`
    Type   string `json:"type"`
    Title  string `json:"title"`
    Status string `json:"status"`
}

type InvestigationItem struct {
    ReqID       string `json:"req_id"`
    Summary     string `json:"summary"`
    ModuleCount int    `json:"module_count"`
    SmellCount  int    `json:"smell_count"`
    RiskCount   int    `json:"risk_count"`
}

type RecoveryItem struct {
    StoryID     string `json:"story_id"`
    Type        string `json:"type"`
    Description string `json:"description"`
    Timestamp   string `json:"timestamp"`
}
```

### New Dashboard Panels

**1. Metrics Panel** (top of page, always visible):
- Token usage with phase breakdown as colored bar segments
- Success rate percentage badge
- LLM call count, average latency, escalation count

**2. Review Gates Panel** (below pipeline, visible when items exist):
- Requirements in "pending_review" with Approve/Reject buttons
- Stories in "merge_ready" with Merge button
- Buttons send WebSocket commands

**3. MemPalace Status** (header indicator):
- Green dot "Memory: Active" or gray dot "Memory: Offline"

**4. Investigation Panel** (below requirements, existing codebases only):
- Summary, module count, smell count, risk count per requirement

**5. Recovery Log** (below escalations, visible when items exist):
- Recent STORY_RECOVERY events with type and description

### New WebSocket Command Handlers

In `handlers.go`:
- `approve_requirement` — emits REQ_PLANNED for pending_review requirement
- `reject_requirement` — emits REQ_REJECTED
- `merge_story` — triggers merge for merge_ready story

### Data Population

In `data.go` / `metrics_data.go`:
- Metrics: read `metrics.jsonl` via recorder, cached (refresh every 10s, not every 2s)
- MemPalace: check `IsAvailable()` (cached)
- Review gates: filter stories with status "merge_ready" + requirements with "pending_review"
- Investigations: query INVESTIGATION_COMPLETED events, parse report payloads
- Recovery: query STORY_RECOVERY events

---

## Section 3: Files Changed Summary

### New Files (2)

| File | Purpose |
|------|---------|
| `.github/workflows/nxd-automate.yml` | Issue-to-PR automation workflow |
| `internal/web/metrics_data.go` | Cached metrics summary builder |

### Modified Files (8)

| File | Change |
|------|--------|
| `internal/web/data.go` | Extend StateSnapshot with 5 new sections, new types |
| `internal/web/handlers.go` | Add approve_requirement, reject_requirement, merge_story commands |
| `internal/web/server.go` | Pass metrics recorder and MemPalace to data builder |
| `internal/web/static/index.html` | Add metrics panel, review gates, MemPalace indicator, investigation, recovery |
| `internal/web/static/app.js` | Render new panels, wire action buttons |
| `internal/web/static/styles.css` | Styles for new panels, badges, gauges |
| `internal/cli/status.go` | Add `--json` flag |
| `internal/cli/root.go` | No changes needed |

### Wiring Tests (3 new)

| Test | What it proves |
|------|---------------|
| StatusJSONOutput | `--json` produces valid JSON with requirements and stories |
| DashboardSnapshotIncludesMetrics | StateSnapshot contains MetricsSummary |
| DashboardReviewGatesPopulated | merge_ready stories appear in ReviewGates |
