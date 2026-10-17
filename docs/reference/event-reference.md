# NXD Event Reference

Every action in NXD produces an immutable event. `internal/state/events.go` defines **65 event types**; a few are defined but not emitted by the running pipeline (noted below), and the monitor also emits one raw string, `PIPELINE_STALLED`. The detailed entries below cover the core requirement → planning → dispatch → review → QA → security → merge pipeline. The remaining types — added across 2026-04 → 2026-06 for the controller, devdb lifecycle, conflict resolver, integration build, and stage timing — are summarised in the *Additional events* section at the bottom of this page. The canonical list of strings lives in `internal/state/events.go`; the test `internal/config/example_gen_test.go` guards the generated example config from drifting, and a future generator can do the same for this page.

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

### REQ_BUDGET_WARNING
**When:** The requirement's actual LLM spend crosses `billing.budget_warn_pct` (default 80%) of `billing.budget_usd`. Fires once per run.
**Producer:** Monitor (budget guard)
**Payload:**
```json
{ "id": "req-...", "spent_usd": 8.10, "budget_usd": 10.0 }
```
**Projection:** None (informational; also a default notification trigger)

### REQ_BUDGET_EXCEEDED
**When:** Spend reaches `billing.budget_usd`. Emitted alongside a `REQ_PAUSED` that halts the pipeline so no further tokens burn.
**Producer:** Monitor (budget guard)
**Payload:**
```json
{ "id": "req-...", "spent_usd": 10.42, "budget_usd": 10.0 }
```
**Projection:** None directly (the paired REQ_PAUSED sets status "paused")

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
**When:** Dispatcher assigns story to an agent
**Payload:**
```json
{
  "agent_id": "junior-req01-1",
  "wave": 1
}
```
**Projection:** Updates story status to "assigned", sets agent_id and wave. The branch is not known yet — it arrives with `STORY_STARTED`.

### STORY_STARTED
**When:** Executor spawns the agent and begins the story
**Payload:**
```json
{
  "worktree_path": "~/.nxd/worktrees/story-01",
  "runtime": "claude-code",
  "session_name": "nxd-req01-junior-1",
  "branch": "nxd/story-01",
  "tier": 0,
  "role": "junior"
}
```
`session_name` is sent by the CLI runtimes only; the native (Gemma) runtime omits it.

**Projection:** Updates story status to "in_progress" and sets branch from the payload (an empty branch never overwrites one already set; a non-empty branch from a re-dispatch replaces it). `stories.branch` is what `nxd merge`, `nxd review`, `nxd gc` and `nxd archive` use when they load a story from the projection. Projections created before the branch was persisted are backfilled from the event log on startup (`BackfillStoryBranches`).

### STORY_PROGRESS
**When:** Agent reports intermediate progress
**Payload:** `{ "message": "Implemented User struct, writing tests" }`
**Projection:** No status change (informational)

### STORY_COMPLETED
**When:** Agent finishes implementation
**Payload:** `{ "files_changed": 3, "lines_added": 120 }`
**Projection:** Updates story status to "review"

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
**Projection:** Updates story status to "qa"

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
**Projection:** Updates story status to "draft" (re-dispatched; counts against the escalation tier budget)

### STORY_QA_STARTED
**When:** QA pipeline begins
**Payload:** `{ "worktree_path": "~/.nxd/worktrees/..." }`
**Projection:** Updates story status to "qa"

### STORY_QA_PASSED
**When:** All QA checks pass (configured lint/build/test commands plus `qa.success_criteria`)
**Payload:**
```json
{ "passed": true, "total_checks": 3, "failed_checks": [] }
```
**Projection:** Updates story status to "pr_submitted" — before the security gate runs and before any merge or PR exists

### STORY_QA_FAILED
**When:** One or more QA checks fail
**Payload:**
```json
{ "passed": false, "total_checks": 3, "failed_checks": ["test"] }
```
**Projection:** Updates story status to "draft" (re-dispatched with the failing output as feedback)

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
  "session_name": "nxd-req01-junior-1"
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

### STORY_ESCALATED
**When:** A story is bumped to a higher tier — the monitor when a tier's retry budget is spent, the Manager on a retry decision, or the active controller when reprioritizing a stuck story
**Producer:** Monitor / Manager / Controller
**Payload:**
```json
{
  "from_tier": 0,
  "to_tier": 1,
  "reason": "review rejected: missing error handling"
}
```
Tiers are integers: 0 same role, 1 Senior, 2 Manager diagnosis, 3 Tech Lead re-plan, 4 requirement paused. `nxd resume` does not attach the Manager or Planner, so tiers 2 and 3 are currently re-dispatched to Senior.
**Projection:** Story reassigned at the higher tier; resolution is observable through the story's subsequent lifecycle events (`STORY_ASSIGNED` → … → `STORY_MERGED`)

## Security Gate Events

### SECURITY_SCAN_COMPLETED
**When:** The security gate finishes scanning a repository (per story, or via `nxd security scan`)
**Producer:** Security gate
**Payload:** `{ "repo": "/path/to/repo", "findings": 3, "max": "medium" }`

### STORY_SECURITY_PASSED
**When:** No finding at or above the configured gate severity for the story's diff
**Producer:** Security gate
**Payload:** `{ "findings": 0 }`
**Projection:** Story may proceed to merge

### STORY_SECURITY_FAILED
**When:** A finding at or above the gate severity blocks the story
**Producer:** Security gate
**Payload:** `{ "reason": "1 critical: hardcoded credential", "findings": 4, "max": "critical" }`
**Projection:** Story blocked pending a human decision

### SECURITY_RULE_LEARNED
**When:** Auto-learn distils a confirmed finding into a new knowledge-base rule
**Producer:** Security gate
**Payload:** `{ "rule": "kb-014", "title": "Unparameterised SQL in repository layer" }`

## Supervisor Events

Defined but not emitted: the supervisor is never constructed (`NewSupervisor` has no callers).

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
**When:** `Reaper.Reap` prunes a worktree. Nothing calls `Reap` today, so this event is not emitted; the monitor removes worktrees after merge without an event.
**Payload:** `{ "worktree_path": "~/.nxd/worktrees/...", "mode": "immediate" }`

### BRANCH_DELETED
**When:** `nxd gc` deletes the branch of a merged story older than `cleanup.branch_retention_days`
**Payload:** `{ "branch": "nxd/story-01", "reason": "gc_retention_expired" }`

### GC_COMPLETED
**When:** `nxd gc` deleted at least one branch — one event per repository it cleaned (gc runs per requirement repo), each with that repository's count
**Payload:** `{ "branches_deleted": 3, "repo_path": "/path/to/repo" }`
**Projection:** none (nor for `BRANCH_DELETED`); both are explicit no-op cases in `Project` (the switch is exhaustive over `events.go`, guarded by `TestProjectLocked_EveryKnownTypeHasACase`), and `nxd gc` projects them (the reaper takes the projection store) so the projection watermark stays level with the log

## Story Status State Machine

```
draft -> (estimated) -> assigned -> in_progress
    -> review          STORY_COMPLETED / STORY_REVIEW_REQUESTED
    -> qa              STORY_REVIEW_PASSED / STORY_QA_STARTED
    -> pr_submitted    STORY_QA_PASSED (before security gate and merge),
                       STORY_PR_CREATED
    -> merged          STORY_MERGED

    STORY_REVIEW_FAILED, STORY_QA_FAILED, STORY_RESET -> draft
    STORY_MERGE_READY -> merge_ready   (merge.review_before_merge: true)
    STORY_SPLIT       -> split         (replaced by child stories)
    STORY_RECOVERY    -> payload new_status
    nxd archive       -> archived
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
- **STORY_RESET** — `nxd resume` startup recovery sent an orphaned story back to draft (the monitor's retry path uses STORY_REVIEW_FAILED instead)
- **STORY_RECOVERY** — startup recovery or the controller reset a stuck story; the payload's `new_status` becomes the story status
- **STORY_MERGE_READY** — review, QA, and the security gate passed with `merge.review_before_merge: true`; the story waits for a human merge
- **STORY_ESCALATED** — story bumped to a higher escalation tier (see STORY_ESCALATED above)
- **STORY_INTEGRATION_FAILED** — post-merge integration build failed; the Tech Lead model drafts a fix description, which is logged
- **PIPELINE_STALLED** — raw string, not an `EventType` constant: stories remain but none are dispatchable (payload: `req_id`, `pending_count`, `total_stories`, `reason`)

### Requirement-level
- **REQ_PLANNING_STARTED** — Tech Lead began decomposition (kicks off planner stage timing)
- **REQ_CLASSIFIED** — requirement classified as feature / bugfix / refactor
- **REQ_ESTIMATED** — heuristic estimator emitted a quote (`nxd estimate`)
- **REQ_PAUSED** — pipeline paused (billing exhaustion, manual hold, or unrecoverable stall)
- **REQ_RESUMED** — paused requirement resumed
- **REQ_REJECTED** — requirement rejected (planner declined, prompt-injection detected, or budget exceeded)
- **REQ_PENDING_REVIEW** — requirement awaiting human approval before dispatch
- **REQ_BLOCKED** — monitor marked the requirement blocked: it cannot reach green without intervention (payload: `{ "id": "req-01" }`)

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

Note: the three devdb events (`STORY_DB_CREATED`, `STORY_DB_FAILED`, `STORY_DB_DELETED`) are documented in the DevDB Lifecycle Events section above. For the canonical list, see `EventType` constants in `internal/state/events.go`.
