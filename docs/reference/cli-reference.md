# NXD CLI Reference

Complete reference for all NXD commands, flags, and options.

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | `nxd.yaml` | Path to configuration file |
| `--state-dir <path>` | (from config) | Override `workspace.state_dir` for this invocation. Relative paths resolve against the working directory. Use it to point a command at another project's state without editing `nxd.yaml`. |
| `--version` | | Show version and exit (the release stamped at build time; `dev` for local builds). `nxd version` prints the same. |
| `--help` | | Show help for any command |

## Commands

### nxd init

Initialize an NXD workspace.

```bash
nxd init                # shared state in ~/.nxd (or wherever workspace.state_dir points)
nxd init --local-state  # per-project state in ./.nxd, added to .gitignore
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--local-state` | false | Write `workspace.state_dir: .nxd` into the generated `nxd.yaml` (repo-relative, resolved against the config file's directory) and append `.nxd/` to `.gitignore`. Recommended whenever you run NXD in more than one repo. |

**What it does:**
1. Generates `nxd.yaml` from defaults tailored to the detected project type (if not present)
2. Creates the state directory with `logs/` and `worktrees/` subdirs — `~/.nxd` by default, `./.nxd` with `--local-state`, or whatever an existing `nxd.yaml` names in `workspace.state_dir`
3. Initializes the event store (`<state_dir>/events.jsonl`)
4. Initializes the projection database (`<state_dir>/nxd.db`)
5. Prints which mode was used (`shared` vs `local`)
6. Checks if Ollama is accessible

**Output example:**
```
Created nxd.yaml with default configuration (project type: Go)
Initialized NXD workspace at /Users/you/project/.nxd (state mode: local)
  Event store:      /Users/you/project/.nxd/events.jsonl
  Projection store: /Users/you/project/.nxd/nxd.db
Ollama detected and running.
```

---

### nxd req

Submit a requirement for decomposition and execution.

```bash
nxd req "<requirement text>"
nxd req --file requirements.md
cat spec.md | nxd req --file -
nxd req --background "<requirement text>"
```

**Arguments / flags:**
| Flag | Description |
|------|-------------|
| `<requirement>` | Positional argument (mutually exclusive with `--file`) |
| `--file, -f <path>` | Read requirement from a file (use `-` for stdin) |
| `--godmode` | Skip permission prompts on LLM calls (fully autonomous) |
| `--review` | Pause after planning; require `nxd approve` before execution |
| `--dry-run` | Simulate LLM responses for pipeline smoke-testing (no API calls) |
| `--background` | Self-daemonize after planning: fork a detached child (Setsid) running `nxd resume <reqID>`; parent exits 0 |

**What it does:**
1. Emits `REQ_SUBMITTED` event
2. Calls Tech Lead LLM to decompose into stories
3. Builds dependency graph (DAG)
4. Validates no circular dependencies
5. Prints the plan summary
6. With `--background`: forks a detached child running `nxd resume <reqID>`; logs go to `~/.nxd/logs/req-<reqID>.log`. Survives parent shell teardown and macOS app-nap.

**Example:**
```bash
nxd req "Add a REST API for user management with CRUD endpoints and JWT auth"
nxd req --background --godmode "Refactor auth middleware"
```

---

### nxd req-logs

Print the log file captured by `nxd req --background`.

```bash
nxd req-logs <req-id>
```

**Arguments:**
| Argument | Description |
|----------|-------------|
| `<req-id>` | The requirement ID printed by `nxd req --background` |

**What it does:**
1. Reads `~/.nxd/logs/req-<req-id>.log` and writes to stdout
2. Errors with a helpful message if no log file exists (e.g., req was run without `--background`)

For live following, use `tail -f` on the log file path.

---

### nxd status

Show requirement and story status.

```bash
nxd status [--req <id>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--req <id>` | (all) | Filter to a specific requirement |

**Output includes:**
- Requirement ID, title, and status
- Story count per status (planned, in_progress, review, qa, merged)
- Per-story details when filtering by requirement

---

### nxd resume

Resume a paused requirement pipeline.

```bash
nxd resume <req-id>
nxd resume <req-id> --repo /path/to/repo
```

**Arguments:**
| Argument | Required | Description |
|----------|----------|-------------|
| `<req-id>` | No | Requirement ID to resume (auto-selected when exactly one active requirement exists) |

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--repo <path>` | cwd | Repository to run in. `resume` refuses to run when the requirement's recorded `repo_path` does not match the current repository root (symlinks resolved); pass `--repo` with the recorded path to run from elsewhere. |
| `--force` | false | Clear a stale `nxd.lock` whose holder process is dead (or whose contents are unreadable). Refuses when the holder is still alive. |
| `--godmode` | false | Skip permission prompts on LLM calls |
| `--dry-run` | false | Simulate LLM responses |

**What it does:**
1. Takes the pipeline lock, then loads state (rebuilding the projection if it is behind the log)
2. Verifies the requirement belongs to this repository
3. Rebuilds the dependency graph
4. Identifies stories with all dependencies satisfied
5. Dispatches the next wave of ready stories

If a requirement was submitted from another repo you get:
`requirement <id> belongs to /path/a; run from that repo or pass --repo /path/a`.

---

### nxd agents

List all agents and their current status.

```bash
nxd agents [--status <status>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--status <s>` | (all) | Filter by status: `active`, `idle`, `stuck`, `terminated` |

**Output columns:** ID, Role, Model, Status, Current Story, Session Name

---

### nxd escalations

List all escalation events.

```bash
nxd escalations
```

**Output columns:** Story ID, From Role, To Role, Reason, Status, Timestamp

---

### nxd gc

Garbage collect merged branches and worktrees.

```bash
nxd gc [--dry-run]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | false | Preview cleanup without deleting anything |

**What it cleans:**
- Worktrees for merged stories (if `worktree_prune: deferred`)
- Branches older than `branch_retention_days`

---

### nxd config

Configuration management subcommands.

```bash
nxd config show       # Pretty-print current config as YAML
nxd config validate   # Validate config file
```

**show** outputs the full parsed config including defaults for unset fields.

**validate** reports the first validation error found, or "Configuration valid" on success.

---

### nxd timeline

Reconstruct a requirement's chronological history from the event log: planning, waves, per-story lifecycle with durations, review/QA/security outcomes, escalations, pauses, budget events, and completion.

```bash
nxd timeline               # auto-selects when only one requirement exists
nxd timeline <req-id>
nxd timeline <req-id> --json
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | Emit the timeline as structured JSON |

**Output includes:**
- Chronological event lines with timestamps (noise like per-iteration progress is filtered out)
- Per-story rollup: status, wall-clock duration (started → merged), wave
- Requirement span and merged/total summary

---

### nxd events

Query the event store.

```bash
nxd events [--type <type>] [--story <id>] [--limit <n>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--type <type>` | (all) | Filter by event type (e.g., `STORY_MERGED`) |
| `--story <id>` | (all) | Filter by story ID |
| `--limit <n>` | 50 | Maximum events to display |

**Events are displayed newest-first.**

**Event types:**
```
REQ_SUBMITTED, REQ_ANALYZED, REQ_PLANNED, REQ_COMPLETED
STORY_CREATED, STORY_ESTIMATED, STORY_ASSIGNED, STORY_STARTED,
STORY_PROGRESS, STORY_COMPLETED, STORY_REVIEW_REQUESTED,
STORY_REVIEW_PASSED, STORY_REVIEW_FAILED, STORY_QA_STARTED,
STORY_QA_PASSED, STORY_QA_FAILED, STORY_PR_CREATED, STORY_MERGED
AGENT_SPAWNED, AGENT_CHECKPOINT, AGENT_RESUMED, AGENT_STUCK, AGENT_TERMINATED
ESCALATION_CREATED, ESCALATION_RESOLVED
SUPERVISOR_CHECK, SUPERVISOR_REPRIORITIZE, SUPERVISOR_DRIFT_DETECTED
WORKTREE_PRUNED, BRANCH_DELETED, GC_COMPLETED
```

---

### nxd dashboard

Launch the dashboard. Defaults to the TUI; use `--web` for the browser-based dashboard.

```bash
nxd dashboard [--web] [--port <port>]
```

**Flags:**
| Flag | Default | Description |
|------|---------|-------------|
| `--web` | false | Start the web dashboard instead of the TUI |
| `--port <port>` | `8787` | Port for the web dashboard (only used with `--web`) |

**Examples:**
```bash
nxd dashboard                        # TUI dashboard
nxd dashboard --web                  # Web dashboard at localhost:8787
nxd dashboard --web --port 9090      # Web dashboard on a custom port
```

#### TUI Dashboard

Single-pane layout — all sections visible at once, no tabs:

| Section | Description |
|---------|-------------|
| Agents | Active agents with role, model, and current story |
| Pipeline | Per-status story counts with a progress bar |
| Stories | Full story table, scrollable |
| Activity | Real-time event feed (last N events) |
| Escalations | Pending and resolved escalations (collapsible) |

**TUI controls:**
| Key | Action |
|-----|--------|
| `j` / `k` | Scroll stories table down / up |
| `w` | Open the web dashboard in the browser |
| `q` / `Ctrl+C` | Quit |

Data refreshes every 2 seconds automatically.

#### Web Dashboard

Opens at `http://localhost:<port>`. Updates in real time via WebSocket.

**Available actions:**

| Action | Target | Notes |
|--------|--------|-------|
| Pause / Resume | Requirement | Halts or restarts story dispatch |
| Retry | Story | Re-dispatches a failed story to the same agent |
| Reassign | Story | Reassigns story to a different agent |
| Escalate | Story | Manually escalates to the next tier |
| Kill | Agent | Terminates an agent's tmux session |
| Edit | Story | Edits story title or description |

Destructive actions (kill, reassign, edit) show a confirmation dialog before executing. Results are shown as toast notifications. The browser reconnects automatically if the WebSocket drops.

The web dashboard surfaces a per-story **DB** column populated from the `STORY_DB_CREATED` / `STORY_DB_FAILED` / `STORY_DB_DELETED` projection, plus an aggregate **Databases** panel (created/failed/deleted counts). Both are hidden when `devdb.provider` is `null` or unset.

---

### nxd logs

Tail a story's agent trace JSONL (per-story event log written by the executor).

```bash
nxd logs <story-id> [--follow] [--lines N] [--raw]
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--follow, -f` | Stream new lines as they arrive |
| `--lines N` | Limit to the last N lines |
| `--raw` | Print raw JSONL without pretty-formatting |

---

### nxd diff

Print a worktree diff against the base branch for a story.

```bash
nxd diff <story-id> [--stat] [--cached]
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--stat` | Show a diffstat summary instead of full diff |
| `--cached` | Show only staged changes |

---

### nxd db

Inspect and manage devdb-provisioned ephemeral databases. The active provider is determined by the project's `devdb.provider` config (`docker` or `null`).

```bash
nxd db list                            # all DBs the provider knows about
nxd db connect <db-name>               # print psql command + DSN
nxd db sql <db-name> <query>           # one-shot SQL query
nxd db schema <db-name>                # agent-friendly schema dump
nxd db delete <db-name> --confirm      # destructive: drop a DB
nxd db gc                              # orphan recovery scan
nxd db ping                            # provider reachability check
nxd db template list                   # list template DBs (docker only)
nxd db template create <name> --from <dump.sql>
```

When `devdb.provider == null` the subcommands return a helpful "devdb is not configured" error so non-devdb projects fail safely. `nxd db delete` always requires `--confirm` because the operation is irreversible.

---

### nxd pause

Pause a requirement. Emits `REQ_PAUSED`; active agents finish their current step but no new waves dispatch until `nxd resume`.

```bash
nxd pause <req-id>
```

---

### nxd archive

Archive a finished requirement so it stops appearing in `status` / `dashboard`. Use `--all` on those commands to see archived requirements again.

```bash
nxd archive <req-id>
```

---

### nxd models

Manage and check LLM model versions.

```bash
nxd models check         # Query Ollama + Google AI Studio for newer model versions
```

---

### nxd metrics

Show aggregate pipeline metrics and token usage from `metrics.jsonl`.

```bash
nxd metrics [--json]
```

---

### nxd watch

Tail the event store as a live stream. Ctrl+C to stop.

```bash
nxd watch
```

---

### nxd plan

Run the planning pipeline (classify, investigate, plan) against a temporary store and print the proposed plan. Nothing is persisted.

```bash
nxd plan "<requirement>"
nxd plan --file requirements.md
cat spec.md | nxd plan --file -
```

---

### nxd approve

Approve a requirement in `pending_review` status (set by `nxd req --review`). Transitions it to `planned` so execution can proceed.

```bash
nxd approve <req-id>
```

---

### nxd reject

Reject a requirement in `pending_review` status. Emits `REQ_REJECTED`.

```bash
nxd reject <req-id>
```

---

### nxd review

Inspect a story's pending changes before merge.

```bash
nxd review <story-id>
```

---

### nxd merge

Manually merge a story that has reached `merge_ready`. Used when `merge.auto_merge` is off or a story needed human sign-off.

```bash
nxd merge <story-id>
```

---

### nxd doctor

Run preflight checks on every NXD dependency and configuration value. Use before the first run on a new machine.

```bash
nxd doctor
```

Checks cover Go, git, tmux, Ollama, the Gemma model, config validity, the state directory, disk/permissions, and optional integrations (MemPalace, Google AI, plugins, devdb). One check, **Projection drift**, compares the SQLite projection's reconciliation watermark against the event-log length: it warns when the projection is behind the log (the desync that a normal command auto-rebuilds on its next open) and reports "in sync" otherwise. The check is read-only — it never rebuilds the projection or creates stores as a side effect.

---

### nxd state

Inspect and repair the per-project event log and SQLite projection.

```bash
nxd state check            # read-only health report
nxd state repair           # quarantine bad lines, rewrite the log, keep a .bak
nxd state compact          # archive informational events of completed requirements
nxd state rebuild          # replay events.jsonl into nxd.db
nxd state <sub> --json     # machine-readable output
```

| Subcommand | Lock | What it does |
|------------|------|--------------|
| `check` | none | Scans `events.jsonl`: line/valid counts, size, last event time, whether the final line is a **torn write** (crash mid-append, no trailing newline) and which lines are **malformed**. Exits non-zero when unhealthy (except with `--json`). |
| `repair` | pipeline | Moves malformed lines and any torn tail to `events.quarantine.jsonl`, rewrites the log atomically (temp file + rename) and leaves `events.jsonl.bak`. Nothing is ever deleted. |
| `compact` | pipeline | Removes only `STORY_PROGRESS` and `AGENT_CHECKPOINT` events whose requirement has reached `REQ_COMPLETED`; the removed lines go to `events.archive-<timestamp>.jsonl` and the original is kept as `.bak`. Refuses to run on an unhealthy log. |
| `rebuild` | pipeline | Truncates the projection tables and replays the whole log in one transaction. Use after a repair/compact, or when a read-only command logged `projection N events behind; pipeline running, skipping rebuild`. |

Commands that take the pipeline lock refuse to run while `nxd req` / `nxd resume` is active. Readers tolerate a torn final line automatically (it is skipped and logged once); every other malformed line is an error unless `NXD_EVENTS_LENIENT=1` is set, in which case the bad lines are moved to `events.quarantine.jsonl` instead of being dropped.

---

### nxd cancel

Stop work on a story or a whole requirement.

```bash
nxd cancel <story-id> [--reason "why"]
nxd cancel <req-id>   [--reason "why"]
```

| Target | Events emitted | Side effects |
|--------|----------------|--------------|
| story (`assigned`, `in_progress`, `review`, `qa`, `merge_ready`) | `STORY_RESET` (→ `draft`, payload `reason`, `previous_status`) then `AGENT_TERMINATED` | Kills the agent's tmux session if alive |
| requirement (not `completed`/`archived`) | the above for every active story, then `REQ_PAUSED` with reason `cancelled by operator` (`stories_cancelled` in payload) | Status becomes `paused`; `nxd resume <req-id>` continues later |

`--reason` is appended to the recorded reason (`cancelled by operator: <reason>`). Cancelling a draft/merged story or a completed requirement is refused with a clear error and emits nothing.

---

### nxd version

Print the build version (`nxd <version>`), identical to `nxd --version`.

---

### nxd estimate

Produce a client quote and internal cost projection for a requirement. Uses the planner for accuracy or `--quick` for a heuristic estimate.

```bash
nxd estimate "<requirement>"
nxd estimate --file requirements.md --quick
```

---

### nxd report

Produce a client-facing delivery report (deliverables, timeline, effort) for a completed requirement. `--internal` adds technical detail; `--html` emits styled HTML.

```bash
nxd report <req-id> [--internal] [--html]
```

---

### nxd learn

Run repository analysis to build a `RepoProfile` that agents consume at dispatch time. Three passes: static scan, git history, LLM-assisted deep analysis.

```bash
nxd learn [--force] [--pass <1|2|3>]
```

---

### nxd security

Security agent: scan a repository with deterministic scanners (gosec, govulncheck, gitleaks, semgrep, npm audit) plus an optional LLM threat-model review against a growable OWASP/CWE knowledge base that learns new vulnerability classes from confirmed findings. The same agent runs as a per-story pre-merge gate during `nxd resume`.

```bash
nxd security scan [path] [--json] [--llm] [--min <critical|high|medium|low>]
nxd security kb [--json]
```

- `scan` reports findings by severity and lists applicable scanners that are not installed (no silent gaps). Exit code is non-zero when a finding meets `--min` (default `high`) — CI-friendly.
- `kb` shows the knowledge base: version, baseline rules, and `+`-marked learned rules.
- Configure via the `security:` section of `nxd.yaml` (`disable_gate`, `gate_severity` default `critical`, `auto_learn`, `kb_path`).

---

### nxd spec

Spec-driven development scaffolding. `nxd spec init` creates a `.spec/` folder with 8 markdown files covering the 5W1H dimensions; the planner picks them up as structured context.

```bash
nxd spec init
```

---

### nxd direct

Append an operator directive to the event log. The native runtime checks for unacknowledged directives at the top of every iteration and prepends them to the agent's prompt — useful for redirecting agents without pausing the run.

```bash
nxd direct <req-id-or-story-id> "<directive text>"
```

---

### nxd improve

Run the self-improvement module: scan metrics + state, optionally fetch curated tips from a JSON feed, and print recommendations.

```bash
nxd improve
nxd improve --feed https://example.com/tips.json
nxd improve --json
```

---

### Tech-Lead conflict resolver + post-merge integration build

When two stories merge against the same files, NXD runs an automated three-way conflict resolution pipeline:

1. **Binary detection** — uses `git diff --numstat` and a null-byte sniff to short-circuit binary conflicts before running text-merge.
2. **Tech-Lead LLM resolution** — for textual conflicts, the Tech-Lead model receives the two diffs plus the base file and produces a unified resolution.
3. **Post-merge integration build** — after the merge commit lands, the configured `go build ./...` / equivalent runs against the integrated tree to surface compile-level regressions introduced by the resolution.
4. **Binary strip** — release binaries are stripped of debug symbols as part of the post-merge step.

No new CLI surface. Configuration lives under `merge:` in `nxd.yaml`.
