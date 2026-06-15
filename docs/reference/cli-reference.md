# NXD CLI Reference

Complete reference for all NXD commands, flags, and options.

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | `nxd.yaml` | Path to configuration file |
| `--version` | | Show version and exit |
| `--help` | | Show help for any command |

## Commands

### nxd init

Initialize an NXD workspace.

```bash
nxd init
```

**What it does:**
1. Creates `~/.nxd/` directory with `logs/` and `worktrees/` subdirs
2. Copies `nxd.config.example.yaml` to `nxd.yaml` (if not present)
3. Initializes the event store (`~/.nxd/events.jsonl`)
4. Initializes the projection database (`~/.nxd/nxd.db`)
5. Checks if Ollama is accessible at `localhost:11434`

**Output example:**
```
Initialized NXD workspace at /Users/you/.nxd
  Config: nxd.yaml
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
```

**Arguments:**
| Argument | Required | Description |
|----------|----------|-------------|
| `<req-id>` | Yes | Requirement ID to resume |

**What it does:**
1. Loads existing state for the requirement
2. Rebuilds the dependency graph
3. Identifies stories with all dependencies satisfied
4. Dispatches the next wave of ready stories

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
