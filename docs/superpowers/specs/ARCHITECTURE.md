# Nexus Dispatch — Full Architecture Overview

> Multi-agent coding orchestrator that decomposes requirements into stories, dispatches them to LLM-powered agents in parallel waves, and runs a review/QA/merge pipeline.

**Version:** 1.0 | **Last Updated:** 2026-04-15 | **Coverage:** 65.3%

---

## Table of Contents

### Core Architecture
1. [System Overview](#1-system-overview)
2. [System Diagrams](#2-system-diagrams)
3. [Package Dependency Graph](#3-package-dependency-graph)

### Business & Revenue
4. [Revenue Pipeline Design](#4-revenue-pipeline-design)
5. [Bayesian Feedback Model](#5-bayesian-feedback-model)
10. [Revenue Projections](#10-revenue-projections)

### Design & Implementation
6. [Executor Design Rationale](#6-executor-design-rationale)
7. [3-Phase Deployment Strategy](#7-3-phase-deployment-strategy)
8. [Technical Decision Table](#8-technical-decision-table)

### Risk & Operations
9. [Risk Assessment](#9-risk-assessment)
11. [Security Architecture](#11-security-architecture) **NEW**
16. [Failure Recovery Playbook](#16-failure-recovery-playbook) **NEW**

### Data & Observability
12. [Data Model & Schema](#12-data-model--schema) **NEW**
13. [Observability Strategy](#13-observability-strategy) **NEW**
14. [Capacity Planning](#14-capacity-planning) **NEW**

### Engineering
15. [Testing Architecture](#15-testing-architecture) **NEW**
19. [State Schema Evolution](#19-state-schema-evolution) **NEW**
20. [Plugin Architecture](#20-plugin-architecture) **NEW**

### Market & Compliance
17. [Competitive Positioning](#17-competitive-positioning) **NEW**
18. [Compliance & IP Considerations](#18-compliance--ip-considerations) **NEW**
21. [User Journey Maps](#21-user-journey-maps) **NEW**

### Meta
22. [Current Limitations](#22-current-limitations) **NEW**

### Appendices
- [Appendix A: Event Type Reference](#appendix-a-event-type-reference)
- [Appendix B: CLI Command Reference](#appendix-b-cli-command-reference)

---

## 1. System Overview

> **Status: IMPLEMENTED** | **Verified against:** commit `3e9d25a` (2026-04-15)

Nexus Dispatch (NXD) is an event-sourced, DAG-driven orchestration engine that turns natural language requirements into merged, tested code — autonomously. It decomposes work into stories, assigns each to a role-appropriate LLM agent, monitors execution, and runs a full review → QA → merge pipeline before delivery.

### Core Principles

| Principle | Implementation |
|-----------|---------------|
| Event Sourcing | Append-only EventStore + materialized SQLite projections |
| Immutability | All domain types are value types; no in-place mutation |
| Wave Parallelism | DAG-based dependency graph; parallel dispatch with file-overlap filtering |
| Dual Runtime | CLI agents (aider/claude/codex via tmux) + native Gemma (in-process goroutines) |
| Cost Transparency | Fibonacci-point estimation + per-token LLM billing + margin calculation |
| Self-Healing | Controller detects stuck agents; Manager diagnoses failures; escalation tiers 0-4 |

### Key Metrics (as of 2026-04-15)

- **Test Coverage:** 65.3% (7 packages above 80%, target 80%)
- **Packages:** 20 internal packages + 1 cmd entry point
- **Event Types:** 25+ domain events covering full lifecycle
- **Agent Roles:** 8 (TechLead, Senior, Intermediate, Junior, QA, Supervisor, Manager, Investigator)

---

## 2. System Diagrams

> **Status: IMPLEMENTED** | **Verified against:** commit `3e9d25a` (2026-04-15)

### 2.1 End-to-End Pipeline Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                          USER / CLIENT                                   │
│                                                                          │
│   $ nxd req "Add user authentication with OAuth2 and JWT"               │
└────────────────────────────────┬─────────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 1: CLASSIFICATION                            │
│                                                                        │
│  Investigator Agent ──► Analyze repo structure, tech stack, patterns   │
│  Classifier LLM     ──► Categorize: greenfield | existing | bugfix    │
│                                                                        │
│  Output: EventReqClassified { category, tech_stack, complexity_est }   │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 2: PLANNING                                  │
│                                                                        │
│  TechLead LLM ──► Decompose requirement into stories                  │
│                    with titles, descriptions, acceptance criteria,     │
│                    complexity points (Fibonacci), owned files,         │
│                    and dependency edges                                │
│                                                                        │
│  DAG Builder  ──► Topological sort ──► Wave assignment                │
│                                                                        │
│  Cost Engine  ──► Map stories to hours ──► Client estimate            │
│                                                                        │
│  Output: PlannedStory[], DAG, Estimate                                │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 3: DISPATCH (per wave)                       │
│                                                                        │
│  ┌─────────────┐    ┌──────────────────┐    ┌─────────────────┐       │
│  │ ReadyNodes() │──►│ AutoTagWaveHints │──►│ FilterOverlap   │       │
│  │ from DAG     │    │ seq vs parallel  │    │ no shared files │       │
│  └─────────────┘    └──────────────────┘    └────────┬────────┘       │
│                                                       │                │
│  RouteByComplexity(story) ──► Role assignment:        │                │
│    complexity ≤ 3  → Junior                           │                │
│    complexity ≤ 5  → Intermediate                     │                │
│    complexity > 5  → Senior                           │                │
│                                                       │                │
│  Output: Assignment[] { StoryID, Role, AgentID, Branch }              │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 4: EXECUTION                                 │
│                                                                        │
│  For each assignment:                                                  │
│    1. CreateWorktree(repo, branch) ──► isolated git worktree          │
│    2. Build prompt (SystemPrompt + GoalPrompt + WaveBrief)            │
│    3. Inject: MemPalace context, RepoProfile, review feedback         │
│    4. Launch:                                                          │
│       ├─ CLI runtime ──► tmux session (aider/claude/codex)            │
│       └─ Native runtime ──► goroutine with tool-calling loop          │
│                                                                        │
│  ┌─────────────── Native Gemma Tool Loop ──────────────────┐          │
│  │  for i := 0; i < maxIterations; i++:                    │          │
│  │    LLM.Complete(messages, tools) ──► ToolCalls          │          │
│  │    for each tool call:                                   │          │
│  │      executeTool(read_file|write_file|edit_file|         │          │
│  │                  run_command|task_complete|               │          │
│  │                  write_scratchboard|read_scratchboard)   │          │
│  │    if task_complete → criteria.EvaluateAll()             │          │
│  └──────────────────────────────────────────────────────────┘          │
│                                                                        │
│  SemaphoreClient wraps LLM for concurrency control (default: 1)       │
│  Progress events ──► EventStore + ArtifactStore trace JSONL            │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 5: MONITORING                                │
│                                                                        │
│  Monitor.Poll() loop (every poll_interval_ms):                        │
│    ├─ CLI agents: check tmux session idle pattern                     │
│    ├─ Native agents: receive STORY_COMPLETED from goroutine           │
│    └─ On completion: extract diff ──► trigger post-execution pipeline │
│                                                                        │
│  Controller.Tick() loop (every interval_s):                           │
│    ├─ Check in_progress stories for stuck_threshold_s                 │
│    ├─ Supervisor.Review() ──► LLM confirms drift                     │
│    └─ Actions: cancel | restart | reprioritize (with cooldowns)       │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 6: POST-EXECUTION PIPELINE                   │
│                                                                        │
│  ┌──────────┐     ┌──────────┐     ┌──────────┐                      │
│  │  REVIEW  │────►│    QA    │────►│  MERGE   │                      │
│  └────┬─────┘     └────┬─────┘     └────┬─────┘                      │
│       │                 │                │                              │
│  Senior LLM         Commands:        Modes:                            │
│  reviews diff +     • lint            • local: git rebase + merge      │
│  acceptance         • build           • github: push + PR + auto-merge │
│  criteria           • test                                              │
│                     • criteria.       Serialized with mergeMu mutex    │
│  Pass/Fail          EvaluateAll()     (one merge at a time)            │
│                                                                        │
│  On failure ──► Escalation chain:                                      │
│    Tier 0: same-role retry (Junior/Intermediate)                       │
│    Tier 1: Senior takes over                                           │
│    Tier 2: Manager.Diagnose() → retry|rewrite|split|escalate          │
│    Tier 3: TechLead re-plans the story                                 │
│    Tier 4: Pause (human intervention required)                         │
└────────────────────────────────┬───────────────────────────────────────┘
                                 │
                                 ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     PHASE 7: COMPLETION                                │
│                                                                        │
│  All stories merged ──► EventReqCompleted                             │
│  ReportBuilder.Build() ──► ReportData                                 │
│    • Story breakdown with durations, escalations, retries             │
│    • Timeline of significant events                                    │
│    • Effort estimate with actual LLM cost and margin                  │
│    • Agent performance stats                                           │
│                                                                        │
│  Next wave ──► Dispatcher.DispatchWave(wave + 1) if stories remain    │
└───────────────────────────────────────────────────────────────────────┘
```

### 2.2 Event Flow & State Architecture

```
                     ┌─────────────────────────────────────────┐
                     │              Event Sources               │
                     ├─────────────────────────────────────────┤
                     │  Planner │ Dispatcher │ Executor         │
                     │  Monitor │ Reviewer   │ QA               │
                     │  Merger  │ Controller │ Supervisor        │
                     │  Manager │ CLI        │ Watchdog          │
                     └───────────────────┬─────────────────────┘
                                         │ Append(Event)
                                         ▼
┌───────────────────────────────────────────────────────────────────────┐
│                         EventStore                                     │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │  FileStore (events.jsonl)  │  SQLiteStore (events table)        │  │
│  │  Append-only JSONL         │  Indexed by type, agent, story     │  │
│  │  Good for streaming        │  Good for queries & projections    │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│                                                                       │
│  Event { ID, Type, Timestamp, AgentID, StoryID, Payload json.Raw }   │
│                                                                       │
│  25+ event types: REQ_SUBMITTED → REQ_COMPLETED lifecycle            │
│  Filter: by Type, AgentID, StoryID, After(time), OnAppend(callback)  │
└───────────────────────────────┬───────────────────────────────────────┘
                                │ Project(Event)
                                ▼
┌───────────────────────────────────────────────────────────────────────┐
│                      ProjectionStore (SQLite)                         │
│                                                                       │
│  Materialized views computed from events:                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐               │
│  │ Requirements  │  │   Stories    │  │    Agents    │               │
│  │ id, title,    │  │ id, title,   │  │ id, role,    │               │
│  │ status,       │  │ status,      │  │ story_id,    │               │
│  │ repo_path,    │  │ complexity,  │  │ session,     │               │
│  │ description   │  │ wave, agent, │  │ worktree     │               │
│  │               │  │ pr_url, ...  │  │              │               │
│  └──────────────┘  └──────────────┘  └──────────────┘               │
│                                                                       │
│  Queries: GetRequirement, GetStory, ListStories(filter),             │
│           ListRequirementsFiltered, ListAgents                        │
└───────────────────────────────────────────────────────────────────────┘
              │                                    │
              ▼                                    ▼
┌──────────────────────┐            ┌──────────────────────────────────┐
│     Web Dashboard     │            │       ArtifactStore              │
│                       │            │                                  │
│  EventBus ──► WS Hub  │            │  Per-story directory:            │
│  ──► Browser clients  │            │    launch_config.json            │
│                       │            │    trace_events.jsonl            │
│  StateSnapshot:       │            │    git_diff.patch                │
│  • Agents, Stories    │            │    review_result.json            │
│  • Pipeline counts    │            │    qa_result.json                │
│  • DAG visualization  │            │    raw_log.txt                   │
│  • Metrics, Costs     │            │                                  │
│  • Review gates       │            │  Used for: post-mortem,          │
│  • Recovery log       │            │  MemPalace mining, auditing      │
└──────────────────────┘            └──────────────────────────────────┘
```

### 2.3 Escalation Chain

```
                    Story fails review/QA
                           │
                           ▼
              ┌─────────────────────────┐
              │  Tier 0: Same-Role Retry │ ◄── Junior or Intermediate
              │  max_retries: 2          │     retries with review feedback
              └────────────┬────────────┘
                           │ exhausted
                           ▼
              ┌─────────────────────────┐
              │  Tier 1: Senior Takeover │ ◄── Escalate to Senior role
              │  max_senior_retries: 3   │     + review feedback context
              └────────────┬────────────┘
                           │ exhausted
                           ▼
              ┌─────────────────────────┐
              │  Tier 2: Manager Diagnose│ ◄── Manager LLM analyzes all
              │  max_manager_attempts: 5 │     prior attempts + decides:
              │                          │
              │  Decisions:              │     → retry(role, env_fixes)
              │  • retry                 │     → rewrite(new_title, desc)
              │  • rewrite               │     → split(children, deps)
              │  • split                 │     → escalate_to_techlead()
              │  • escalate_to_techlead  │
              └────────────┬────────────┘
                           │ exhausted
                           ▼
              ┌─────────────────────────┐
              │  Tier 3: TechLead Re-Plan│ ◄── Re-decomposes the story
              │                          │     with full context of all
              │                          │     prior failures
              └────────────┬────────────┘
                           │ exhausted
                           ▼
              ┌─────────────────────────┐
              │  Tier 4: PAUSE           │ ◄── Human intervention needed
              │  EventReqPaused          │     Dashboard shows alert
              │  $ nxd approve <story>   │     User unblocks manually
              └─────────────────────────┘
```

### 2.4 Runtime Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                       Runtime Registry                                │
│                                                                      │
│  ┌─────────── CLI Runtimes ───────────────────────────────────────┐  │
│  │                                                                 │  │
│  │  ┌─────────┐   ┌──────────────┐   ┌──────────┐                │  │
│  │  │  Aider   │   │ Claude Code  │   │  Codex   │                │  │
│  │  │ (Ollama) │   │ (Anthropic)  │   │ (OpenAI) │                │  │
│  │  └────┬─────┘   └──────┬───────┘   └────┬─────┘                │  │
│  │       │                │                  │                      │  │
│  │       └──────────┬─────┴──────────────────┘                     │  │
│  │                  │                                               │  │
│  │                  ▼                                               │  │
│  │          tmux session                                            │  │
│  │          ├── idle detection (pattern match)                     │  │
│  │          ├── permission auto-response                           │  │
│  │          └── output log capture                                  │  │
│  └─────────────────────────────────────────────────────────────────┘  │
│                                                                      │
│  ┌─────────── Native Runtime ─────────────────────────────────────┐  │
│  │                                                                 │  │
│  │  GemmaRuntime                                                   │  │
│  │  ├── In-process goroutine (no tmux dependency)                 │  │
│  │  ├── LLM function calling with tools:                          │  │
│  │  │   read_file, write_file, edit_file, run_command,            │  │
│  │  │   task_complete, write_scratchboard, read_scratchboard      │  │
│  │  ├── Command allowlist (sandboxed execution)                   │  │
│  │  ├── Criteria evaluation on task_complete                      │  │
│  │  ├── Progress callbacks → event emission                       │  │
│  │  └── Concurrency: SemaphoreClient wraps LLM (default 1)       │  │
│  │                                                                 │  │
│  │  SemaphoreClient ──► Ollama API (local GPU)                    │  │
│  │  Max concurrent calls = config.runtimes.gemma.concurrency      │  │
│  └─────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

### 2.5 Web Dashboard Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│                         Browser Client                                │
│                                                                      │
│  app.js: Single-page dashboard                                       │
│  ├── DAG SVG visualization (story dependency graph)                  │
│  ├── Agent status cards (active sessions, iterations)                │
│  ├── Pipeline progress (planned → in_progress → review → merged)     │
│  ├── Story detail panels (diff, logs, criteria results)              │
│  ├── Review gate controls (approve/reject)                           │
│  ├── Metrics panel (tokens, costs, durations)                        │
│  ├── Recovery log (escalations, retries, controller actions)         │
│  └── Investigation viewer                                            │
└────────────────────────────────┬─────────────────────────────────────┘
                                 │ WebSocket /ws
                                 ▼
┌──────────────────────────────────────────────────────────────────────┐
│                        WebSocket Hub                                  │
│                                                                      │
│  On connect:                                                          │
│    sendState() ──► Full StateSnapshot to new client                  │
│                                                                      │
│  EventBus.Subscribe() ──► Instant event push (no delay)              │
│                                                                      │
│  Run() ticker (5s):                                                   │
│    ├── BuildSnapshot(es, ps, cfg) ──► StateSnapshot                  │
│    ├── Diff: new events since last tick                               │
│    └── Broadcast to all connected clients                             │
│                                                                      │
│  HandleCommand(msg):                                                  │
│    approve_story, reject_story, reprioritize,                        │
│    view_diff, view_logs, ...                                          │
└──────────────────────────────────────────────────────────────────────┘
              ▲                              ▲
              │ Subscribe()                  │ Publish()
┌─────────────┴──────────────────────────────┴─────────────────────────┐
│                         EventBus                                      │
│  In-process pub/sub for instant WebSocket delivery                   │
│  ├── subscribers: map[string]chan Event                               │
│  ├── Publish(event) ──► non-blocking send to all channels            │
│  ├── Subscribe(id) ──► buffered channel (100 events)                 │
│  └── Slow consumer protection: drop if channel full                  │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 3. Package Dependency Graph

> **Status: IMPLEMENTED** | **Verified against:** commit `3e9d25a` (2026-04-15)

### 3.1 Dependency Map

```
                                    cmd/nxd/main.go
                                         │
                                         ▼
                                    ┌─────────┐
                                    │   cli    │ (20 internal imports)
                                    └────┬─────┘
                           ┌─────────────┼─────────────┐
                           ▼             ▼             ▼
                    ┌──────────┐   ┌─────────┐   ┌─────────┐
                    │  engine  │   │   web   │   │ runtime  │
                    │(15 deps) │   │(4 deps) │   │ (5 deps) │
                    └────┬─────┘   └────┬────┘   └────┬─────┘
              ┌──────────┼──────┐       │        ┌────┼────────┐
              ▼          ▼      ▼       ▼        ▼    ▼        ▼
         ┌────────┐ ┌──────┐ ┌─────┐ ┌──────┐ ┌──────┐ ┌──────────┐
         │ agent  │ │ git  │ │graph│ │memory│ │config│ │   tmux   │
         │(2 deps)│ │ (0)  │ │ (0) │ │ (0)  │ │ (0)  │ │   (0)   │
         └───┬────┘ └──────┘ └─────┘ └──────┘ └──────┘ └──────────┘
             ▼
         ┌──────┐
         │ llm  │──► update
         └──────┘
```

### 3.2 Full Import Table

| Package | Imports | Role |
|---------|---------|------|
| **cli** | agent, artifact, codegraph, config, criteria, dashboard, engine, git, graph, llm, memory, metrics, plugin, repolearn, runtime, scratchboard, state, tmux, update, web | Command orchestration layer |
| **engine** | agent, artifact, codegraph, config, criteria, git, graph, llm, memory, metrics, plugin, repolearn, runtime, scratchboard, state | Core orchestration pipeline |
| **runtime** | config, criteria, llm, scratchboard, tmux | Agent execution (CLI + native) |
| **web** | graph, memory, metrics, state | Dashboard + WebSocket hub |
| **agent** | config, llm | Role definitions + prompt templates |
| **metrics** | llm | Token tracking + aggregation |
| **plugin** | config | Extension system |
| **repolearn** | llm | Repository profiling |
| **dashboard** | state | TUI status display |
| **llm** | update | LLM client abstraction |
| **artifact** | *(none)* | Per-story persistence |
| **codegraph** | *(none)* | Code dependency analysis |
| **config** | *(none)* | Configuration types |
| **criteria** | *(none)* | Success check evaluation |
| **git** | *(none)* | Worktree + merge operations |
| **graph** | *(none)* | DAG + topological sort |
| **memory** | *(none)* | MemPalace semantic memory |
| **scratchboard** | *(none)* | Cross-agent knowledge JSONL |
| **state** | *(none)* | Event/projection store |
| **tmux** | *(none)* | Terminal multiplexer control |
| **update** | *(none)* | Version checking |

### 3.3 Architectural Observations

- **11 leaf packages** with zero internal dependencies — strong foundation layer
- **2 hub packages** (cli: 20 deps, engine: 15 deps) — expected for orchestrator architecture
- **No circular dependencies** — clean DAG; enforced by Go compiler
- **Clear layering:** leaves → mid-tier (agent, runtime, web) → hubs (engine, cli)
- **Plugin isolation:** plugin only imports config, not engine — prevents coupling

---

## 4. Revenue Pipeline Design

> **Status: IMPLEMENTED** — Cost engine, token tracking, and report generation are production-ready

### 4.1 Cost Model Architecture

NXD implements a dual-mode cost engine that enables both **client quoting** (pre-execution estimates) and **margin tracking** (post-execution actuals).

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Revenue Pipeline                                 │
│                                                                     │
│  ┌──────────────┐    ┌──────────────────┐    ┌───────────────────┐ │
│  │  Pre-Estimate │    │  Execution Phase  │    │  Post-Delivery    │ │
│  │  (nxd estimate)│    │  (nxd resume)     │    │  (nxd report)    │ │
│  └──────┬───────┘    └────────┬─────────┘    └────────┬──────────┘ │
│         │                     │                        │            │
│         ▼                     ▼                        ▼            │
│  CalculateCost()       MetricsClient wraps     CalculateCostWith   │
│  Stories → Points      every LLM call →        Tokens() → actual   │
│  Points → Hours        records to metrics.     LLM cost + margin   │
│  Hours × Rate =        jsonl                                        │
│  QuoteLow/QuoteHigh                                                 │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                   Pricing Formula                             │  │
│  │                                                               │  │
│  │  ServiceCost = Σ(story_hours × hourly_rate)                  │  │
│  │                                                               │  │
│  │  LLMCost = Σ(input_tokens/1000 × input_rate                 │  │
│  │              + output_tokens/1000 × output_rate)              │  │
│  │                                                               │  │
│  │  Margin% = (1 - LLMCost / QuoteHigh) × 100                  │  │
│  │                                                               │  │
│  │  Revenue = QuoteHigh - LLMCost                               │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### 4.2 Fibonacci Complexity-to-Hours Mapping

| Story Points | Hours (Low) | Hours (High) | Cost @ $150/hr (Low) | Cost @ $150/hr (High) |
|:---:|:---:|:---:|:---:|:---:|
| 1 | 0.25 | 0.5 | $37.50 | $75.00 |
| 2 | 0.5 | 1.0 | $75.00 | $150.00 |
| 3 | 1.0 | 2.0 | $150.00 | $300.00 |
| 5 | 2.0 | 4.0 | $300.00 | $600.00 |
| 8 | 4.0 | 8.0 | $600.00 | $1,200.00 |
| 13 | 8.0 | 13.0 | $1,200.00 | $1,950.00 |

### 4.3 LLM Cost Modes

| Mode | Behavior | Use Case |
|------|----------|----------|
| `subscription` | LLMCost = $0 always | Flat-rate Ollama/local models |
| `per_token` | Cost from actual token usage | Cloud API models (Anthropic, OpenAI, Google) |

### 4.4 Token Cost Configuration (per 1K tokens)

| Model | Input/1K | Output/1K | Typical Story Cost |
|-------|----------|-----------|-------------------|
| gemma4 (local) | $0.00 | $0.00 | $0.00 |
| claude-opus | $0.015 | $0.075 | ~$2.50 |
| claude-sonnet | $0.003 | $0.015 | ~$0.50 |
| deepseek-coder-v2 | $0.0075 | $0.015 | ~$0.30 |

### 4.5 Report Delivery Structure

```go
ReportData {
    Status: DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT
    Stories: [{
        Title, Status, Complexity, Wave,
        EscalationCount, RetryCount, Duration,
        PRUrl, PRNumber
    }]
    Effort: Estimate {
        Summary: {
            StoryCount, TotalPoints,
            HoursLow, HoursHigh,
            QuoteLow, QuoteHigh,
            LLMCost, MarginPercent,
            Rate, Currency
        }
    }
    Timeline: [{ Timestamp, EventType, StoryID, Description }]
    AgentStats: [{ AgentID, StoriesWorked, Escalations }]
}
```

---

## 5. Bayesian Feedback Model

> **Status: PROPOSED** — Scoring infrastructure exists (`agent/scoring.go`); Bayesian routing is a design proposal, not yet implemented

### 5.1 Current State

NXD has the **infrastructure** for feedback-driven routing but does **not yet implement** adaptive behavior. The following components exist:

| Component | Status | Location |
|-----------|--------|----------|
| `AgentReputation` scoring | Defined, unused | `agent/scoring.go` |
| `ComputeReputation()` | Implemented | `agent/scoring.go` |
| `OverallScore()` weighted formula | Implemented | `agent/scoring.go` |
| Failure pattern analysis | Deterministic | `engine/failure_analyzer.go` |
| Metrics recording | Active | `metrics/recorder.go` |
| Routing decisions | Static (config thresholds) | `engine/dispatcher.go` |

### 5.2 Proposed Bayesian Routing Model

The scoring infrastructure in `agent/scoring.go` provides the foundation for a Bayesian update model that would dynamically adjust routing confidence based on observed outcomes.

#### Prior Distribution

Each agent role starts with a **prior belief** about its success probability for a given complexity tier:

```
P(success | role, complexity) ~ Beta(α₀, β₀)
```

Initial priors (uninformative):

| Role | Complexity ≤ 3 | Complexity 4-5 | Complexity > 5 |
|------|:-:|:-:|:-:|
| Junior | Beta(8, 2) | Beta(3, 7) | Beta(1, 9) |
| Intermediate | Beta(6, 4) | Beta(7, 3) | Beta(3, 7) |
| Senior | Beta(5, 5) | Beta(6, 4) | Beta(8, 2) |

Where `α` = expected successes, `β` = expected failures out of 10 observations.

#### Bayesian Update Rule

After each story execution, update the role's prior for that complexity tier:

```
If story passes QA without escalation:
    α_new = α + 1    (success)
    β_new = β         (no change)

If story fails and gets escalated:
    α_new = α         (no change)
    β_new = β + 1    (failure)

If story passes after retry (same role):
    α_new = α + 0.5  (partial credit)
    β_new = β + 0.5  (partial penalty)
```

#### Posterior Success Probability

```
P(success | role, complexity, history) = α / (α + β)

Variance = (α × β) / ((α + β)² × (α + β + 1))
```

#### Routing Decision

Replace static `RouteByComplexity()` with:

```
BayesianRoute(complexity, available_roles):
    for each role in available_roles:
        p = α[role][complexity] / (α[role][complexity] + β[role][complexity])
        confidence = 1 - variance[role][complexity]

    # Select role with highest expected value weighted by confidence
    score = p × confidence × (1 - cost_weight × role_cost_factor)

    return role with max(score)
```

Where `cost_weight` biases toward cheaper roles when success probabilities are similar.

#### Integration with Existing Scoring

The `OverallScore()` formula in `agent/scoring.go` maps naturally:

```go
// Current: static weighted score
OverallScore = Quality×0.5 + Reliability×0.3 + Speed×0.2

// Proposed: Bayesian posterior replaces Quality and Reliability
BayesianScore = P(success)×0.5 + P(no_escalation)×0.3 + SpeedNorm×0.2
```

#### Decay Factor

To prevent early observations from dominating indefinitely, apply exponential decay:

```
α_decayed = α₀ + Σ(outcome_i × λ^(t_now - t_i))
β_decayed = β₀ + Σ((1 - outcome_i) × λ^(t_now - t_i))

where λ = 0.95 (per-story decay)
```

This means observations older than ~20 stories contribute < 36% weight, allowing the system to adapt to changing model capabilities.

#### Expected Impact

| Metric | Before (static) | After (Bayesian) | Improvement |
|--------|:---:|:---:|:---:|
| First-attempt success rate | ~60% | ~78% | +30% |
| Avg escalations per requirement | 1.8 | 0.7 | -61% |
| Avg stories-to-completion time | baseline | -25% | faster |
| LLM cost per requirement | baseline | -20% | cheaper (fewer retries) |

---

## 6. Executor Design Rationale

> **Status: IMPLEMENTED** | **Verified against:** commit `3e9d25a` (2026-04-15)

### 6.1 Why Dual Runtime?

The Executor supports two fundamentally different execution models. Each solves a distinct deployment scenario:

| Design Decision | CLI Runtime | Native Runtime |
|-----------------|-------------|----------------|
| **Target user** | Teams with existing aider/claude-code setups | Self-hosted Ollama users |
| **Process model** | External process in tmux session | In-process goroutine |
| **Monitoring** | Output polling via idle pattern match | Event callbacks + progress hooks |
| **Model support** | Any model the CLI tool supports | Ollama-compatible models |
| **Sandboxing** | Delegated to the CLI tool | Command allowlist enforcement |
| **Debugging** | Session logs, tmux attach | Trace JSONL, progress events |
| **Cost** | Depends on external tool's API usage | Controlled via SemaphoreClient |

### 6.2 Architectural Decisions in Executor

#### Decision 1: Semaphore-Wrapped LLM Client

**Problem:** Multiple native agents in a wave share one GPU.

**Solution:** `SemaphoreClient` wraps the LLM client with a counting semaphore (default concurrency: 1). All agents in a wave share one semaphore instance created in `SpawnAll()`.

**Why not per-agent rate limiting?** Per-agent limits don't prevent concurrent GPU saturation. A shared semaphore ensures at most N LLM calls proceed simultaneously across all agents.

```
SpawnAll() creates 1 SemaphoreClient
    └─► shared by all native goroutines in this wave
    └─► concurrency = config.runtimes.gemma.concurrency (default 1)
```

#### Decision 2: Wave Brief for Parallel Awareness

**Problem:** Parallel agents in the same wave might unknowingly modify shared files, creating merge conflicts.

**Solution:** `BuildWaveBrief()` generates a context block injected into each agent's prompt listing all parallel stories and their owned files.

```go
WaveBrief: "Stories running in parallel with you:
  - story-002: Add OAuth middleware (owns: auth/middleware.go, auth/config.go)
  - story-003: Add rate limiting (owns: middleware/ratelimit.go)
  DO NOT modify files owned by other stories."
```

**Why not file locking?** File locks would break the LLM's ability to reason about the codebase holistically. Prompt-based awareness is softer but preserves the agent's full context.

#### Decision 3: Worktree Isolation

**Problem:** Parallel agents modifying the same repository creates race conditions on git state.

**Solution:** Each agent gets its own git worktree on a dedicated branch.

```
repo/
├── .git/                          (shared)
├── src/                           (main working tree)
└── ~/.nxd/worktrees/
    ├── story-001/                 (branch: nxd/story-001)
    ├── story-002/                 (branch: nxd/story-002)
    └── story-003/                 (branch: nxd/story-003)
```

**Why worktrees over clones?** Worktrees share the same `.git` directory — no disk duplication, instant branch creation, and `git merge` resolves against the same object store.

#### Decision 4: Controller with Cancellable Contexts

**Problem:** Agents can get stuck in infinite loops or unproductive reasoning.

**Solution:** Each native agent spawns with a `context.WithCancel()`. The Controller's `RegisterCancel(storyID, cancelFunc)` stores the cancel function, allowing external termination.

```
Controller.Tick() detects story stuck > 300s
    └─► Supervisor.Review() confirms drift
    └─► RegisterCancel[storyID]() cancels the context
    └─► Goroutine exits cleanly via ctx.Done()
    └─► Controller can restart with resetStoryToDraft()
```

**Why not process kill?** Works for CLI runtimes (tmux kill), but goroutines require cooperative cancellation via Go contexts. The unified approach (cancel function per agent) works for both.

#### Decision 5: Attempt History for Retry Intelligence

**Problem:** Retried agents repeat the same mistakes without learning from failures.

**Solution:** `AttemptTracker` queries the event store for all prior STORY_STARTED events for a story. On retry, `RenderGoalWithAttempts()` injects full attempt history (role, outcome, error message) into the prompt.

```
Attempt 1 (Junior): FAILED — "undefined: oauth.NewClient"
Attempt 2 (Intermediate): FAILED — "test TestOAuth/token_refresh failed"
Attempt 3 (Senior): Current attempt with full failure context
```

### 6.3 Spawn Flow Summary

```
SpawnAll(repoDir, assignments, stories)
    │
    ├─► BuildWaveBrief (parallel story awareness)
    ├─► buildNativeClient (shared SemaphoreClient)
    │
    └─► for each assignment:
        spawn(repoDir, assignment, story, waveBrief, nativeClient)
            │
            ├─► CreateWorktree(repoDir, worktreePath, branch)
            ├─► runtimeForRole(role) → select runtime
            │
            ├─ [CLI path]─────────────────────────────────────┐
            │   ├─► Load RepoProfile or ScanRepo              │
            │   ├─► Build PromptContext (tech stack, commands)  │
            │   ├─► Query MemPalace for prior work context     │
            │   ├─► GoalPrompt / RenderGoalWithAttempts        │
            │   ├─► rt.Spawn(SessionConfig)                    │
            │   └─► Emit EventStoryStarted                     │
            │                                                   │
            └─ [Native path]──────────────────────────────────┐
                ├─► Build PromptContext + MemPalace query       │
                ├─► Write LaunchConfig artifact                 │
                ├─► Emit EventStoryStarted                     │
                └─► go func():                                  │
                    ├─► NewGemmaRuntime(nativeClient, config)  │
                    ├─► Wire: Scratchboard, Criteria, Progress │
                    ├─► RegisterCancel with Controller          │
                    ├─► Execute(ctx, workDir, model, prompt)   │
                    └─► Emit EventStoryCompleted               │
```

---

## 7. 3-Phase Deployment Strategy

> **Status: Phase 1 IMPLEMENTED, Phases 2-3 PROPOSED**

### Phase 1: Local-First (Current — MVP)

**Duration:** Months 1-3 | **Target:** Individual developers and small teams

```
┌─────────────────────────────────────────────────────┐
│                  Developer Machine                    │
│                                                      │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐ │
│  │  Ollama   │  │  SQLite  │  │  Git Worktrees    │ │
│  │  (GPU)    │  │  (state) │  │  (per-story)      │ │
│  └──────────┘  └──────────┘  └───────────────────┘ │
│                                                      │
│  $ nxd req "..." → local planning → local execution  │
│  $ nxd resume    → watch agents → review → merge     │
│                                                      │
│  Dashboard: localhost:8080 (WebSocket)               │
└─────────────────────────────────────────────────────┘
```

**Characteristics:**
- Zero infrastructure cost (local Ollama, free models)
- Single-user, single-machine
- State in `~/.nxd/` (SQLite + JSONL)
- No network dependency (mode: local merge)
- CLI-driven workflow

**Success Criteria:**
- 80%+ test coverage
- `nxd req` → merged code in < 30 min for 5-point stories
- Successful execution with gemma4, deepseek-coder-v2
- Dashboard shows real-time progress
- Cost reports accurate within 5%

### Phase 2: Team Server (Next — Growth)

**Duration:** Months 4-8 | **Target:** Engineering teams (5-20 developers)

```
┌──────────────────────────────────────────────────────────────────┐
│                        NXD Server                                 │
│                                                                  │
│  ┌────────────┐   ┌──────────────┐   ┌───────────────────────┐  │
│  │  REST API   │   │  PostgreSQL   │   │  Redis (queue/cache)  │  │
│  │  /api/v1    │   │  (state)      │   │  (job scheduling)     │  │
│  └──────┬──────┘   └──────────────┘   └───────────────────────┘  │
│         │                                                         │
│  ┌──────┴──────┐   ┌──────────────┐   ┌───────────────────────┐  │
│  │  Worker Pool │   │  Ollama/Cloud│   │  GitHub Integration   │  │
│  │  (goroutines)│   │  LLM Provider│   │  (PR, webhooks)       │  │
│  └─────────────┘   └──────────────┘   └───────────────────────┘  │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────────┐│
│  │  Multi-tenant: team_id scopes all queries                    ││
│  │  Auth: GitHub OAuth or API keys                              ││
│  │  Queue: requirements queued, processed FIFO per team         ││
│  └──────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────┘
         ▲                           ▲
         │ REST/WebSocket            │ git push + webhook
┌────────┴────────┐         ┌───────┴──────────┐
│  Web Dashboard   │         │  GitHub/GitLab    │
│  (team view)     │         │  (PR integration) │
└─────────────────┘         └──────────────────┘
```

**New Capabilities:**
- Multi-user concurrent access (team_id scoping)
- PostgreSQL replaces SQLite (durability, concurrent writes)
- Redis job queue (requirement scheduling, priority)
- GitHub integration (PR mode default, webhook triggers)
- Cloud LLM support (Anthropic, OpenAI) with API key management
- Usage dashboards per user/team
- Role-based access control (admin, developer, viewer)

**Migration Path:**
1. Add `state/postgres.go` implementing `EventStore` + `ProjectionStore` interfaces
2. Add `/api/v1` REST layer wrapping existing CLI commands
3. Redis queue wraps `Dispatcher.DispatchWave()` for async processing
4. GitHub OAuth for authentication; team_id injected per request
5. Existing WebSocket hub serves team-scoped snapshots

### Phase 3: Platform (Future — Scale)

**Duration:** Months 9-18 | **Target:** Organizations (50+ developers), SaaS customers

```
┌──────────────────────────────────────────────────────────────────────┐
│                        NXD Platform                                   │
│                                                                      │
│  ┌─────────────────┐   ┌──────────────────┐   ┌──────────────────┐  │
│  │  API Gateway     │   │  Auth Service     │   │  Billing Service  │  │
│  │  (rate limiting) │   │  (OAuth, SAML)    │   │  (Stripe, usage)  │  │
│  └────────┬────────┘   └──────────────────┘   └──────────────────┘  │
│           │                                                          │
│  ┌────────┴────────────────────────────────────────────────────────┐ │
│  │                    Orchestration Service                         │ │
│  │  (NXD engine — horizontal scaling via requirement partitioning) │ │
│  └────────┬────────────────────────────────────────────────────────┘ │
│           │                                                          │
│  ┌────────┼────────────────────────────────────────────────────────┐ │
│  │  ┌─────┴─────┐  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │ │
│  │  │ Worker Pool│  │PostgreSQL│  │  Redis   │  │ Object Store │  │ │
│  │  │ (K8s pods) │  │ (Aurora) │  │ (ElastiC)│  │ (S3/artifacts)│  │ │
│  │  └───────────┘  └──────────┘  └──────────┘  └──────────────┘  │ │
│  └─────────────────────────────────────────────────────────────────┘ │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────────┐│
│  │  Multi-org tenancy │ SSO/SAML │ SOC2 audit logs │ SLA monitoring││
│  │  Usage-based billing │ Model marketplace │ Bayesian routing      ││
│  │  Custom plugins per org │ Dedicated worker pools │ VPC peering   ││
│  └──────────────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────────────┘
```

**New Capabilities:**
- Kubernetes-native with horizontal pod autoscaling
- Multi-org tenancy with data isolation
- Usage-based billing (per story point, per token)
- Model marketplace (bring-your-own-model + managed models)
- SSO/SAML enterprise authentication
- SOC2-compliant audit logging (event store is already append-only)
- SLA monitoring with Bayesian quality guarantees
- Custom plugin marketplace
- Dedicated worker pools for high-priority customers
- Artifact storage in S3/GCS (replace local filesystem)

### Deployment Phase Comparison

| Capability | Phase 1 (Local) | Phase 2 (Team) | Phase 3 (Platform) |
|-----------|:---:|:---:|:---:|
| Users | 1 | 5-20 | 50-500+ |
| State backend | SQLite | PostgreSQL | Aurora |
| LLM provider | Ollama (local) | Ollama + Cloud | Multi-provider |
| Git integration | Local merge | GitHub PRs | GitHub/GitLab/Bitbucket |
| Authentication | None | GitHub OAuth | SSO/SAML |
| Billing | Cost reports only | Team usage tracking | Usage-based SaaS |
| Scaling | Single machine | Single server | Kubernetes |
| Monitoring | CLI + dashboard | Team dashboard | SLA monitoring |
| Plugin system | Local files | Shared team plugins | Plugin marketplace |
| Infrastructure cost | $0 | ~$200/mo | $2K-20K/mo |

---

## 8. Technical Decision Table

> **Status: IMPLEMENTED** — All decisions reflect current codebase state

### 8.1 Architecture Decisions

| # | Decision | Chosen | Alternatives Considered | Rationale |
|---|----------|--------|------------------------|-----------|
| AD-1 | State management | Event Sourcing + CQRS | Direct CRUD, State machine | Full audit trail; time-travel debugging; natural fit for multi-agent systems where every state change matters |
| AD-2 | Storage backend | SQLite (local), PostgreSQL (server) | DoltDB, CockroachDB | SQLite: zero-config for local. PostgreSQL: proven at scale. DoltDB explored (git-for-data) but added complexity without clear benefit |
| AD-3 | Agent isolation | Git worktrees | Git clones, Docker containers, Branch-only | Worktrees share `.git` (no disk duplication), instant creation, and native merge support. Docker adds overhead for code-editing agents |
| AD-4 | Parallelism model | Wave-based DAG dispatch | Fully parallel, Sequential only, Kanban board | DAG captures real dependencies. Waves batch parallelizable work. Better than fully parallel (merge conflicts) or sequential (slow) |
| AD-5 | LLM abstraction | Interface + providers | Single provider, LangChain, LiteLLM | Clean interface enables test doubles (ReplayClient, DryRunClient, ErrorClient). No external dependency for abstraction |
| AD-6 | CLI runtime | tmux sessions | Docker exec, SSH, subprocess pipes | tmux: observable (attach for debugging), persistent across disconnects, supports any CLI tool. Docker: too heavy for iterative coding |
| AD-7 | Native runtime | In-process goroutines | Separate processes, WebAssembly, gRPC services | Goroutines: zero serialization overhead, shared memory for scratchboard, cancellable via Go context |
| AD-8 | Concurrency control | Semaphore client | Token bucket, Queue per agent, No limit | Semaphore is simplest correct solution for GPU-limited scenarios. One shared semaphore per wave prevents resource exhaustion |
| AD-9 | Conflict resolution | Rebase + auto-resolve | Merge commits, Manual only, Squash | Rebase: linear history, easier to review. Auto-resolve for non-overlapping hunks. Fall back to manual for ambiguous conflicts |
| AD-10 | Plugin system | File-based (MD/YAML/scripts) | Go plugins, gRPC, WebAssembly | File-based: no compilation needed, version-controllable, editable by non-engineers. Go plugins have shared symbol issues |
| AD-11 | Dashboard transport | WebSocket + EventBus | SSE, Polling, gRPC streaming | WebSocket: bidirectional (commands from browser), low latency. EventBus: in-process pub/sub avoids network hop for local |
| AD-12 | Cost model | Fibonacci points × hourly rate | T-shirt sizes, Linear hours, Story count | Fibonacci: well-understood by engineering orgs, naturally captures estimation uncertainty. Dual (low/high) range avoids false precision |

### 8.2 Language & Framework Decisions

| # | Decision | Chosen | Rationale |
|---|----------|--------|-----------|
| LF-1 | Language | Go 1.26 | Single binary, goroutines for native runtime, strong concurrency primitives, fast compilation |
| LF-2 | Database | SQLite (mattn/go-sqlite3) | Embedded, zero-config, sufficient for single-user. Easy migration path to PostgreSQL |
| LF-3 | CLI framework | Cobra | Industry standard for Go CLIs, subcommand support, flag parsing |
| LF-4 | Web framework | net/http (stdlib) | No external dependency needed for WebSocket + static file serving |
| LF-5 | LLM clients | Custom per-provider | Full control over retry logic, error classification, token tracking. No LangChain/LiteLLM dependency |
| LF-6 | Testing | Go testing + race detector | Native tooling, `go test -race` catches concurrency bugs, no test framework dependency |

---

## 9. Risk Assessment

> **Status: IMPLEMENTED** (risk identification) + **PROPOSED** (some mitigations are future work)

### 9.1 Technical Risks

| Risk | Likelihood | Impact | Severity | Mitigation |
|------|:---:|:---:|:---:|------------|
| **LLM quality regression** — Model updates degrade agent output quality | High | High | **Critical** | Criteria-based QA catches regressions. ReplayClient for deterministic regression tests. Multi-provider fallback via FallbackClient |
| **Merge conflict cascade** — Wave N merge failures block wave N+1 | Medium | High | **High** | Sequential-first dispatch for shared files. OwnedFiles overlap filtering. Serialized merge with mutex. Rebase auto-resolution |
| **GPU saturation** — Concurrent agents overwhelm single Ollama instance | High | Medium | **High** | SemaphoreClient limits concurrent calls. Configurable concurrency per runtime. Monitoring via metrics.jsonl |
| **Stuck agent loops** — Agent enters infinite reasoning without progress | Medium | Medium | **Medium** | Controller with stuck_threshold_s detection. Supervisor LLM confirms drift. Auto-cancel/restart with cooldowns |
| **Context window overflow** — Large codebases exceed LLM context limits | Medium | Medium | **Medium** | context_freshness_tokens config limits prompt size. RepoProfile pre-summarizes tech stack. Scratchboard for incremental knowledge |
| **Event store growth** — Unbounded append-only storage fills disk | Low | Medium | **Medium** | log_retention_days for cleanup. Archive to dolt/file. GC command (`nxd gc`) prunes old events |
| **Prompt injection** — Malicious code in repo tricks agent into harmful actions | Low | High | **Medium** | Command allowlist in native runtime. Worktree isolation limits blast radius. QA criteria catch unexpected changes |
| **State corruption** — Concurrent writes corrupt SQLite database | Low | High | **Medium** | SQLite WAL mode for concurrent reads. Mutex-protected writes in ProjectionStore. Event store is append-only (corruption-resistant) |
| **tmux session leak** — Failed cleanup leaves orphaned sessions | Medium | Low | **Low** | Cleanup on story completion. `nxd gc` prunes stale sessions. Monitor detects orphans |
| **Version skew** — Mixed NXD versions read/write same state dir | Low | Medium | **Low** | State schema includes version field. Fail fast on incompatible versions |

### 9.2 Business Risks

| Risk | Likelihood | Impact | Severity | Mitigation |
|------|:---:|:---:|:---:|------------|
| **LLM cost volatility** — Provider pricing changes break margin model | Medium | High | **High** | Dual-mode billing (subscription vs per_token). Local Ollama as zero-cost fallback. Configurable rates per model |
| **Quality perception** — Early users see low first-attempt success rates | High | High | **High** | Bayesian routing (proposed) improves success rate. Transparent escalation chain visible in dashboard. Reports show concerns honestly |
| **Adoption friction** — Complex setup deters new users | Medium | High | **High** | Phase 1 is zero-config (`nxd req` works with defaults). Example nxd.yaml included. Smoke test project for quick validation |
| **Competitive pressure** — Established players (Devin, Cursor, etc.) | High | Medium | **Medium** | Differentiator: self-hosted, multi-agent DAG parallelism, cost transparency. No vendor lock-in |
| **Model dependency** — Reliance on specific model capabilities | Medium | Medium | **Medium** | Provider-agnostic interface. FallbackClient for multi-provider. Works with local models (zero API dependency) |

### 9.3 Risk Heat Map

```
                    L I K E L I H O O D
                Low         Medium        High
         ┌──────────────┬──────────────┬──────────────┐
  High   │ Prompt       │ Merge        │ LLM quality  │
         │ injection    │ conflicts    │ regression   │
I        │ State        │ LLM cost     │ Quality      │
M        │ corruption   │ volatility   │ perception   │
P        ├──────────────┼──────────────┼──────────────┤
A  Med   │ Version      │ Context      │ GPU          │
C        │ skew         │ overflow     │ saturation   │
T        │              │ Stuck agents │ Competitive  │
         │              │ Model depend.│ pressure     │
         ├──────────────┼──────────────┼──────────────┤
  Low    │              │ tmux leak    │ Adoption     │
         │              │ Event growth │ friction     │
         └──────────────┴──────────────┴──────────────┘
```

---

## 10. Revenue Projections

> **Status: PROPOSED** — Pricing models and projections are design proposals, not live revenue

### 10.1 Pricing Model Options

#### Model A: Per-Story-Point Pricing (Recommended for Phase 2)

| Tier | Price per Story Point | Included Models | Target Customer |
|------|:---:|---|---|
| Free | $0 | Local Ollama only | Individual devs |
| Pro | $5/point | Ollama + 1 cloud provider | Small teams |
| Team | $3/point (volume) | All providers | Engineering teams |
| Enterprise | Custom | Dedicated workers | Large orgs |

#### Model B: Subscription Pricing (Recommended for Phase 3)

| Tier | Monthly Price | Story Points/mo | Overage Rate |
|------|:---:|:---:|:---:|
| Starter | $49/mo | 50 points | $7/point |
| Growth | $199/mo | 250 points | $5/point |
| Scale | $799/mo | 1,200 points | $3/point |
| Enterprise | Custom | Unlimited | N/A |

### 10.2 Unit Economics

**Cost Structure per Story (5-point average):**

| Component | Local (Ollama) | Cloud (Sonnet) | Cloud (Opus) |
|-----------|:---:|:---:|:---:|
| LLM inference | $0.00 | $0.50 | $2.50 |
| Compute (server) | $0.00 | $0.02 | $0.02 |
| Storage (events/artifacts) | $0.00 | $0.001 | $0.001 |
| **Total COGS** | **$0.00** | **$0.52** | **$2.52** |
| Revenue (@ $5/point) | $25.00 | $25.00 | $25.00 |
| **Gross Margin** | **100%** | **97.9%** | **89.9%** |

### 10.3 Growth Projections

**Assumptions:**
- Phase 1 launch: Month 1 (free, local only)
- Phase 2 launch: Month 4 (paid team server)
- Phase 3 launch: Month 10 (SaaS platform)
- Average story points per requirement: 15
- Average requirements per team per month: 8

#### Conservative Scenario

| Month | Phase | Users | Teams | Monthly Story Points | MRR |
|:---:|:---:|:---:|:---:|:---:|:---:|
| 1 | 1 | 10 | — | — | $0 |
| 3 | 1 | 50 | — | — | $0 |
| 4 | 2 | 80 | 3 | 360 | $1,080 |
| 6 | 2 | 150 | 8 | 960 | $2,880 |
| 9 | 2 | 300 | 15 | 1,800 | $5,400 |
| 12 | 3 | 500 | 30 | 3,600 | $14,400 |
| 18 | 3 | 1,200 | 80 | 9,600 | $43,200 |

#### Optimistic Scenario

| Month | Phase | Users | Teams | Monthly Story Points | MRR |
|:---:|:---:|:---:|:---:|:---:|:---:|
| 1 | 1 | 25 | — | — | $0 |
| 3 | 1 | 200 | — | — | $0 |
| 4 | 2 | 400 | 10 | 1,200 | $3,600 |
| 6 | 2 | 800 | 25 | 3,000 | $9,000 |
| 9 | 2 | 1,500 | 50 | 6,000 | $18,000 |
| 12 | 3 | 3,000 | 120 | 14,400 | $57,600 |
| 18 | 3 | 8,000 | 350 | 42,000 | $174,000 |

### 10.4 Revenue Milestones

```
Revenue ($K MRR)
    │
180 │                                               ╱ Optimistic
    │                                             ╱
160 │                                           ╱
    │                                         ╱
140 │                                       ╱
    │                                     ╱
120 │                                   ╱
    │                                 ╱
100 │                               ╱
    │                             ╱
 80 │                           ╱
    │                         ╱
 60 │                     ╱─╱
    │                   ╱    ╱ Conservative
 40 │                 ╱    ╱
    │               ╱   ╱
 20 │            ╱─╱  ╱
    │          ╱    ╱
  0 │─────────╱──╱───────────────────────────
    └──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬──┬
       1  2  3  4  5  6  7  8  9  10 11 12 13 14 15 16 17 18
                            Month

    Phase 1        Phase 2              Phase 3
    (Free/Local)   (Team Server)        (SaaS Platform)
```

### 10.5 Break-Even Analysis

**Phase 2 Server Costs:**
- Compute: $100/mo (4 vCPU, 16GB RAM)
- Database: $50/mo (PostgreSQL managed)
- Storage: $20/mo (artifacts)
- LLM API costs: variable (passed through to customer or Ollama)
- **Fixed overhead: ~$200/mo**

**Break-even:** 4 paying teams x $3/point x 120 points/mo = $1,440 MRR (Month 5)

**Phase 3 Platform Costs:**
- Kubernetes cluster: $1,500/mo
- Aurora PostgreSQL: $500/mo
- Redis/ElastiCache: $200/mo
- S3 storage: $100/mo
- Monitoring/Logging: $200/mo
- **Fixed overhead: ~$2,500/mo**

**Break-even:** 15 teams on Growth plan x $199/mo = $2,985 MRR (Month 11)

### 10.6 Key Revenue Levers

| Lever | Impact | Implementation Effort |
|-------|--------|----------------------|
| **Bayesian routing** → fewer retries → lower COGS | -20% LLM cost | Medium (Section 5) |
| **Model marketplace** → customer BYO model | New revenue stream | High (Phase 3) |
| **Priority queuing** → faster execution for premium tiers | +30% willingness to pay | Low |
| **Audit/compliance reports** → enterprise value-add | +50% enterprise pricing | Medium |
| **Plugin marketplace** → community-contributed extensions | Network effects | High (Phase 3) |

---

## 11. Security Architecture

> **Status: IMPLEMENTED** (with known gaps documented below)

### 11.1 Threat Model

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Threat Surfaces                               │
│                                                                     │
│  ┌──────────────────┐  ┌───────────────────┐  ┌─────────────────┐ │
│  │ LLM Prompt        │  │ Shell Execution    │  │ File System     │ │
│  │ Injection          │  │ (native runtime)   │  │ Access          │ │
│  │                    │  │                    │  │                 │ │
│  │ Malicious code in  │  │ Agent-requested    │  │ read_file,      │ │
│  │ repo tricks agent  │  │ commands via       │  │ write_file,     │ │
│  │ into harmful       │  │ run_command tool   │  │ edit_file tools │ │
│  │ actions            │  │                    │  │                 │ │
│  └────────┬─────────┘  └────────┬──────────┘  └────────┬────────┘ │
│           │                      │                       │          │
│           ▼                      ▼                       ▼          │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │                    Defense Layers                              │  │
│  │                                                                │  │
│  │  Layer 1: Worktree isolation (git worktree per agent)         │  │
│  │  Layer 2: Command allowlist (prefix matching)                 │  │
│  │  Layer 3: Path validation (safePath confines to workDir)      │  │
│  │  Layer 4: Criteria evaluation (QA catches unexpected changes) │  │
│  │  Layer 5: Review pipeline (Senior LLM reviews all diffs)     │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────┐  ┌───────────────────┐  ┌─────────────────┐ │
│  │ API Keys          │  │ Plugin Scripts     │  │ Config Injection│ │
│  │ (env vars, tmux)  │  │ (QA check exec)    │  │ (nxd.yaml)     │ │
│  └──────────────────┘  └───────────────────┘  └─────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
```

### 11.2 Sandboxing Model

#### File System Sandboxing (`runtime/gemma.go:398`)

```go
safePath(relPath, workDir string) (string, error)
// 1. filepath.Join(workDir, relPath) → resolve absolute
// 2. filepath.Clean() → normalize
// 3. strings.HasPrefix(cleaned, workDir+"/") → confine to workDir
// Blocks: ../../etc/passwd, /etc/shadow, symlink escapes
```

Applied to: `read_file`, `write_file`, `edit_file` tools.

**Gap:** Does not resolve symlinks via `filepath.EvalSymlinks()`. A symlink inside the worktree pointing outside could bypass confinement.

#### Command Sandboxing (`runtime/gemma.go:542`)

```go
// Prefix matching against allowlist
for _, pattern := range g.config.CommandAllowlist {
    if strings.HasPrefix(args.Command, pattern) {
        allowed = true
    }
}
// Execution: exec.Command("sh", "-c", args.Command)
```

**Known vulnerability:** Prefix matching allows command chaining. If allowlist contains `"go"`, then `"go test; rm -rf /"` passes the check because `strings.HasPrefix("go test; rm -rf /", "go")` is true.

**Recommended fix:** Parse command into `(binary, args)` and match binary exactly, or use exact command strings in the allowlist.

#### Investigator Sandboxing (`engine/investigator.go:98`)

Same prefix-matching pattern with the same vulnerability. Default allowlist is conservative (read-only commands: `ls`, `grep`, `cat`, `git log`, etc.) but the `sh -c` execution allows chaining if prefixes are loose.

### 11.3 API Key Management

| Aspect | Current State | Risk Level |
|--------|--------------|:---:|
| Storage | Environment variables (`os.Getenv`) | Medium |
| In-memory | Plain string fields on client structs | Medium |
| tmux propagation | `tmux set-environment -g KEY VALUE` | Medium |
| Shell export | `fmt.Sprintf("export %s=%q", key, val)` (quoted) | Low |
| Secret rotation | Manual (no automated rotation) | Medium |
| Logging | Keys not logged (no explicit redaction either) | Low |

**Keys handled:** `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_AI_API_KEY`, `OLLAMA_HOST`

**Recommendation for Phase 2:** Integrate with a secrets manager (HashiCorp Vault, AWS Secrets Manager) or at minimum use encrypted environment files.

### 11.4 Plugin Security

**Critical gap:** Plugin `resolvePath()` accepts absolute paths without validation:

```go
func resolvePath(pluginDir, subdir, file string) string {
    if filepath.IsAbs(file) {
        return file  // ← No confinement check
    }
    return filepath.Join(pluginDir, subdir, file)
}
```

QA check scripts at arbitrary paths are executed via `exec.CommandContext(ctx, check.ScriptPath)`. A malicious `nxd.yaml` could point to any executable.

**Mitigation:** Plugin paths are admin-controlled (config file), not user/agent-controlled. Risk is misconfiguration, not exploitation.

### 11.5 Input Validation Summary

| Component | Validation | Sanitization |
|-----------|-----------|-------------|
| Model names | Regex: `^[a-zA-Z0-9._:/-]+$` | `runtime/sanitize.go` |
| Session names | Regex: `^[a-zA-Z0-9._-]+$` | `runtime/sanitize.go` |
| Shell arguments | Quote with `'...'` + escape embedded `'` | `runtime/sanitize.go` |
| Config enums | Whitelist (backends, log levels, providers) | `config/config.go` |
| Routing thresholds | Range checks (1-13) | `config/config.go` |
| LLM tool calls | Required fields only (no type validation) | `llm/tools.go` |
| Event payloads | No validation (JSON marshal/unmarshal) | None |

### 11.6 Security Gaps Status

| # | Gap | Severity | Status | Location | Resolution |
|---|-----|:---:|:---:|----------|------------|
| SG-1 | Command allowlist uses prefix matching (chainable) | **Critical** | ✅ **FIXED** | `gemma.go`, `investigator.go` | Added `isCommandAllowed()` rejecting shell metacharacters (`;`, `&&`, `||`, `|`, `$(`, `` ` ``, `\n`) + requires `pattern + " "` boundary for prefix match |
| SG-2 | Plugin paths accept absolute paths without confinement | **Critical** | ✅ **FIXED** | `plugin/loader.go` | `resolvePath()` now returns error for `filepath.IsAbs()` and validates resolved path stays under `pluginDir` |
| SG-3 | Story IDs not validated before use as git branch names | **High** | ✅ **FIXED** | `engine/dispatcher.go` | Added `safeStoryIDPattern` regex `^[a-zA-Z0-9._-]+$` validation in `DispatchWave()` |
| SG-4 | LLM tool call arguments lack type/range validation | **High** | ✅ **FIXED** | `llm/tools.go` | `ValidateToolCall()` now reads `properties` from schema and validates argument types (string/number/integer/boolean/object/array) |
| SG-5 | safePath doesn't resolve symlinks | **Medium** | ✅ **FIXED** | `runtime/gemma.go` | Added `filepath.EvalSymlinks()` on existing targets; verifies real path stays under workDir |
| SG-6 | No response length limits on LLM output | **Medium** | ✅ **FIXED** | `llm/client.go`, `runtime/gemma.go` | Added `llm.MaxResponseContentLen` (200K) + `TruncateContent()` applied in native runtime tool loop |
| SG-7 | API keys in plain memory + tmux environment | **Medium** | 🔄 **DEFERRED** | `anthropic.go`, `env.go` | Phase 2 scope — requires secrets manager integration (see Section 11.7) |
| SG-8 | RuntimeDetection regex patterns not validated (ReDoS risk) | **Medium** | ✅ **FIXED** | `config/config.go` | All detection patterns (`IdlePattern`, `PermissionPattern`, `PlanModePattern`) now compiled via `regexp.Compile()` during `Validate()` |

**Summary:** 7 of 8 gaps resolved. SG-7 deferred to Phase 2 (see next section for secrets manager recommendations).

### 11.7 Phase 2 Secrets Manager Integration (SG-7 Resolution)

> **Status: PROPOSED** — Recommended for Phase 2 team server deployment

The current state — API keys loaded via `os.Getenv()`, held as plain strings in client structs, propagated via `tmux set-environment -g` — is acceptable for Phase 1 (single-user, local) but unacceptable for Phase 2 (multi-user team server) or Phase 3 (SaaS).

#### Comparison Matrix

| Option | Self-Hosted | Cost (Team) | Go SDK Quality | Complexity | Best For |
|--------|:---:|:---:|:---:|:---:|----------|
| **HashiCorp Vault** | Yes | Free (OSS) / $1.58/hr (Enterprise) | Official, excellent | High | Enterprise with existing Vault |
| **AWS Secrets Manager** | No | $0.40/secret/mo + $0.05/10K calls | Official (aws-sdk-go-v2) | Low | AWS-native deployments |
| **GCP Secret Manager** | No | $0.06/secret/mo + $0.03/10K calls | Official | Low | GCP-native deployments |
| **Azure Key Vault** | No | $0.03/10K calls | Official | Low | Azure-native deployments |
| **Doppler** | No | $7/user/mo | Community (REST API) | Very Low | Startups, fast onboarding |
| **1Password Connect** | Partial (Connect server) | $19.95/user/mo | Community | Medium | Teams already on 1Password |
| **Infisical** | Yes | Free (OSS) / $0.50/secret/mo | Official | Low | OSS-first teams |
| **SOPS + age/KMS** | Yes | Free | Via library (getsops/sops) | Low | Git-native secret storage |
| **Mozilla SOPS** | Yes | Free (pairs with KMS) | Mature | Low | Declarative config workflows |

#### Recommendation: Three-Tier Strategy

**Tier 1 (Default): Environment-based with validation**
- Keep `os.Getenv()` as fallback
- Add startup validation + redaction in logs
- Suitable for solo developers (Phase 1)

**Tier 2 (Phase 2 default): Infisical or AWS Secrets Manager**
- **Infisical** if team prefers self-hosted OSS (matches NXD's self-hosted ethos)
- **AWS Secrets Manager** if team deploys NXD on AWS anyway
- Both offer:
  - Rotation policies
  - Audit logs (aligns with NXD event-sourced auditability)
  - Per-team scoping via namespaces/paths
  - IAM/RBAC integration

**Tier 3 (Phase 3 Enterprise): HashiCorp Vault**
- Required for SOC2 compliance at scale
- Dynamic secrets (short-lived credentials)
- Transit encryption engine (encrypt/decrypt without storing)
- Integrates with every enterprise identity provider

#### Primary Recommendation: **Infisical**

For Phase 2, **Infisical** is the best first-class integration target:

| Criterion | Why Infisical |
|-----------|--------------|
| **Self-hosted OSS** | Matches NXD's "no vendor lock-in" principle |
| **Free tier** | Teams can self-host without cost barrier |
| **Managed option** | Teams can upgrade to hosted without migration |
| **Simple API** | Go SDK is idiomatic; similar to `os.Getenv()` at call site |
| **Multi-env support** | `dev`, `staging`, `prod` scoping built-in |
| **Audit logs** | Natural pair with NXD event store for compliance |
| **Rotation** | Policy-driven rotation reduces credential lifetime |

#### Proposed Integration Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                    NXD Secrets Layer                              │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  secrets.Provider interface                                 │  │
│  │    Get(ctx, key string) (string, error)                    │  │
│  │    Rotate(ctx, key string) error                           │  │
│  │    Health(ctx) error                                       │  │
│  └────────────────────────────────────────────────────────────┘  │
│                            │                                      │
│     ┌──────────────────────┼──────────────────────┐              │
│     ▼                      ▼                      ▼              │
│  ┌─────────┐        ┌──────────────┐      ┌──────────────┐      │
│  │ EnvProv │        │ Infisical    │      │ VaultProv    │      │
│  │  (fallback)│     │ Provider     │      │ (Phase 3)    │      │
│  └─────────┘        └──────────────┘      └──────────────┘      │
│                                                                  │
│  Config selection:                                                │
│    workspace.secrets.provider: env | infisical | vault | aws     │
│    workspace.secrets.endpoint: https://app.infisical.com         │
│    workspace.secrets.project_id: <uuid>                          │
│    workspace.secrets.cache_ttl_s: 300  # avoid hot-path calls   │
└──────────────────────────────────────────────────────────────────┘
```

#### Implementation Plan (Phase 2)

```go
// internal/secrets/provider.go
package secrets

type Provider interface {
    Get(ctx context.Context, key string) (string, error)
    Rotate(ctx context.Context, key string) error
    Health(ctx context.Context) error
}

// internal/secrets/env.go — fallback/default
type EnvProvider struct{}
func (e *EnvProvider) Get(ctx context.Context, key string) (string, error) {
    v := os.Getenv(key)
    if v == "" {
        return "", fmt.Errorf("secret %q not set in environment", key)
    }
    return v, nil
}

// internal/secrets/infisical.go
type InfisicalProvider struct {
    client    *infisical.Client
    projectID string
    env       string
    cache     *lru.Cache  // 5-min TTL to avoid hot-path API calls
}

// internal/llm/anthropic.go — usage
client := anthropic.New(cfg)
apiKey, err := secrets.Get(ctx, "ANTHROPIC_API_KEY")
// ... key never lands in config YAML or env vars
```

#### Migration Path (backward compatible)

1. **Phase 2.0:** Add `secrets.Provider` interface, `EnvProvider` default (no behavior change)
2. **Phase 2.1:** Add `InfisicalProvider`, gated by `workspace.secrets.provider: infisical` config
3. **Phase 2.2:** Add redaction layer: any log line containing a known secret value is replaced with `***REDACTED***`
4. **Phase 2.3:** Deprecate direct `os.Getenv()` for API keys; warn at startup if found
5. **Phase 3.0:** Add `VaultProvider` for enterprise SOC2 tier

#### Additional Hardening (Phase 2)

| Hardening | Implementation | Effort |
|-----------|---------------|:---:|
| Redact secrets in logs | Middleware wrapping `log.Printf` | Low |
| Remove tmux global env propagation | Pass secrets per-session via stdin | Medium |
| Zero out key strings after use | `crypto/subtle.ConstantTimeCompare` + memset | Medium |
| Rotation alerts | Emit `SECRET_ROTATION_NEEDED` event when TTL near expiry | Low |
| Access audit | Emit `SECRET_ACCESSED` event per `Get()` call | Low |

---

## 12. Data Model & Schema

> **Status: IMPLEMENTED** — SQLite schema with migration support

### 12.1 Entity-Relationship Diagram

```
┌─────────────────┐       ┌─────────────────────────────────────┐
│  requirements    │       │              stories                 │
├─────────────────┤       ├─────────────────────────────────────┤
│ id          PK  │◄──┐   │ id                    PK            │
│ title           │    │   │ req_id                FK ──────────►│
│ description     │    │   │ title                               │
│ status          │    │   │ description                         │
│ repo_path       │    │   │ acceptance_criteria                 │
│ req_type        │    │   │ complexity            INT           │
│ is_existing     │    │   │ status                              │
│ investigation_  │    │   │ agent_id                            │
│   report_json   │    │   │ branch                              │
│ created_at      │    │   │ pr_url                              │
│ updated_at      │    │   │ pr_number             INT           │
└─────────────────┘    │   │ owned_files           JSON[]        │
                       │   │ wave_hint                           │
                       │   │ wave                  INT           │
┌─────────────────┐    │   │ escalation_tier       INT           │
│    agents       │    │   │ split_depth           INT           │
├─────────────────┤    │   │ merged_at             TIMESTAMP?    │
│ id          PK  │    │   │ created_at                          │
│ type            │    │   │ updated_at                          │
│ model           │    │   └─────────────────────────────────────┘
│ runtime         │    │              │
│ status          │    │              │ 1:N
│ current_story_id│    │              ▼
│ session_name    │    │   ┌─────────────────────┐
│ created_at      │    │   │    story_deps        │
│ updated_at      │    │   ├─────────────────────┤
└─────────────────┘    │   │ story_id       PK,FK│
                       │   │ depends_on_id  PK,FK│
                       │   └─────────────────────┘
                       │
                       │   ┌─────────────────────────┐
                       │   │     escalations          │
                       │   ├─────────────────────────┤
                       └───│ story_id           FK   │
                           │ id                 PK   │
                           │ from_agent              │
                           │ reason                  │
                           │ status                  │
                           │ resolution              │
                           │ from_tier          INT  │
                           │ to_tier            INT  │
                           │ created_at              │
                           │ resolved_at             │
                           └─────────────────────────┘

                       ┌─────────────────────────┐
                       │     agent_scores         │
                       ├─────────────────────────┤
                       │ id                 PK   │
                       │ agent_id           FK   │
                       │ story_id           FK   │
                       │ quality            INT  │
                       │ reliability        INT  │
                       │ duration_s         INT  │
                       │ created_at              │
                       └─────────────────────────┘
```

### 12.2 Status State Machines

**Requirement Status:**
```
pending → analyzed → planned → completed
                  ↘ paused ↗     ↓
            pending_review → rejected
                           → archived
```

**Story Status:**
```
draft → estimated → assigned → in_progress → review → qa → pr_submitted → merge_ready → merged
  ↑         ↑                                  │       │                                    ↓
  └─────────┴──────── (review/QA failure) ─────┘───────┘                                archived
  ↓
split (terminal — children created)
```

### 12.3 Event Payload Contracts

Events use `json.RawMessage` payloads. Key contracts:

| Event | Required Payload Fields | Types |
|-------|------------------------|-------|
| `REQ_SUBMITTED` | `id`, `title`, `description`, `repo_path` | all `string` |
| `REQ_CLASSIFIED` | `id`, `req_type`, `is_existing` | `string`, `string`, `bool` |
| `STORY_CREATED` | `id`, `req_id`, `title`, `complexity`, `depends_on`, `owned_files`, `wave_hint` | `string`, `string`, `string`, `int`, `[]string`, `[]string`, `string` |
| `STORY_ASSIGNED` | `agent_id`, `wave` | `string`, `int` |
| `STORY_STARTED` | `worktree_path`, `runtime`, `branch`, `tier`, `role` | all `string` except `tier` (`int`) |
| `STORY_ESCALATED` | `from_tier`, `to_tier`, `reason` | `int`, `int`, `string` |
| `STORY_REWRITTEN` | `changes` (nested: `title`, `description`, `acceptance_criteria`, `complexity`) | `map` with `string`/`int` values |
| `STORY_PR_CREATED` | `pr_number`, `pr_url` | `int`, `string` |

### 12.4 Event Store Formats

**FileStore (`events.jsonl`):**
```json
{"id":"evt-001","type":"STORY_CREATED","timestamp":"2026-04-15T10:30:45Z","agent_id":"planner","story_id":"s-001","payload":{"id":"s-001","req_id":"req-001","title":"Add auth","complexity":5}}
```
One JSON object per line. Append-only. Mutex-protected writes.

**SQLiteStore (`events` table):** Same data indexed by `type`, `agent_id`, `story_id` for fast filtered queries.

### 12.5 Schema Migration Strategy

Current approach: `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` in `initSQL`. Safe for SQLite (idempotent). Each new column added with `NOT NULL DEFAULT ''` or `DEFAULT 0`.

---

## 13. Observability Strategy

> **Status: PARTIALLY IMPLEMENTED** — Metrics recording active; SLIs/SLOs/alerting not yet defined

### 13.1 Current Observability Stack

```
┌──────────────────────────────────────────────────────────────────┐
│                    Observability Layers                            │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Layer 1: Event Store (events.jsonl / SQLite)              │  │
│  │  Every state transition is an event. Full audit trail.     │  │
│  │  Query: es.List(EventFilter{Type, AgentID, StoryID})       │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Layer 2: Metrics (metrics.jsonl)                          │  │
│  │  Per-LLM-call recording via MetricsClient wrapper.         │  │
│  │  Fields: timestamp, req_id, story_id, phase, role,         │  │
│  │          model, tokens_in, tokens_out, duration_ms,        │  │
│  │          success, escalated                                │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Layer 3: Artifact Store (per-story)                       │  │
│  │  launch_config.json, trace_events.jsonl, git_diff.patch,   │  │
│  │  review_result.json, qa_result.json, raw_log.txt           │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Layer 4: Web Dashboard (real-time)                        │  │
│  │  WebSocket push via EventBus. StateSnapshot every 5s.      │  │
│  │  DAG visualization, pipeline counts, agent status.         │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Layer 5: Go log.Printf (structured-ish)                  │  │
│  │  Prefixed: [native-runtime], [monitor], [controller]       │  │
│  │  Not structured JSON. Not leveled beyond log.Printf.       │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

### 13.2 Proposed SLIs / SLOs

| SLI (Service Level Indicator) | Measurement | SLO Target | Phase |
|-------------------------------|-------------|:---:|:---:|
| **First-attempt success rate** | Stories passing QA without escalation / total stories | ≥ 60% | Phase 1 |
| **Requirement completion time** | Time from REQ_SUBMITTED to REQ_COMPLETED (p90) | ≤ 45 min (5-point avg) | Phase 1 |
| **Merge success rate** | Stories merged without conflict resolution / total merges | ≥ 85% | Phase 1 |
| **LLM latency (p95)** | Time per Complete() call | ≤ 30s (local), ≤ 10s (cloud) | Phase 2 |
| **Dashboard availability** | WebSocket connection uptime | ≥ 99.5% | Phase 2 |
| **API availability** | REST endpoint success rate | ≥ 99.9% | Phase 3 |
| **Cost accuracy** | \|Estimated - Actual\| / Actual | ≤ 20% variance | Phase 2 |

### 13.3 Gaps and Recommendations

| Gap | Impact | Recommendation | Effort |
|-----|--------|---------------|:---:|
| No structured logging (JSON) | Hard to parse logs in production | Adopt `slog` (Go 1.21+) with JSON handler | Medium |
| No log levels beyond Printf | Can't filter noise | Use `slog.Info/Warn/Error/Debug` | Medium |
| No alerting on stuck agents | Silent failures | Webhook/email on `CONTROLLER_STUCK_DETECTED` events | Low |
| No metrics aggregation API | Dashboard rebuilds from raw JSONL | Add `GET /api/metrics/summary` endpoint | Medium |
| No distributed tracing | Phase 2/3 debugging difficult | OpenTelemetry spans per story lifecycle | High |
| No health check endpoint | Can't integrate with load balancers | Add `GET /healthz` returning store connectivity | Low |

---

## 14. Capacity Planning

> **Status: PROPOSED** — Based on architectural analysis, not benchmarked

### 14.1 Bottleneck Analysis

```
Bottleneck Chain (single machine):

  GPU (Ollama)          ← Primary bottleneck for native runtime
       │
       ▼
  LLM Concurrency      ← SemaphoreClient (default: 1 concurrent call)
       │
       ▼
  SQLite Write Lock     ← Single-writer; ProjectionStore mutex
       │
       ▼
  Disk I/O              ← events.jsonl append, metrics.jsonl append
       │
       ▼
  Git Operations        ← Worktree creation, rebase, merge (serialized by mergeMu)
       │
       ▼
  Memory                ← LLM context windows, MemPalace embeddings
```

### 14.2 Theoretical Limits (Phase 1, Single Machine)

| Resource | Limit | Bottleneck | Scaling Strategy |
|----------|:---:|-----------|-----------------|
| Concurrent native agents | 1-4 (GPU VRAM dependent) | GPU memory / SemaphoreClient | Increase `concurrency` config |
| Concurrent CLI agents | 8-12 (tmux sessions) | RAM + CPU for external tools | Machine upgrade |
| Stories per requirement | ~20 (practical) | DAG complexity, planning quality | Adjust `max_story_complexity` |
| Waves per requirement | ~10 | Sequential dependencies | Better parallelization in planning |
| Events per hour | ~10,000 | SQLite write throughput | PostgreSQL (Phase 2) |
| Metrics entries per hour | ~5,000 | JSONL append I/O | Buffered writes |
| Worktrees active | 10-15 | Disk space (~50MB each) | `worktree_prune: immediate` |
| WebSocket clients | ~50 | Hub broadcast loop | Connection pooling |

### 14.3 Resource Consumption Estimates

**Per-Story Resource Usage:**

| Component | Memory | Disk | GPU VRAM | Duration |
|-----------|:---:|:---:|:---:|:---:|
| Worktree creation | ~5 MB | ~50 MB | 0 | ~2s |
| Native agent (Gemma) | ~200 MB | ~1 MB (trace) | 4-8 GB | 2-15 min |
| CLI agent (tmux) | ~50 MB | ~5 MB (logs) | 0 (external) | 5-30 min |
| Review LLM call | ~10 MB | ~1 KB | 4-8 GB | 10-30s |
| QA commands | ~100 MB (build) | ~10 MB (artifacts) | 0 | 30s-5 min |
| Merge + rebase | ~5 MB | ~1 MB | 0 | ~5s |

**Per-Requirement Totals (5 stories, 2 waves):**
- Memory peak: ~600 MB (3 concurrent agents + orchestrator)
- Disk: ~300 MB (worktrees) + ~10 MB (state) = ~310 MB
- GPU: 4-8 GB sustained during execution waves
- Duration: 15-60 min (depends on complexity and concurrency)

### 14.4 Phase 2 Scaling Thresholds

| Trigger | Threshold | Action Required |
|---------|-----------|----------------|
| SQLite write contention | > 100 concurrent writes/sec | Migrate to PostgreSQL |
| Metrics file size | > 100 MB / day | Rotate + archive |
| Events file size | > 500 MB | Enable `log_retention_days` |
| Concurrent requirements | > 3 simultaneous | Job queue (Redis) |
| WebSocket clients | > 50 | Connection pooling + horizontal hub |

---

## 15. Testing Architecture

> **Status: IMPLEMENTED** — 65.3% coverage, comprehensive test double hierarchy

### 15.1 Test Double Hierarchy

```
                    llm.Client interface
                         │
           ┌─────────────┼──────────────┬─────────────────┐
           ▼             ▼              ▼                 ▼
    ┌────────────┐ ┌──────────┐  ┌───────────┐    ┌───────────────┐
    │ReplayClient│ │DryRunCli.│  │ErrorClient│    │SemaphoreClient│
    │            │ │          │  │           │    │   (decorator) │
    │ Pre-canned │ │ Role-    │  │ Always    │    │   Wraps any   │
    │ response   │ │ aware    │  │ returns   │    │   Client with │
    │ sequence   │ │ canned   │  │ configured│    │   concurrency │
    │            │ │ responses│  │ error     │    │   limit       │
    │ Records    │ │          │  │           │    │               │
    │ all calls  │ │ Inspects │  │ Tests     │    │ Tests rate    │
    │ for assert │ │ system   │  │ error     │    │ limiting &    │
    │            │ │ prompt   │  │ paths     │    │ cancellation  │
    └────────────┘ └──────────┘  └───────────┘    └───────────────┘
         │              │                                │
    Unit tests     E2E pipeline                    Concurrency
    Component      Dry-run CLI                     tests
    tests          flag
```

| Client | Purpose | Created Via | Used In |
|--------|---------|------------|---------|
| `ReplayClient` | Returns pre-configured responses in sequence | `llm.NewReplayClient(responses...)` | Component tests, `withMockLLM()` |
| `DryRunClient` | Inspects system prompt → role-appropriate JSON | `llm.NewDryRunClient(delay)` | `--dry-run` CLI flag, E2E pipeline tests |
| `ErrorClient` | Always returns configured error | `llm.NewErrorClient(err)` | Error handling tests |
| `SemaphoreClient` | Wraps inner client with concurrency limit | `llm.NewSemaphoreClient(inner, n)` | GPU saturation tests, production |

### 15.2 Mock Injection Pattern

```go
// Package-level function variable (cli/req.go)
var buildLLMClientFunc = buildLLMClientDefault

// Test helper overrides it (cli/orchestration_test.go)
func withMockLLM(t *testing.T, responses ...llm.CompletionResponse) {
    original := buildLLMClientFunc
    t.Cleanup(func() { buildLLMClientFunc = original })
    client := llm.NewReplayClient(responses...)
    buildLLMClientFunc = func(provider string, godmode ...bool) (llm.Client, error) {
        return client, nil
    }
}

// Usage:
withMockLLM(t, plannerResponse, reviewResponse)
out, err := execCmd(t, newReqCmd(), env.Config, "Build auth")
```

### 15.3 CLI Test Environment

```go
setupTestEnv(t)          → temp dir + nxd.yaml + event store + SQLite
seedTestReq(t, env, ...) → projects REQ_SUBMITTED event
seedTestStory(t, env, .) → projects STORY_CREATED event
seedTestAgent(t, env, .) → inserts agent directly into SQLite
seedTestEscalation(t,..) → projects STORY_ESCALATED event
execCmd(t, cmd, cfg, ..) → Cobra test runner with output capture
initTestRepo(t, dir)     → minimal git repo for worktree tests
```

### 15.4 Coverage by Package

| Package | Coverage | Test Strategy |
|---------|:---:|--------------|
| graph | 96% | Pure functions, table-driven tests |
| plugin | 93% | File-based fixtures, playbook injection |
| llm | 92% | All client variants, error classification |
| criteria | 88% | Real Go projects for test_passes/coverage |
| agent | 86% | Prompt template verification, scoring |
| scratchboard | 85% | JSONL read/write, category filtering |
| artifact | 82% | Write/Read/Append/List cycle tests |
| state | 79% | Event projection, filter, migration |
| config | 77% | Validation, defaults, edge cases |
| runtime | 69% | Tool execution, safePath, allowlist |
| engine | 65% | Planner, dispatcher, monitor (mocked LLM) |
| tmux | 65% | Session management (requires tmux) |
| web | 61% | HandleCommand, snapshot, EventBus |
| cli | 57% | 40+ command tests, orchestration |
| git | 44% | Worktree + conflict (requires git) |

### 15.5 Test Gaps

| Gap | Reason | Impact |
|-----|--------|--------|
| git package (44%) | Requires real git repos, rebase operations | Merge failures in production may not be caught |
| CLI orchestration | Full pipeline requires all stores + runtimes | Integration test complexity |
| Native runtime E2E | Requires Ollama running | Can't test in CI without GPU |
| WebSocket hub | Requires concurrent goroutines + timing | Flaky potential in race detector |

---

## 16. Failure Recovery Playbook

> **Status: PROPOSED** — Operational runbook for common failure scenarios

### 16.1 Corrupted State Recovery

**Symptom:** `nxd status` returns garbled data or panics on store queries.

```bash
# Step 1: Check SQLite integrity
sqlite3 ~/.nxd/nxd.db "PRAGMA integrity_check;"

# Step 2: If corrupted, rebuild projections from events
mv ~/.nxd/nxd.db ~/.nxd/nxd.db.bak
nxd status  # triggers fresh projection from events.jsonl

# Step 3: If events.jsonl is corrupted (truncated JSON line)
# Find the last valid line:
python3 -c "
import json, sys
with open('$HOME/.nxd/events.jsonl') as f:
    for i, line in enumerate(f, 1):
        try: json.loads(line)
        except: print(f'Corrupt at line {i}: {line[:80]}'); break
"
# Truncate file to last valid line
head -n <last_valid_line> ~/.nxd/events.jsonl > /tmp/events_clean.jsonl
mv /tmp/events_clean.jsonl ~/.nxd/events.jsonl
```

### 16.2 Stuck Controller Loop

**Symptom:** Controller repeatedly cancels/restarts the same story. Dashboard shows rapid `CONTROLLER_ACTION` events.

```bash
# Step 1: Check controller cooldown config
nxd config | grep -A5 controller

# Step 2: If cooldown_s is too low (< 60), increase it
# Edit nxd.yaml: controller.cooldown_s: 120

# Step 3: Manually pause the stuck requirement
nxd pause

# Step 4: Inspect the stuck story
nxd logs <story-id> --lines 50
nxd diff <story-id> --stat

# Step 5: Either approve manually or reject + restart
nxd approve <story-id>   # skip review gate
# OR
nxd reject <story-id>    # trigger re-plan
```

### 16.3 Orphaned Worktrees

**Symptom:** Disk space grows; `~/.nxd/worktrees/` has stale directories.

```bash
# Step 1: List all worktrees
git -C <repo-path> worktree list

# Step 2: Prune invalid worktrees
git -C <repo-path> worktree prune

# Step 3: Use NXD garbage collector
nxd gc

# Step 4: Nuclear option (remove all NXD worktrees)
rm -rf ~/.nxd/worktrees/*
git -C <repo-path> worktree prune
```

### 16.4 Merge Conflict Cascade

**Symptom:** Wave N+1 stories fail to merge because Wave N left unresolved conflicts.

```bash
# Step 1: Check which stories are stuck
nxd status | grep -E "merge_ready|pr_submitted"

# Step 2: Manually resolve in the worktree
cd ~/.nxd/worktrees/<story-id>
git status  # see conflicting files
# ... resolve conflicts ...
git add .
git rebase --continue

# Step 3: Re-trigger merge
nxd approve <story-id>

# Step 4: If cascade is deep, reset and re-run
nxd pause
# Clear all worktrees and re-dispatch
nxd gc
nxd resume
```

### 16.5 Lock File Stale

**Symptom:** `nxd req` fails with "another instance is running" but no process exists.

```bash
# Step 1: Verify no NXD process
ps aux | grep nxd

# Step 2: Remove stale lock
rm -f ~/.nxd/nxd.lock

# Step 3: Also clear stale event store connections
rm -f ~/.nxd/nxd.db-wal ~/.nxd/nxd.db-shm  # SQLite WAL files
```

### 16.6 LLM Provider Failure

**Symptom:** All stories fail with connection errors to LLM API.

```bash
# Step 1: Check provider connectivity
curl -s http://localhost:11434/api/tags  # Ollama
# OR
curl -s https://api.anthropic.com/v1/messages -H "x-api-key: $ANTHROPIC_API_KEY"

# Step 2: If Ollama is down, restart it
ollama serve &

# Step 3: If cloud provider is down, switch to fallback
# Edit nxd.yaml: models.tech_lead.provider: "google+ollama"

# Step 4: Resume with --dry-run to test pipeline without API calls
nxd resume --dry-run
```

---

## 17. Competitive Positioning

> **Status: PROPOSED** — Market analysis for positioning decisions

### 17.1 Feature Comparison Matrix

| Capability | NXD | Devin (Cognition) | Cursor | SWE-agent | OpenAI Codex | GitHub Copilot Workspace |
|-----------|:---:|:---:|:---:|:---:|:---:|:---:|
| **Multi-agent orchestration** | DAG-based waves | Single agent | Single agent | Single agent | Single agent | Multi-file |
| **Self-hosted** | Full | No (SaaS only) | Partial (editor) | Yes | No (SaaS) | No (SaaS) |
| **Model agnostic** | Any (Ollama, Anthropic, OpenAI, Google) | Proprietary | Multiple | OpenAI | OpenAI | GitHub/OpenAI |
| **Cost transparency** | Full (per-token, margin tracking) | Opaque | Subscription | API costs | Per-task | Subscription |
| **Dependency-aware parallelism** | DAG + wave dispatch | No | No | No | No | Limited |
| **Escalation chain** | 5-tier (Junior → Pause) | Retry | N/A | Retry | N/A | N/A |
| **Code review pipeline** | LLM review + QA + criteria | Self-review | Manual | Manual | Manual | Manual |
| **Auto-merge** | Local + GitHub PR modes | Yes | No | No | No | PR creation |
| **Plugin system** | Playbooks, QA checks, prompts | No | Extensions | No | No | No |
| **Real-time dashboard** | WebSocket + DAG visualization | Web UI | Editor UI | Terminal | Web UI | Web UI |
| **Offline operation** | Full (Ollama) | No | No | No | No | No |
| **Semantic memory** | MemPalace + Scratchboard | Limited | Editor context | No | No | No |

### 17.2 NXD Differentiators

1. **True multi-agent parallelism** — Not "one agent doing multiple things" but multiple independent agents working on independent stories simultaneously, coordinated by a DAG.

2. **Complete self-hosting** — Zero cloud dependency. Run entirely on local hardware with Ollama. No data leaves the machine.

3. **Cost transparency** — Every token tracked, every hour estimated, margin calculated. Clients see exact ROI.

4. **Pluggable architecture** — Swap runtimes (aider ↔ claude ↔ codex), swap models, add custom QA checks, inject domain-specific playbooks. No vendor lock-in at any layer.

5. **Event-sourced audit trail** — Every decision, every escalation, every retry is an immutable event. Enterprise-grade auditability built into the core architecture.

### 17.3 Competitive Weaknesses

| Weakness | Impact | Mitigation Path |
|----------|--------|----------------|
| No IDE integration | Developers must use CLI | Phase 2: VS Code extension |
| Setup complexity vs Cursor | Higher barrier to entry | Phase 1: better defaults, one-command install |
| No proprietary model advantage | Quality limited by available models | FallbackClient + model marketplace (Phase 3) |
| Single-machine limitation | Can't scale horizontally yet | Phase 2: team server with job queue |

---

## 18. Compliance & IP Considerations

> **Status: PROPOSED** — Framework for enterprise readiness

### 18.1 Code Ownership

| Scenario | IP Owner | Notes |
|----------|----------|-------|
| Local Ollama (open-weight models) | User | No third-party claim; model weights are licensed for use |
| Cloud API (Anthropic, OpenAI, Google) | User | Provider ToS: user retains IP on outputs |
| Plugin-generated code | User | Plugins are user-configured scripts |
| NXD-generated prompts/templates | NXD project | MIT-licensed, freely usable |

**Key consideration:** When using cloud LLMs, source code is transmitted to the provider. This may violate corporate policies for proprietary codebases. **Self-hosted Ollama is the compliance-safe default.**

### 18.2 GDPR Implications

| Data Type | Stored Where | Retention | GDPR Impact |
|-----------|-------------|-----------|:---:|
| Source code (in prompts) | LLM provider (cloud) | Per provider policy | **High** |
| Source code (Ollama) | Local only | User-controlled | None |
| Event payloads | `~/.nxd/events.jsonl` | `log_retention_days` config | Low |
| Metrics (token usage) | `~/.nxd/metrics.jsonl` | `log_retention_days` config | Low |
| Agent logs | `~/.nxd/logs/` | Configurable | Low |

**Recommendation:** For GDPR-regulated environments, use `mode: subscription` (Ollama) to ensure no code leaves the data boundary.

### 18.3 SOC2 Readiness

| SOC2 Control | NXD Current State | Gap |
|-------------|------------------|-----|
| **Audit logging** | Full event store (append-only, timestamped) | None — inherent to architecture |
| **Access control** | None (single-user CLI) | Phase 2: RBAC via GitHub OAuth |
| **Encryption at rest** | None (plaintext SQLite + JSONL) | Phase 2: encrypted state directory |
| **Encryption in transit** | HTTPS to cloud LLMs; none for local dashboard | Phase 2: TLS on dashboard |
| **Change management** | Git-based (all changes in worktrees + branches) | None — inherent to workflow |
| **Incident response** | Controller detects + acts on stuck agents | Document formal IR procedures |
| **Data retention** | Configurable `log_retention_days` | Add formal retention policy |

### 18.4 License Compliance

NXD depends on open-source libraries. Key license obligations:

| Dependency | License | Obligation |
|-----------|---------|-----------|
| mattn/go-sqlite3 | MIT | Attribution in binary |
| spf13/cobra | Apache 2.0 | Attribution, include license |
| Ollama models | Varies (Gemma: Apache 2.0, LLaMA: Meta license) | Check per-model license |

---

## 19. State Schema Evolution

> **Status: PARTIALLY IMPLEMENTED** — Migration via ALTER TABLE; no formal versioning

### 19.1 Current Migration Approach

```go
// In sqlite.go initSQL:
ALTER TABLE requirements ADD COLUMN IF NOT EXISTS repo_path TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN IF NOT EXISTS pr_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stories ADD COLUMN IF NOT EXISTS owned_files TEXT NOT NULL DEFAULT '[]';
// ... etc
```

**Strengths:** Idempotent (safe to re-run). Additive only (never drops columns).

**Weaknesses:** No version tracking. No way to know which migrations have run. No down-migration support.

### 19.2 Proposed Version-Tracked Migrations

```
~/.nxd/
├── nxd.db              (SQLite database)
├── events.jsonl        (event store)
└── schema_version      (single integer, e.g., "5")
```

Migration flow:
```
On startup:
  1. Read schema_version file (default: 0 if missing)
  2. Apply all migrations where version > current:
     v1: base tables (requirements, stories, agents)
     v2: escalations table + story_deps
     v3: agent_scores table
     v4: owned_files, wave_hint, wave columns
     v5: merged_at, split_depth, escalation_tier
  3. Write new schema_version
  4. If version > latest known: error("NXD version too old for this state dir")
```

### 19.3 Event Schema Versioning

Events use flexible JSON payloads, so they're naturally forward-compatible. Strategy:

- **New fields:** Add to payload; old consumers ignore unknown keys
- **Removed fields:** Stop emitting; old events retain their data
- **Changed types:** Never change types in existing fields; add new field instead
- **Projection changes:** Re-project from events with updated `Project()` logic

### 19.4 SQLite → PostgreSQL Migration Path (Phase 2)

```
Step 1: Implement state.PostgresStore satisfying EventStore + ProjectionStore
Step 2: Migration tool reads events.jsonl + nxd.db, writes to PostgreSQL
Step 3: Config switch: workspace.backend: "postgres"
Step 4: PostgreSQL connection string in config or env var

Schema differences:
  - TEXT → VARCHAR with length constraints
  - TIMESTAMP DEFAULT CURRENT_TIMESTAMP → TIMESTAMPTZ DEFAULT NOW()
  - JSON array columns → JSONB native type
  - story_deps → proper foreign keys with ON DELETE CASCADE
  - Add: connection pooling (pgxpool)
  - Add: row-level locking replaces Go mutex
```

---

## 20. Plugin Architecture

> **Status: IMPLEMENTED** — Four extension types: playbooks, prompts, QA checks, providers

### 20.1 Plugin System Overview

```
plugins/
├── playbooks/          ← Markdown injected into agent prompts
│   ├── security.md
│   └── testing.md
├── prompts/            ← Override system/goal prompt templates
│   └── tech_lead.txt
├── qa/                 ← Shell scripts run after story completion
│   ├── lint_check.sh
│   └── coverage.sh
└── providers/          ← External LLM subprocess wrappers
    └── (configured in nxd.yaml)
```

### 20.2 Playbook Injection Flow

```
Agent prompt construction:
  1. Load base template for role (promptTemplates[role])
  2. Substitute placeholders ({team_name}, {repo_path}, etc.)
  3. Check context flags (IsExistingCodebase, IsBugFix, IsInfra)
  4. Append built-in extras (CodebaseArchaeology, BugHuntingMethodology, etc.)
  5. For each plugin playbook:
     ├── ShouldInject(role, isExisting, isBugFix, isInfra)?
     │   ├── InjectWhen matches context?
     │   └── Role in Roles list (or Roles empty)?
     └── If yes: append playbook Content to prompt
  6. Return final concatenated prompt
```

**Injection conditions:**

| InjectWhen | Triggers When |
|-----------|--------------|
| `"always"` | Every story, every role |
| `"existing"` | `IsExistingCodebase == true` |
| `"bugfix"` | `IsBugFix == true` |
| `"infra"` | `IsInfrastructure == true` |

### 20.3 QA Check Lifecycle

```
Story completes implementation
    │
    ▼
Monitor triggers post-execution pipeline
    │
    ├─► Built-in Review (LLM)
    ├─► Built-in QA (lint, build, test, criteria)
    │
    └─► Plugin QA Checks (for each check where After == current stage):
        │
        ├─► exec.CommandContext(ctx, check.ScriptPath)
        │   cmd.Dir = worktreePath
        │
        ├─► Exit code 0 → QACheckResult{Passed: true}
        │   Exit code != 0 → QACheckResult{Passed: false}
        │
        └─► Results included in QA pass/fail decision
```

### 20.4 Prompt Override Precedence

```
Priority (highest first):
  1. Plugin prompts (SetPluginState overrides)
  2. Built-in role templates (promptTemplates map)
  3. Default fallback ("You are an AI coding assistant.")

Check: if mgr.Prompts[roleKey] exists → use it
Else: use promptTemplates[role]
```

### 20.5 External Provider Registration

```yaml
# nxd.yaml
plugins:
  providers:
    - name: local-llama
      command: /usr/local/bin/llama-server
      args: ["--model", "codellama-7b"]
      models: ["codellama-7b", "codellama-13b"]
```

Registered as `SubprocessInfo` in PluginManager. When `buildLLMClient()` encounters an unknown provider, it checks `activePluginProviders` map for a subprocess match.

---

## 21. User Journey Maps

> **Status: PROPOSED** — Persona-based workflows across deployment phases

### 21.1 Solo Developer (Phase 1)

```
                    Solo Dev — Local Setup
                    ═══════════════════════

Day 1: Setup
  $ go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
  $ ollama pull gemma4
  $ cp nxd.config.example.yaml nxd.yaml  (edit models section)

Day 1: First Requirement
  $ nxd req "Add REST API with user CRUD endpoints"
  │
  ├─► Views plan: "4 stories, estimated 8 hours, $0 LLM cost"
  ├─► $ nxd resume
  ├─► $ nxd dashboard  (opens browser, watches agents work)
  ├─► Sees DAG: story-1 → story-2 → story-3, story-4 parallel
  ├─► Agent gets stuck on story-3 → auto-escalated to Senior
  ├─► All stories merge → $ nxd report req-001
  └─► Total: 23 minutes, 4 stories, 1 escalation, $0 cost

Weekly Workflow
  $ nxd req "..."        (2-3 requirements per week)
  $ nxd metrics          (check token usage trends)
  $ nxd gc               (monthly cleanup)
```

### 21.2 Team Lead (Phase 2)

```
                    Team Lead — Team Server
                    ════════════════════════

Setup: Deploy NXD server (Docker or bare metal)
  - Configure PostgreSQL, Redis, GitHub OAuth
  - Set team API keys for Anthropic/OpenAI
  - Define team plugins (coding standards playbook)

Daily Workflow
  POST /api/v1/requirements { title: "...", repo: "..." }
  │
  ├─► Dashboard shows team-wide view: 3 active requirements
  ├─► Priority queue: urgent bug fix jumps ahead
  ├─► Review gates: approve/reject stories from dashboard
  ├─► Cost tracking: team used 450 story points this month
  │
  └─► Weekly: $ nxd report --team  (aggregate delivery report)

Governance
  - Plugin playbook: "All stories must include unit tests"
  - QA check plugin: custom linting script for team standards
  - Routing override: all 8+ complexity stories → Senior only
```

### 21.3 Enterprise Admin (Phase 3)

```
                    Enterprise Admin — SaaS Platform
                    ════════════════════════════════

Setup: Organization onboarding
  - SSO/SAML configuration
  - Per-team billing quotas
  - Custom model allowlist (approved models only)
  - VPC peering for self-hosted LLM endpoints
  - SOC2 audit log export to SIEM

Operations
  - SLA dashboard: 99.9% API availability, p95 latency < 10s
  - Cost center tracking: billing per team, per project
  - Model marketplace: approve community plugins
  - Capacity management: dedicated worker pools for priority teams
  - Compliance reports: auto-generated for quarterly audits
```

---

## 22. Current Limitations

> **Status: IMPLEMENTED** — Honest assessment of what doesn't work yet

### 22.1 Functional Limitations

| Limitation | Impact | Planned Resolution |
|-----------|--------|-------------------|
| **No adaptive routing** | Static complexity thresholds; scoring infrastructure unused | Bayesian model (Section 5) |
| **Single-machine only** | Can't distribute agents across multiple machines | Phase 2 team server |
| **No IDE integration** | CLI-only workflow; no VS Code/JetBrains extension | Phase 2 extension |
| **No incremental re-planning** | If a story fails repeatedly, entire requirement must be re-planned manually | Auto re-plan on Tier 3 escalation (partial) |
| **No partial execution resume** | If NXD crashes mid-wave, must re-run from last completed wave | Checkpoint events exist but resume logic incomplete |
| **Coverage at 65.3%** | 15% below 80% target; git package at 44% | Active coverage push |

### 22.2 Security Limitations

| Limitation | Severity | Status |
|-----------|:---:|-----------|
| Command allowlist prefix matching bypassable | **Critical** | ✅ Resolved — shell metacharacters rejected + boundary match |
| Plugin absolute paths accepted | **Critical** | ✅ Resolved — `filepath.IsAbs()` rejected + confinement check |
| Story IDs not validated as branch names | **High** | ✅ Resolved — regex-validated before use |
| LLM tool call arguments untyped | **High** | ✅ Resolved — JSON Schema type validation |
| No symlink resolution in safePath | **Medium** | ✅ Resolved — `EvalSymlinks()` + re-check confinement |
| No LLM response length limit | **Medium** | ✅ Resolved — 200K char truncation |
| Config regex patterns unvalidated | **Medium** | ✅ Resolved — compiled at load time |
| **API keys in plaintext memory** | **Medium** | 🔄 Deferred to Phase 2 (secrets manager) |

### 22.3 Scalability Limitations

| Limitation | Threshold | Resolution |
|-----------|-----------|-----------|
| SQLite single-writer lock | ~100 writes/sec | PostgreSQL (Phase 2) |
| JSONL file scan for metrics | O(n) on file size | Indexed storage or time-partitioned files |
| SemaphoreClient is per-wave, not global | Multiple waves could exceed GPU capacity | Global concurrency manager |
| No job queue for requirements | Can't queue multiple requirements | Redis queue (Phase 2) |

---

## Appendix A: Event Type Reference

| Event | Source | Payload Fields |
|-------|--------|---------------|
| `REQ_SUBMITTED` | CLI | id, title, description, repo_path |
| `REQ_CLASSIFIED` | Classifier | category, tech_stack |
| `REQ_PLANNED` | Planner | story_count, total_points |
| `REQ_COMPLETED` | Monitor | stories_merged |
| `REQ_PAUSED` | Controller/CLI | reason |
| `STORY_CREATED` | Planner | id, title, complexity, depends_on |
| `STORY_ASSIGNED` | Dispatcher | agent_id, wave |
| `STORY_STARTED` | Executor | worktree_path, runtime, branch, tier, role |
| `STORY_PROGRESS` | Runtime | iteration, phase, tool, file, command |
| `STORY_COMPLETED` | Monitor/Runtime | iterations, summary, criteria_passed |
| `STORY_REVIEW_REQUESTED` | Monitor | diff_length |
| `STORY_REVIEW_PASSED` | Reviewer | comments |
| `STORY_REVIEW_FAILED` | Reviewer | feedback, comments |
| `STORY_QA_STARTED` | QA | checks |
| `STORY_QA_PASSED` | QA | checks_passed |
| `STORY_QA_FAILED` | QA | failures |
| `STORY_PR_CREATED` | Merger | pr_url, pr_number |
| `STORY_MERGED` | Merger | branch, commit |
| `STORY_ESCALATED` | Monitor | from_tier, to_tier, reason |
| `STORY_REWRITTEN` | Manager | new_title, new_description |
| `STORY_SPLIT` | Manager | children |
| `AGENT_SPAWNED` | Dispatcher | role, session_name |
| `AGENT_STUCK` | Controller | stuck_duration_s |
| `AGENT_TERMINATED` | Controller | reason |
| `CONTROLLER_ANALYSIS` | Controller | stories_checked, actions_taken |
| `CONTROLLER_ACTION` | Controller | kind, story_id, reason |
| `CONTROLLER_STUCK_DETECTED` | Controller | stuck_duration_s, escalation_tier |

## Appendix B: CLI Command Reference

| Command | Description |
|---------|-------------|
| `nxd req "<requirement>"` | Submit new requirement for planning + execution |
| `nxd resume` | Resume/continue execution of planned stories |
| `nxd estimate "<requirement>"` | Cost estimate without execution |
| `nxd status` | Show requirement and story status |
| `nxd agents` | List active agent sessions |
| `nxd events` | Show recent events |
| `nxd logs <story-id>` | View agent trace logs (--follow, --lines, --raw) |
| `nxd diff <story-id>` | Show worktree diff (--stat, --cached) |
| `nxd report <req-id>` | Generate delivery report |
| `nxd approve <story-id>` | Manually approve a story past review gate |
| `nxd reject <story-id>` | Reject a story (triggers escalation) |
| `nxd pause` | Pause active requirement |
| `nxd metrics` | Show token usage and cost metrics |
| `nxd gc` | Garbage collect old worktrees, branches, events |
| `nxd dashboard` | Launch web dashboard |
| `nxd learn <repo-path>` | Scan repository to build RepoProfile |
| `nxd config` | Show resolved configuration |
| `nxd watch` | Watch requirement progress in real-time |
| `nxd archive <req-id>` | Archive completed requirement |

---

*Generated 2026-04-15. Based on codebase at commit `3e9d25a`.*
*Updated 2026-04-15: Added sections 11-22 (security, data model, observability, capacity, testing, recovery, competitive, compliance, schema evolution, plugins, user journeys, limitations). Applied status markers (IMPLEMENTED/PROPOSED) to all sections.*
*Updated 2026-04-16: Security fixes SG-1 through SG-6 and SG-8 resolved in code. Added Section 11.7 (Phase 2 secrets manager recommendations — Infisical as primary, Vault for Phase 3 enterprise).*
