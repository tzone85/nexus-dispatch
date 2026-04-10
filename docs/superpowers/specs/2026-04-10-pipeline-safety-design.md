# Pipeline Safety Design Spec

**Date:** 2026-04-10
**Status:** Approved
**Branch:** `feat/pipeline-safety`

## Overview

Four safety features that make NXD trustworthy for real-world usage: human review gates (approve plans before execution, review diffs before merge), crash recovery (detect and fix inconsistent state on resume), security hardening (command allowlists, prompt injection sanitization), and multi-requirement locking (prevent concurrent pipeline corruption).

### Features

| # | Feature | What it prevents |
|---|---------|-----------------|
| 2 | Human review gates | Auto-merging broken code with no way to stop it |
| 3 | Crash recovery | State inconsistency after process crashes |
| 5 | Security hardening | Arbitrary command execution, prompt injection |
| 7 | Multi-requirement lock | Race conditions from concurrent nxd processes |

### Constraints

- All new features are opt-in or backward compatible — existing behavior unchanged by default
- Lock uses OS-level flock (crash-safe, auto-released on process death)
- Recovery is non-destructive — events are source of truth, projections rebuilt from events
- Sanitization is lightweight defense-in-depth, not paranoid filtering

---

## Section 1: Human Review Gates

### Checkpoint 1: Plan Review (`nxd req --review`)

New `--review` flag on `nxd req`. When set:
1. Planner runs normally (classification, investigation, planning)
2. Plan is printed to stdout
3. Instead of `REQ_PLANNED`, emits `REQ_PENDING_REVIEW` event
4. Requirement status set to `"pending_review"`
5. Pipeline is NOT runnable until approved

User reviews via `nxd status --req <id>`, then:
- `nxd approve <req-id>` — emits `REQ_PLANNED`, transitions to "planned", unlocks for `nxd resume`
- `nxd reject <req-id>` — emits `REQ_REJECTED`, sets status to "rejected"

Without `--review` flag, behavior is unchanged (auto-plan, same as today).

### Checkpoint 2: Pre-Merge Review (`merge.review_before_merge`)

New config option:

```yaml
merge:
  review_before_merge: false
```

When `true`: after QA passes, story status becomes `"merge_ready"` instead of immediately merging. User reviews and merges manually:
- `nxd review <story-id>` — shows git diff, review summary, QA results
- `nxd merge <story-id>` — triggers the merge
- `nxd reject-story <story-id>` — sends story back with feedback (resets to "draft" with ReviewFeedback)

When `false` (default): behavior unchanged (auto-merge after QA).

### Checkpoint 3: Dry-Run Planning (`nxd plan`)

New command `nxd plan <requirement>` runs the full classification + investigation + planning pipeline but does NOT emit events or persist to stores. Prints the plan and exits.

Same flags as `nxd req` (`--file`, `--config`). Output shows: detected codebase type, classification, investigation summary (if existing), proposed stories with complexity and waves, total complexity estimate.

---

## Section 2: Crash Recovery

### Recovery Module

**New file:** `internal/engine/recovery.go`

`RunRecovery(repoDir string, cfg config.Config, events state.EventStore, proj *state.SQLiteStore) []RecoveryAction`

Runs at the start of `nxd resume` before dispatching new work.

### Four Recovery Checks

**Check 1: Orphaned worktrees**

Stories in "in_progress" or "review" status whose worktree is missing or corrupt.
- Verify worktree path exists and has valid `.git` file
- If missing: reset story to "draft" (re-dispatch)
- Emit `EventStoryRecovery` with reason "orphaned_worktree"

**Check 2: Stuck merges**

Stories in "pr_submitted" status where the merge never completed.
- In `local` mode: check `git branch --merged main` for the story branch
- In `github` mode: check `gh pr view` for PR status
- If already merged: emit `EventStoryMerged` (catch up projection)
- If no PR exists: reset to "qa" status to re-attempt merge

**Check 3: Stale tmux sessions**

Agent sessions still alive in tmux but their story is already completed/merged.
- List sessions matching `nxd-*` pattern
- Kill sessions whose story is in terminal status
- Log recovery action

**Check 4: Event-projection consistency**

Replay all events and verify projected state matches SQLite.
- If divergence found: re-project from events (events are source of truth)
- Return list of inconsistencies found and fixed

### Recovery Output

```go
type RecoveryAction struct {
    StoryID     string
    Type        string // "orphaned_worktree", "stuck_merge", "stale_session", "projection_fix"
    Description string
}
```

Printed at the start of resume:
```
Recovery: fixed 2 issues from previous crash
  - s-001: orphaned worktree — reset to draft for re-dispatch
  - s-002: stuck merge — PR already merged, updated projection
```

### New Event Type

`EventStoryRecovery` — emitted when recovery fixes a story's state. Payload: `{reason, action, previous_status, new_status}`.

---

## Section 3: Security Hardening

### Fix 1: Investigator Command Allowlist

The Investigator's `run_command` currently executes ANY shell command. Add an allowlist.

**Default allowlist:**

```go
var defaultInvestigatorAllowlist = []string{
    "ls", "find", "wc", "grep", "cat", "head", "tail",
    "git log", "git status", "git diff", "git ls-files", "git blame", "git branch",
    "go build", "go test", "go mod", "go vet",
    "npm test", "npm run", "npm ls",
    "python -m pytest", "python -m py_compile",
    "make", "docker ps", "docker-compose config",
}
```

Configurable via:

```yaml
investigation:
  command_allowlist:
    - "ls"
    - "find"
    # ...
```

Commands not matching any prefix → rejected with error returned to model.

**Modified file:** `internal/engine/investigator.go` — `execRunCommand` gains allowlist check before execution.

### Fix 2: Prompt Injection Sanitization

**New file:** `internal/agent/sanitize.go`

```go
func SanitizePromptField(input string) string
```

Defuses known injection patterns:
- Lines starting with `IMPORTANT:`, `IGNORE`, `SYSTEM:`, `INSTRUCTION:` prefixed with `[user-content] `
- Does NOT alter normal text — only defuses obvious injection attempts

Applied in `prompts.go` to:
- `ReviewFeedback` (highest risk — comes from LLM output)
- `PriorWorkContext` (medium risk — comes from MemPalace, could contain previously injected text)

### Fix 3: Investigator Path Traversal Verification

Verify existing path traversal check ensures resolved path stays under repoPath. Add test proving `../../../etc/passwd` is blocked (may already exist).

---

## Section 4: Multi-Requirement Locking

### File-Based Lock

**New file:** `internal/engine/lockfile.go`

```go
type PipelineLock struct {
    path string
    file *os.File
}

func AcquireLock(stateDir string) (*PipelineLock, error)
func (l *PipelineLock) Release() error
```

Uses `syscall.Flock` — OS-level advisory lock. If another process holds the lock, `AcquireLock` returns error immediately (non-blocking).

**Lock file:** `~/.nxd/nxd.lock`

Content (informational, for error messages):
```json
{"pid": 12345, "command": "resume", "req_id": "req-abc", "started_at": "2026-04-10T10:00:00Z"}
```

Actual locking via flock — crash-safe (OS releases lock when process dies).

**Error message when locked:**
```
Error: another NXD process is running (PID 12345, started 2m ago).
Use 'nxd status' to check progress, or wait for it to finish.
```

### Applied To

Locked commands: `nxd req`, `nxd resume`, `nxd approve`, `nxd merge`

Not locked (read-only, safe concurrent): `nxd status`, `nxd events`, `nxd watch`, `nxd metrics`, `nxd plan`, `nxd models check`, `nxd config show`, `nxd review`

### Stale Lock Detection

If PID in lock file is no longer running (`kill -0 pid` fails), lock is considered stale — forcefully acquired with warning:
```
Warning: stale lock detected (PID 12345 no longer running). Acquiring lock.
```

---

## Section 5: New CLI Commands + Files Changed

### New CLI Commands

| Command | Purpose |
|---------|---------|
| `nxd plan <requirement>` | Dry-run planning — preview without persisting |
| `nxd approve <req-id>` | Approve a pending plan |
| `nxd reject <req-id>` | Reject a pending plan |
| `nxd review <story-id>` | Show diff + review + QA for pre-merge review |
| `nxd merge <story-id>` | Manually merge a "merge_ready" story |

### New Files (10)

| File | Purpose |
|------|---------|
| `internal/cli/plan.go` | `nxd plan` dry-run command |
| `internal/cli/approve.go` | `nxd approve` command |
| `internal/cli/reject.go` | `nxd reject` command |
| `internal/cli/review_story.go` | `nxd review` command |
| `internal/cli/merge_story.go` | `nxd merge` command |
| `internal/engine/recovery.go` | Crash recovery checks |
| `internal/engine/recovery_test.go` | Recovery scenario tests |
| `internal/engine/lockfile.go` | File-based pipeline lock |
| `internal/engine/lockfile_test.go` | Lock tests |
| `internal/agent/sanitize.go` | Prompt injection sanitization |

### Modified Files (10)

| File | Change |
|------|--------|
| `internal/cli/root.go` | Register 5 new commands |
| `internal/cli/req.go` | Add `--review` flag, conditional REQ_PENDING_REVIEW |
| `internal/cli/resume.go` | Add lock + recovery before dispatch |
| `internal/engine/monitor.go` | Check review_before_merge, set merge_ready status |
| `internal/engine/investigator.go` | Add command allowlist to execRunCommand |
| `internal/agent/prompts.go` | Apply SanitizePromptField to ReviewFeedback, PriorWorkContext |
| `internal/config/config.go` | Add ReviewBeforeMerge, InvestigationConfig |
| `internal/config/loader.go` | Add defaults |
| `internal/state/events.go` | Add 4 new event types |
| `internal/state/sqlite.go` | Project new events |

### Wiring Tests (5 new)

| Test | What it proves |
|------|---------------|
| ReviewFlagPausesBeforePlanning | `--review` → REQ_PENDING_REVIEW, not REQ_PLANNED |
| MergeReadyPausesBeforeMerge | `review_before_merge: true` → story at merge_ready |
| InvestigatorCommandAllowlist | Blocked command returns error |
| PromptSanitization | Injection patterns defused |
| LockPreventsConurrentAccess | Second lock acquisition fails with error |
