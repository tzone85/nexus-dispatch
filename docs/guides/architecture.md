# NXD Architecture Deep Dive

This document explains how NXD works internally — the event-sourced pipeline, agent hierarchy, wave dispatch, and monitoring systems.

## Core Design Principles

1. **Event sourcing** — Every state change is an append-only event. The event log is the source of truth.
2. **Dependency injection** — All components use interfaces, enabling local/cloud swapping and testing.
3. **Wave parallelism** — Stories execute in dependency-resolved waves for maximum throughput.
4. **Offline-first** — Every component works without network access by default.

## System Architecture

```
                    nxd req "..."
                         |
                    [CLI Layer]
                    (Cobra commands)
                         |
            +------------+------------+
            |                         |
       [Event Store]          [Projection Store]
       (events.jsonl)         (SQLite nxd.db)
            |                         |
            +--------+--------+-------+
                     |
              [Orchestrator Engine]
              |    |    |    |    |    |    |    |
           Plan  Disp Watch Super Rev  QA  Merge Reap
              |    |    |    |    |    |    |    |
         [Agent Runtime Layer]
         (tmux + Aider/Claude/Codex)
              |
         [Infrastructure]
         (git worktrees, branches)
```

## Event Sourcing Model

### Events Are Immutable Facts

Every action in NXD produces an event. Events are never modified or deleted — only appended.

```
Event {
    ID:        "01HZ..."          // ULID (time-sortable, unique)
    Type:      "STORY_CREATED"    // One of 31 event types
    Timestamp: 2026-03-10T...     // UTC
    AgentID:   "tech_lead-req1-1" // Which agent produced this
    StoryID:   "story-01"         // Related story (if any)
    Payload:   {...}              // JSON with event-specific data
}
```

### Event Categories

| Category | Events | Producer |
|----------|--------|----------|
| Requirement | REQ_SUBMITTED, REQ_ANALYZED, REQ_PLANNED, REQ_COMPLETED | CLI, Planner |
| Story | STORY_CREATED through STORY_MERGED (14 types) | Planner, Dispatcher, Reviewer, QA, Merger |
| Agent | AGENT_SPAWNED, AGENT_CHECKPOINT, AGENT_RESUMED, AGENT_STUCK, AGENT_TERMINATED | Dispatcher, Watchdog |
| Escalation | ESCALATION_CREATED, ESCALATION_RESOLVED | Watchdog, Supervisor |
| Supervisor | SUPERVISOR_CHECK, SUPERVISOR_REPRIORITIZE, SUPERVISOR_DRIFT_DETECTED | Supervisor |
| Cleanup | WORKTREE_PRUNED, BRANCH_DELETED, GC_COMPLETED | Reaper |

### Projections

Events are materialized into queryable SQL tables via the ProjectionStore:

```
events.jsonl (append-only)
    |
    | Project(event)
    v
SQLite tables:
    requirements (id, title, status, ...)
    stories      (id, req_id, complexity, status, agent_id, branch, ...)
    agents       (id, type, model, status, session_name, ...)
    escalations  (id, story_id, from_role, to_role, reason, ...)
    story_deps   (story_id, depends_on)
    agent_scores (agent_id, quality, reliability, speed, ...)
```

**Why both?** The event log is the authoritative history (append-only, auditable, replayable). SQLite projections are derived views optimized for queries (list stories by status, find agents by role). If projections get corrupted, they can be rebuilt by replaying all events.

## Agent Hierarchy

NXD models a complete agile development team:

```
        Tech Lead (Opus-class model)
        Decomposes requirements into stories
              |
     +--------+--------+
     |                  |
   Senior            Supervisor
   (Sonnet-class)    (Sonnet-class)
   Reviews code,     Periodic drift
   handles 6+        detection,
   complexity        reprioritization
     |
     +--------+--------+
     |                  |
  Intermediate       Junior
  (14B model)        (7B model)
  Handles 4-5        Handles 1-3
  complexity         complexity
              |
              QA
              (14B model)
              Lint, build, test
```

### Execution Modes

| Role | Mode | How It Runs |
|------|------|-------------|
| Tech Lead | API | Direct LLM call, returns structured JSON |
| Senior | API (review) / CLI (complex tasks) | LLM for review, tmux session for implementation |
| Intermediate | CLI | tmux session with Aider in a git worktree |
| Junior | CLI | tmux session with Aider in a git worktree |
| QA | Hybrid | LLM analysis + shell commands (lint/build/test) |
| Supervisor | API | Periodic LLM call to assess progress |

### Complexity Routing (Fibonacci)

```
Complexity 1-3:  -> Junior      (qwen2.5-coder:7b)
Complexity 4-5:  -> Intermediate (qwen2.5-coder:14b)
Complexity 6-8:  -> Senior      (qwen2.5-coder:32b)
Complexity 9-13: -> Senior decomposes further, then assigns
```

Thresholds are configurable via `routing.junior_max_complexity` and `routing.intermediate_max_complexity`.

### Escalation Flow

```
Junior stuck (2 retries)
    -> Senior takes over
        -> Senior stuck
            -> Tech Lead re-plans
                -> Human intervention (if all else fails)
```

## Wave-Based Dispatch

Stories aren't executed sequentially — they run in parallel waves resolved by topological sort.

### Example

Given stories with dependencies:
```
A (no deps) ----+
                |---> D (depends on A, B)
B (no deps) ----+
                |---> E (depends on B, C)
C (no deps) ----+
```

NXD computes waves using Kahn's algorithm:
```
Wave 1: [A, B, C]  <- all independent, run in parallel
Wave 2: [D, E]     <- dependencies satisfied, run in parallel
```

Each wave waits for the previous wave to complete (all stories reviewed + merged) before dispatching.

### Dependency Graph

The DAG (Directed Acyclic Graph) is built during planning:

```go
graph.AddNode("story-01")
graph.AddNode("story-02")
graph.AddEdge("story-02", "story-01")  // story-02 depends on story-01
```

`ReadyNodes(completed)` returns nodes whose dependencies are all in the `completed` set.

Cycle detection is built-in — if the Tech Lead creates circular dependencies, the Planner rejects the plan.

## Dashboard

### TUI (Bubbletea)

The TUI is a single-pane interface that renders all sections simultaneously — no tabs or panel switching. Sections rendered top to bottom:

1. **Agents** — active agents with role, model, and current story
2. **Pipeline summary bar** — per-status story counts with a progress indicator
3. **Stories table** — all stories with status; scrollable with `j`/`k`
4. **Activity log** — last N events in real-time
5. **Escalations** — collapsible; shows pending and resolved escalations

The TUI reads from the SQLite projection store and refreshes every 2 seconds.

### Web Dashboard

`nxd dashboard --web` starts an embedded HTTP server (default port 8787). A WebSocket hub broadcasts projection snapshots to all connected browsers every 2 seconds.

```
nxd dashboard --web
  |
  +-> HTTP server (port 8787)
  |     GET /        -> embedded HTML/CSS/JS (no external dependencies)
  |     GET /ws      -> WebSocket upgrade
  |
  +-> WebSocket hub
        every 2s: read nxd.db -> marshal snapshot -> broadcast to all clients
```

The web dashboard provides a full control panel:

| Action | Target |
|--------|--------|
| Pause / Resume | Requirement |
| Retry / Reassign / Escalate | Story |
| Kill | Agent |
| Edit | Story details |

Destructive actions (kill, reassign, edit) require a confirmation dialog. Command results are shown as toast notifications. The client reconnects automatically on disconnect.

## Monitoring Systems

### Watchdog (Deterministic)

Runs every `poll_interval_ms` (default 10s). For each active tmux session:

1. **Read** last 30 lines of pane output
2. **Detect** status via regex matching:
   - `idle_pattern` -> Agent is done/waiting
   - `permission_pattern` -> Auto-approve with "Y"
   - `plan_mode_pattern` -> Send Escape to exit
3. **Fingerprint** the output (SHA-256 hash)
4. **Compare** with previous fingerprint
5. If unchanged for `stuck_threshold_s` -> Flag as STUCK

No LLM calls. Purely deterministic. Runs as a Go goroutine.

### Supervisor (LLM-Based)

Runs periodically (configurable). Sends a structured prompt to the Supervisor model:

```
"Review the progress of this requirement:
 Requirement: <original text>
 Stories and their status:
 - story-01: Add User model (complexity: 2, status: merged)
 - story-02: JWT utility (complexity: 3, status: in_progress)
 ...
 Assess whether stories are on track."
```

Returns: `{on_track: bool, concerns: [...], reprioritize: [...]}`

If drift is detected, emits `SUPERVISOR_DRIFT_DETECTED` event and can trigger reprioritization.

## Code Review Pipeline

When a story's implementation is complete:

1. **Diff extraction** — `git diff main...<branch>` captures all changes
2. **Senior review** — Diff sent to Senior LLM with acceptance criteria
3. **Structured response** — Pass/fail with file-level comments and severity ratings
4. **Event emission** — `STORY_REVIEW_PASSED` or `STORY_REVIEW_FAILED`

Review comments include:
```json
{
  "file": "internal/auth/jwt.go",
  "line": 42,
  "severity": "major",
  "comment": "Token expiry should be configurable, not hardcoded"
}
```

If review fails, the story loops back to the implementing agent with feedback.

## QA Pipeline

After review passes, QA runs three checks in sequence:

```
1. LINT   -> golangci-lint run ./...  (or project-specific linter)
2. BUILD  -> go build ./...           (or project-specific build)
3. TEST   -> go test ./...            (or project-specific test)
```

All three must pass. If any fails:
- `STORY_QA_FAILED` event emitted with details of which check failed
- Story loops back for fixes
- After `max_qa_failures_before_escalation` failures, escalates

## Merge Strategy

### Local Mode (Default, Offline)

```
1. git checkout main
2. git merge --no-ff nxd/story-01 -m "Merge nxd/story-01 into main"
3. Emit STORY_PR_CREATED (pr_url: "local://merged")
4. Emit STORY_MERGED
```

If conflicts: merge aborted, conflict file list returned, story escalated.

### GitHub Mode

```
1. git push origin nxd/story-01
2. gh pr create --title "[NXD] Story title" --body "..."
3. gh pr merge <number> (if auto_merge enabled)
4. Emit STORY_PR_CREATED, STORY_MERGED
```

## Cleanup (Reaper)

After merge, the Reaper performs tiered cleanup:

| Phase | When | What |
|-------|------|------|
| Worktree prune | Immediately after merge | Delete `~/.nxd/worktrees/nxd-req-role-n/` |
| Log archive | Immediately | Archive tmux session logs to `~/.nxd/logs/` |
| Branch GC | On `nxd gc` | Delete `nxd/*` branches older than retention period |

`nxd gc --dry-run` previews without deleting. Retention days are configurable.

## Reputation Scoring

Each agent builds a reputation score across assignments:

```
Overall = (Quality * 0.50) + (Reliability * 0.30) + (Speed * 0.20)
```

| Metric | What It Measures | Range |
|--------|-----------------|-------|
| Quality | Review pass rate, QA pass rate | 0.0 - 1.0 |
| Reliability | Task completion rate, escalation frequency | 0.0 - 1.0 |
| Speed | Relative to expected duration for complexity tier | 0.0 - 1.0 |

Scores influence future routing — high-performing agents get prioritized for similar tasks.

## Data Flow Summary

```
User: "Add auth"
  -> REQ_SUBMITTED event
  -> Planner.Plan() via Tech Lead LLM
  -> STORY_CREATED events (x5)
  -> REQ_PLANNED event

Dispatcher.DispatchWave()
  -> AGENT_SPAWNED events
  -> STORY_ASSIGNED events
  -> tmux sessions created with Aider

Watchdog.Check() every 10s
  -> Permission bypass if needed
  -> AGENT_STUCK if no progress

Reviewer.Review()
  -> STORY_REVIEW_PASSED or STORY_REVIEW_FAILED

QA.Run()
  -> STORY_QA_PASSED or STORY_QA_FAILED

Merger.Merge()
  -> STORY_PR_CREATED, STORY_MERGED

Reaper.Reap()
  -> WORKTREE_PRUNED, BRANCH_DELETED

All events append to events.jsonl
All events project to nxd.db
TUI dashboard reads from nxd.db (2s refresh)
Web dashboard reads from nxd.db, broadcasts over WebSocket (2s refresh)
```
