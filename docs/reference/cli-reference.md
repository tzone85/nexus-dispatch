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
```

**Arguments:**
| Argument | Required | Description |
|----------|----------|-------------|
| `<requirement>` | Yes | Natural language description of what to build |

**What it does:**
1. Emits `REQ_SUBMITTED` event
2. Calls Tech Lead LLM to decompose into stories
3. Builds dependency graph (DAG)
4. Validates no circular dependencies
5. Prints the plan summary

**Example:**
```bash
nxd req "Add a REST API for user management with CRUD endpoints and JWT auth"
```

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
