# GitHub Actions Integration & Web Dashboard Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add issue-to-PR GitHub Actions automation and update the web dashboard with metrics, MemPalace status, review gates, investigation reports, and recovery logs.

**Architecture:** GitHub Actions workflow triggers on `nxd` label, runs the NXD pipeline, creates PRs. Dashboard extends the existing WebSocket StateSnapshot with 5 new data sections, adds frontend panels with action buttons, and a cached metrics data builder. All dynamic values pass through the existing `esc()` XSS escaping function.

**Tech Stack:** Go 1.23+, WebSocket (gorilla/websocket), embedded static files, GitHub Actions YAML, existing metrics/memory packages

**Spec:** `docs/superpowers/specs/2026-04-10-integration-dashboard-design.md`

---

## Task Decomposition

| Task | What | Files |
|------|------|-------|
| 1 | `nxd status --json` | status.go |
| 2 | Dashboard data types + metrics cache | metrics_data.go, data.go, server.go, dashboard.go |
| 3 | Dashboard command handlers | handlers.go |
| 4 | HTML structure updates | index.html |
| 5 | JavaScript rendering | app.js |
| 6 | CSS styles | styles.css |
| 7 | GitHub Actions workflow | nxd-automate.yml |
| 8 | Wiring tests + verification | wiring_test.go |

Each task has complete code and exact file paths in the spec. Implementer agents should read the referenced files before modifying. The dashboard follows existing patterns: StateSnapshot pushed via WebSocket every 2s, all user-facing strings escaped via `esc()`, commands sent as WebSocket messages.

**Key implementation notes for agents:**
- The web dashboard uses embedded static files via Go's `embed` package
- All dynamic content in app.js MUST use the existing `esc()` function for XSS safety
- The existing codebase uses innerHTML with esc() for all dynamic rendering — follow the same pattern
- MetricsCache refreshes every 10 seconds (not 2) to avoid reading JSONL on every snapshot
- Server constructor signature changes — update the call site in dashboard.go
- New panels (review gates, investigations, recovery) use `style="display:none"` and are shown only when data exists

**Status JSON output format:**
```json
{"requirements": [{"id": "...", "title": "...", "status": "...", "story_count": 3, "stories": [...]}]}
```

**New StateSnapshot fields:**
- `metrics` (*MetricsSummary) — cached token/call/success data
- `mempalace_status` (*MemPalaceStatus) — available bool
- `review_gates` ([]ReviewGateItem) — pending_review requirements + merge_ready stories
- `investigations` ([]InvestigationItem) — from INVESTIGATION_COMPLETED events
- `recovery_log` ([]RecoveryItem) — from STORY_RECOVERY events

**New WebSocket commands:**
- `approve_requirement` {req_id} — emits REQ_PLANNED
- `reject_requirement` {req_id} — emits REQ_REJECTED
- `merge_story` {story_id} — emits STORY_MERGED

**GitHub Actions workflow:** Triggers on `nxd` label, builds NXD, installs Ollama, runs `nxd req --review`, approves, resumes, creates branch + PR, comments on issue.
