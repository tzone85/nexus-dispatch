# NXD Event Reference

Every action in NXD produces an immutable event. NXD currently emits **58 event types**. The first 32 (described in detail below) cover the core requirement → planning → dispatch → review → QA → merge pipeline. The remaining types — added across 2026-04 → 2026-06 for the controller, devdb lifecycle, conflict resolver, integration build, and stage timing — are summarised in the *Additional events* section at the bottom of this page. The canonical list of strings lives in `internal/state/events.go`; the test `internal/config/example_gen_test.go` guards the generated example config from drifting, and a future generator can do the same for this page.

## Event Structure

```json
{
  "id": "01HZ4K9XMRN6B3P5T8...",
  "type": "STORY_CREATED",
  "timestamp": "2026-03-10T14:30:00Z",
  "agent_id": "tech_lead-req01-1",
  "story_id": "story-01",
  "payload": { "title": "Add User model", "complexity": 2 }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | ULID | Time-sortable unique identifier |
| `type` | string | One of the event types below |
| `timestamp` | ISO 8601 | UTC time of event creation |
| `agent_id` | string | Agent that produced the event |
| `story_id` | string | Related story (empty for system events) |
| `payload` | JSON | Event-specific data |

## Requirement Events

### REQ_SUBMITTED
**When:** User runs `nxd req "<text>"`
**Producer:** CLI
**Payload:**
```json
{ "title": "User requirement text", "description": "Full text" }
```
**Projection:** Creates row in `requirements` table with status "pending"

### REQ_ANALYZED
**When:** Tech Lead begins analyzing the requirement
**Producer:** Planner
**Payload:**
```json
{ "tech_stack": "Go 1.23", "repo_path": "/path/to/repo" }
```
**Projection:** Updates requirement status to "analyzed"

### REQ_PLANNED
**When:** Tech Lead finishes creating stories
**Producer:** Planner
**Payload:**
```json
{ "story_count": 5, "total_complexity": 16 }
```
**Projection:** Updates requirement status to "planned"

### REQ_COMPLETED
**When:** All stories for a requirement are merged
**Producer:** Orchestrator
**Payload:**
```json
{ "stories_merged": 5, "total_events": 47 }
```
**Projection:** Updates requirement status to "completed"

## Story Events

### STORY_CREATED
**When:** Planner creates a story from requirement decomposition
**Payload:**
```json
{
  "title": "Add User model",
  "description": "Create User struct with validation",
  "acceptance_criteria": "User struct with name, email, password hash",
  "complexity": 2,
  "depends_on": []
}
```
**Projection:** Creates row in `stories` table with status "draft"

### STORY_ESTIMATED
**When:** Complexity score finalized
**Payload:** `{ "complexity": 3, "estimated_by": "tech_lead" }`
**Projection:** Updates story status to "estimated", sets complexity

### STORY_ASSIGNED
**When:** Dispatcher assigns story to an agent role
**Payload:**
```json
{
  "agent_id": "junior-req01-1",
  "role": "junior",
  "session_name": "nxd-req01-junior-1",
  "branch": "nxd/story-01"
}
```
**Projection:** Updates story status to "assigned", sets agent_id and branch

### STORY_STARTED
**When:** Agent begins working on the story
**Payload:** `{ "worktree_path": "~/.nxd/worktrees/nxd-req01-junior-1" }`
**Projection:** Updates story status to "in_progress"

### STORY_PROGRESS
**When:** Agent reports intermediate progress
**Payload:** `{ "message": "Implemented User struct, writing tests" }`
**Projection:** No status change (informational)

### STORY_COMPLETED
**When:** Agent finishes implementation
**Payload:** `{ "files_changed": 3, "lines_added": 120 }`
**Projection:** Updates story status to "completed"

### STORY_REVIEW_REQUESTED
**When:** Story submitted for Senior code review
**Payload:** `{ "branch": "nxd/story-01", "diff_lines": 150 }`
**Projection:** Updates story status to "review"

### STORY_REVIEW_PASSED
**When:** Reviewer approves the code
**Payload:**
```json
{
  "passed": true,
  "comment_count": 2,
  "summary": "Clean implementation, minor suggestions"
}
```
**Projection:** Updates story status to "review_passed"

### STORY_REVIEW_FAILED
**When:** Reviewer requests changes
**Payload:**
```json
{
  "passed": false,
  "comment_count": 3,
  "summary": "Missing error handling in auth middleware"
}
```
**Projection:** Updates story status to "review_failed" (loops back to agent)

### STORY_QA_STARTED
**When:** QA pipeline begins
**Payload:** `{ "worktree_path": "~/.nxd/worktrees/..." }`
**Projection:** Updates story status to "qa"

### STORY_QA_PASSED
**When:** All QA checks pass (lint + build + test)
**Payload:**
```json
{ "passed": true, "total_checks": 3, "failed_checks": [] }
```
**Projection:** Updates story status to "qa_passed"

### STORY_QA_FAILED
**When:** One or more QA checks fail
**Payload:**
```json
{ "passed": false, "total_checks": 3, "failed_checks": ["test"] }
```
**Projection:** Updates story status to "qa_failed" (loops back to agent)

### STORY_PR_CREATED
**When:** Merger creates a PR or performs local merge
**Payload:**
```json
{ "pr_number": 42, "pr_url": "https://github.com/...", "branch": "nxd/story-01" }
```
In local mode: `{ "pr_number": 0, "pr_url": "local://merged", "merged_sha": "abc123" }`
**Projection:** Updates story status to "pr_submitted", sets pr_url

### STORY_MERGED
**When:** Story branch merged into base
**Payload:** `{ "pr_number": 42, "branch": "nxd/story-01" }`
**Projection:** Updates story status to "merged"

## Agent Events

### AGENT_SPAWNED
**When:** Dispatcher creates a new agent session
**Payload:**
```json
{
  "role": "junior",
  "model": "qwen2.5-coder:7b",
  "runtime": "aider",
  "session_name": "nxd-req01-junior-1",
  "story_id": "story-01"
}
```
**Projection:** Creates row in `agents` table

### AGENT_CHECKPOINT
**When:** Agent saves intermediate state
**Payload:** `{ "message": "Tests passing, moving to next subtask" }`

### AGENT_RESUMED
**When:** Previously paused agent resumes work
**Payload:** `{ "reason": "pipeline resumed" }`

### AGENT_STUCK
**When:** Watchdog detects no progress
**Payload:**
```json
{ "session_name": "nxd-req01-junior-1", "stuck_for_s": 180 }
```

### AGENT_TERMINATED
**When:** Agent session ends (success or forced)
**Payload:** `{ "reason": "completed" | "stuck" | "escalated" | "killed" }`

## Escalation Events

### ESCALATION_CREATED
**When:** Agent exceeds retry limit or gets stuck
**Payload:**
```json
{
  "story_id": "story-01",
  "from_role": "junior",
  "to_role": "senior",
  "reason": "Agent stuck after 2 retries"
}
```

### ESCALATION_RESOLVED
**When:** Escalation target successfully handles the issue
**Payload:** `{ "resolved_by": "senior-req01-1", "action": "completed" }`

## Supervisor Events

### SUPERVISOR_CHECK
**When:** Periodic progress review shows everything on track
**Payload:** `{ "on_track": true, "concerns": [] }`

### SUPERVISOR_REPRIORITIZE
**When:** Supervisor recommends changing story priorities
**Payload:** `{ "reprioritize": ["story-03", "story-05"] }`

### SUPERVISOR_DRIFT_DETECTED
**When:** Stories are diverging from the original requirement
**Payload:**
```json
{
  "on_track": false,
  "concerns": ["Story-03 is implementing features not in the requirement"]
}
```

## DevDB Lifecycle Events

### STORY_DB_CREATED
**When:** `devdb.Lifecycle` successfully provisions a per-story ephemeral DB and writes `.nxd-db/connect.env` into the worktree.
**Payload:** `{ "db_id": "...", "db_name": "...", "provider": "docker", "template": "...", "conn_string_hash": "sha256:..." }`

`conn_string_hash` is a SHA-256 hash of the connection string used by metrics — the raw DSN is **never** put on the event bus.

### STORY_DB_FAILED
**When:** Provisioning fails (provider unreachable, template missing, ports exhausted, etc.)
**Payload:** `{ "db_id": "...", "db_name": "...", "provider": "docker", "error": "..." }`

### STORY_DB_DELETED
**When:** Lifecycle hook tears down a per-story DB after merge/abandon. Carries duration/size metrics for cost analysis.
**Payload:** `{ "db_id": "...", "status": "deleted", "duration_seconds": <float>, "bytes_used": <int> }`

The `status` field may be `kept` instead of `deleted` if `devdb.on_failure.keep_db == true` and the story was abandoned within the retain window.

## Cleanup Events

### WORKTREE_PRUNED
**When:** Reaper deletes a worktree after merge
**Payload:** `{ "worktree_path": "~/.nxd/worktrees/...", "mode": "immediate" }`

### BRANCH_DELETED
**When:** Reaper deletes a merged branch
**Payload:** `{ "branch": "nxd/story-01" }`

### GC_COMPLETED
**When:** Garbage collection finishes
**Payload:** `{ "branches_deleted": 3 }`

## Story Status State Machine

```
draft -> estimated -> assigned -> in_progress
    -> completed -> review -> review_passed -> qa
    -> qa_passed -> pr_submitted -> merged

    review -> review_failed -> (back to in_progress)
    qa -> qa_failed -> (back to in_progress)
```

## Querying Events

```bash
# CLI
nxd events --type STORY_MERGED --limit 10
nxd events --story story-01

# Direct SQLite
sqlite3 ~/.nxd/nxd.db "SELECT type, story_id, timestamp FROM events WHERE type LIKE 'STORY%' ORDER BY timestamp DESC LIMIT 20;"

# Raw JSONL
tail -20 ~/.nxd/events.jsonl | python3 -m json.tool
```

## Additional events (post-2026-04 ports)

The following event types ship in current NXD but pre-date this doc's
expanded coverage. Each is a one-line summary — for the producer / payload
shape, grep `internal/engine/` or `internal/state/events.go`.

### Pipeline / staging
- **STAGE_COMPLETED** — coarse timing marker for each pipeline stage (executor, reviewer, QA, merger) with duration and outcome
- **STORY_REWRITTEN** — Manager rewrote the story (title / description / acceptance criteria / complexity) after diagnosis
- **STORY_SPLIT** — Tech Lead replaced one story with N replacements; payload includes child_story_ids
- **STORY_RESET** — story sent back to draft status by the monitor (post-failure retry path)
- **STORY_RECOVERY** — controller reset a stuck story to draft
- **STORY_MERGE_READY** — review + QA both passed; merger may proceed
- **STORY_ESCALATED** — story bumped to a higher escalation tier (junior → intermediate → senior, etc.)
- **STORY_INTEGRATION_FAILED** — post-merge integration build failed; tech-lead fixer dispatched

### Requirement-level
- **REQ_PLANNING_STARTED** — Tech Lead began decomposition (kicks off planner stage timing)
- **REQ_CLASSIFIED** — requirement classified as feature / bugfix / refactor
- **REQ_ESTIMATED** — heuristic estimator emitted a quote (`nxd estimate`)
- **REQ_PAUSED** — pipeline paused (billing exhaustion, manual hold, or unrecoverable stall)
- **REQ_RESUMED** — paused requirement resumed
- **REQ_REJECTED** — requirement rejected (planner declined, prompt-injection detected, or budget exceeded)
- **REQ_PENDING_REVIEW** — requirement awaiting human approval before dispatch

### Investigation
- **INVESTIGATION_COMPLETED** — investigator produced an InvestigationReport for an existing-codebase requirement

### Conflict resolution
- **STORY_CONFLICT_BINARY** — git rebase hit a binary conflict
- **STORY_CONFLICT_BINARY_REMOVED** — binary file stripped from branch as part of conflict resolution
- **STORY_CONFLICT_ESCALATED** — conflict resolver gave up; merge escalated to human

### Controller (auto-recovery)
- **CONTROLLER_ANALYSIS** — emitted every tick with stories_checked + actions_taken counts
- **CONTROLLER_ACTION** — controller cancelled / restarted / reprioritised a stuck story
- **CONTROLLER_STUCK_DETECTED** — story exceeded stuck threshold (stuck_duration_s, escalation_tier)
- **RECOVERY_COMPLETED** — orphan worktree / lockfile / branch recovery finished

### Operator directives
- **USER_DIRECTIVE** — `nxd direct <id> "<message>"` — injected into the next native-runtime iteration
- **DIRECTIVE_ACKED** — agent acknowledged it processed a USER_DIRECTIVE
- **HUMAN_REVIEW_NEEDED** — pipeline parked awaiting human input (e.g. PR approval, tier-3 escalation)

Note: 6 devdb events (`STORY_DB_CREATED`, `STORY_DB_FAILED`, `STORY_DB_DELETED` plus their controller/recovery siblings) are documented in [`docs/guides/configuration.md`](../guides/configuration.md). For the canonical list, see `EventType` constants in `internal/state/events.go`.
