# Feature Specification: AgentFlow-Inspired Runtime Improvements

**Feature Branch**: `001-agentflow-improvements`
**Created**: 2026-04-12
**Status**: Implemented (retroactive spec)
**Input**: Seven improvements to the native Gemma runtime inspired by patterns from the AgentFlow orchestration framework, addressing concurrency, observability, cross-agent collaboration, quality verification, visualization, and autonomous recovery.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Reliable Native Agent Execution (Priority: P1)

As a developer running multiple native Gemma agents in parallel on a single-GPU machine, I need the system to serialize inference calls so that agents don't timeout waiting for the GPU, and I can see real-time progress of each agent's work.

**Why this priority**: Without concurrency control, parallel native agents all timeout on Ollama (5-minute HTTP timeout, single-GPU serialization). This is a blocking bug that prevents any native agent work from completing.

**Independent Test**: Can be tested by launching 3+ native agents simultaneously against a single Ollama instance and verifying all complete without timeout errors.

**Acceptance Scenarios**:

1. **Given** 6 native Gemma agents are dispatched simultaneously, **When** the system begins execution, **Then** at most 1 agent calls the LLM at a time (configurable) and all agents eventually complete without HTTP timeout errors.
2. **Given** a native agent is waiting for an LLM slot, **When** the context is cancelled (e.g., Ctrl+C), **Then** the agent stops waiting immediately and returns a context cancellation error.
3. **Given** a native agent is executing, **When** it calls a tool (read_file, write_file, etc.), **Then** a progress event is emitted within 100ms showing the iteration number, tool name, and target file.

---

### User Story 2 - Post-Mortem Debugging with Artifacts (Priority: P1)

As a developer investigating why an agent's work was rejected or failed QA, I need structured per-story artifacts (launch config, trace events, diffs, review/QA results) so I can reproduce and diagnose issues without re-running the agent.

**Why this priority**: Without artifact storage, agent work is ephemeral -- logs are sparse, worktrees are pruned, and there's no way to reconstruct what happened during execution.

**Independent Test**: Can be tested by running a single story through the full pipeline and verifying the artifact directory contains all expected files.

**Acceptance Scenarios**:

1. **Given** a native agent is spawned for a story, **When** it starts execution, **Then** a launch config artifact is written containing the story ID, runtime name, model, prompt text, and wave brief.
2. **Given** a native agent is executing, **When** it emits progress events, **Then** each event is appended to a per-story trace JSONL file in the artifact directory.
3. **Given** a story completes the post-execution pipeline, **When** review and QA run, **Then** the git diff, review result, and QA result are each written as separate artifacts.

---

### User Story 3 - Cross-Agent Knowledge Sharing (Priority: P2)

As a developer running parallel agents on related stories, I need agents to share discoveries (API patterns, configuration requirements, schema details) so that later agents benefit from earlier agents' findings rather than rediscovering the same information.

**Why this priority**: Parallel agents working on related stories frequently discover shared context (e.g., "go.mod requires go 1.22") that other agents need but have no way to access.

**Independent Test**: Can be tested by running 2 native agents in parallel where Agent A writes a discovery and Agent B reads it before completing its task.

**Acceptance Scenarios**:

1. **Given** two native agents are running in parallel, **When** Agent A writes a discovery to the scratchboard, **Then** Agent B can read it via the read_scratchboard tool and sees Agent A's entry.
2. **Given** a scratchboard has entries from multiple agents, **When** an agent reads with a category filter, **Then** only entries matching that category are returned.
3. **Given** a scratchboard accumulates many entries, **When** an agent reads it, **Then** at most 20 entries are returned (newest first) to avoid context overflow.

---

### User Story 4 - Real-Time Dashboard Updates (Priority: P2)

As a developer monitoring agents via the web dashboard, I need events to appear instantly (not on a 2-second polling cycle) so I can see what each agent is doing in real time.

**Why this priority**: The 2-second polling delay makes the dashboard feel sluggish during native runtime execution where progress events fire every few seconds.

**Independent Test**: Can be tested by opening the web dashboard, running a native agent, and verifying progress events appear within 200ms of emission.

**Acceptance Scenarios**:

1. **Given** a native agent emits a STORY_PROGRESS event, **When** the event is appended to the event store, **Then** all connected WebSocket clients receive it within 200ms.
2. **Given** a WebSocket client disconnects and reconnects, **When** it receives the initial state snapshot, **Then** it has a complete view including any events that occurred during disconnection.
3. **Given** a WebSocket client is slow to consume events, **When** more events arrive, **Then** old events are dropped for that client rather than blocking the event producer.

---

### User Story 5 - Automated Quality Verification (Priority: P2)

As a developer configuring QA checks, I need to define declarative success criteria (file must exist, tests must pass, coverage must exceed threshold) that are automatically evaluated after each story completes.

**Why this priority**: Procedural QA catches syntax and build errors but cannot verify that the agent actually created expected files, met coverage targets, or produced output matching business requirements.

**Independent Test**: Can be tested by configuring criteria in nxd.yaml and running a story, then verifying criteria are evaluated and failures trigger a retry with specific feedback.

**Acceptance Scenarios**:

1. **Given** success criteria are configured (file_exists, test_passes), **When** a native agent calls task_complete, **Then** the criteria are evaluated against the worktree and results are included in the completion payload.
2. **Given** a criterion fails (e.g., expected file does not exist), **When** the result is returned, **Then** the story is marked as failed with a message identifying exactly which criterion failed and what was expected vs actual.
3. **Given** no criteria are configured, **When** a story completes, **Then** the existing procedural QA pipeline runs unchanged.

---

### User Story 6 - Visual Pipeline Overview (Priority: P3)

As a developer or team lead reviewing pipeline progress, I need to see the story dependency graph visually with color-coded status per node, so I can understand wave parallelism and identify bottlenecks.

**Why this priority**: The current tabular dashboard shows stories in a flat list. The DAG structure is invisible.

**Independent Test**: Can be tested by running a requirement with dependencies, then checking that the web dashboard state snapshot includes a DAG export with correct wave assignments.

**Acceptance Scenarios**:

1. **Given** a requirement has been planned with story dependencies, **When** the web dashboard loads, **Then** the state snapshot includes a DAG export with nodes (each having a wave assignment) and edges.
2. **Given** stories in the DAG have different statuses, **When** the dashboard renders, **Then** each node can be colored according to its current status.
3. **Given** a DAG has multiple waves, **When** exported, **Then** wave assignments correctly reflect the topological ordering.

---

### User Story 7 - Autonomous Stuck Agent Recovery (Priority: P3)

As a developer who may step away during long pipeline runs, I need the system to automatically detect stuck agents and take corrective action so that the pipeline doesn't stall indefinitely.

**Why this priority**: Without a controller, stuck agents remain in_progress forever. An active controller enables truly autonomous pipeline operation.

**Independent Test**: Can be tested by running a native agent that deliberately stalls, enabling the controller, and verifying it cancels/restarts the stuck story after the configured threshold.

**Acceptance Scenarios**:

1. **Given** a controller is enabled with a 300-second stuck threshold, **When** a story has no progress events for 300+ seconds, **Then** the controller detects it as stuck and emits a CONTROLLER_STUCK_DETECTED event.
2. **Given** a stuck story is detected and auto_restart is enabled, **When** the controller takes action, **Then** the agent's context is cancelled, the story is reset to draft status, and a CONTROLLER_ACTION event is emitted.
3. **Given** a controller has taken an action this tick, **When** another stuck story is detected in the same tick, **Then** it is deferred to the next tick (max 1 action per tick by default).
4. **Given** a controller took an action recently, **When** the cooldown period (default 120s) has not elapsed, **Then** no further actions are taken.

---

### Edge Cases

- What happens when the Ollama server goes down mid-execution? The semaphore releases the slot on error, and the progress callback emits an error-phase event.
- What happens when multiple agents write to the scratchboard simultaneously? Thread-safe via sync.RWMutex; JSONL append is atomic per entry.
- What happens when a WebSocket client reconnects after missing events? Full state snapshot is sent on connect; missed individual events are not replayed.
- What happens when the artifact directory's disk is full? Write errors are logged but don't crash the pipeline.
- What happens when a story has no progress events and was never started? lastProgressTime returns zero time, which exceeds any threshold -- the controller correctly identifies it as stuck.
- What happens when the controller cancels a native agent mid-tool-call? Context cancellation propagates to the LLM client's HTTP request; the goroutine exits and emits STORY_COMPLETED with error.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST limit concurrent LLM calls for native runtimes to a configurable maximum (default 1) using a shared semaphore across all agents in a wave.
- **FR-002**: System MUST persist per-story artifacts (launch config, trace events, git diff, review result, QA result) to a structured directory.
- **FR-003**: System MUST provide a thread-safe scratchboard scoped per requirement run, allowing agents to write categorized discoveries and read recent entries (max 20, newest first).
- **FR-004**: Native Gemma agents MUST have write_scratchboard and read_scratchboard tools available during execution.
- **FR-005**: System MUST publish events instantly to WebSocket clients via an in-process event bus, without waiting for the polling interval.
- **FR-006**: The event bus MUST drop events for slow consumers rather than blocking the producer.
- **FR-007**: System MUST support 5 declarative criterion types: file_exists, file_contains (with regex support), test_passes, coverage_above, and command_succeeds.
- **FR-008**: Criteria evaluation MUST run after a native agent calls task_complete, and results MUST be included in the execution result.
- **FR-009**: System MUST export the story dependency DAG as a JSON structure with nodes (including wave assignments) and edges.
- **FR-010**: The periodic controller MUST detect stuck stories by comparing the last progress event timestamp against a configurable threshold.
- **FR-011**: The controller MUST support 3 action types: cancel, restart, and reprioritize.
- **FR-012**: The controller MUST enforce safety guards: maximum actions per tick, cooldown period between actions, and opt-in activation via config.
- **FR-013**: Native runtime goroutines MUST use cancellable contexts so the controller can stop them.

### Key Entities

- **SemaphoreClient**: Wraps an LLM client with a buffered channel limiting concurrent calls. One instance shared per wave.
- **ArtifactStore**: Filesystem-backed store managing per-story artifact directories. Supports JSON, raw text, and JSONL append.
- **Scratchboard**: JSONL-backed knowledge store scoped to a requirement run. Entries have agent ID, story ID, category, content, and timestamp.
- **EventBus**: In-process pub/sub with subscriber channels. Drops events for slow consumers.
- **Criterion/Result**: Declarative check definition (type, target, expected) and evaluation outcome (passed, actual, message).
- **DAGExport**: JSON-serializable representation of the dependency graph with nodes and edges.
- **Controller**: Periodic loop that detects stuck stories, decides and executes corrective actions with safety guards.
- **ProgressEvent**: Describes a single progress update with iteration, phase, tool, file, and detail.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All native agents in a 6-agent parallel wave complete without timeout errors on a single-GPU machine.
- **SC-002**: Every completed story has an artifact directory containing at least a launch config and trace events file.
- **SC-003**: Scratchboard entries written by one agent are readable by another agent within the same requirement run within 1 second.
- **SC-004**: Progress events appear in connected WebSocket clients within 200ms of being appended to the event store.
- **SC-005**: Declarative criteria with known outcomes produce correct pass/fail results 100% of the time.
- **SC-006**: The DAG export correctly assigns wave numbers matching the topological ordering for all tested graph shapes.
- **SC-007**: A story with no progress for longer than the configured threshold is detected and acted upon within 2 controller ticks.
- **SC-008**: The controller never takes more than the configured max actions per tick and respects cooldown.
- **SC-009**: All 7 features have unit tests and the full test suite passes with no regressions.

## Assumptions

- The system runs on a machine with a single GPU for Ollama inference. Multi-GPU setups can increase the concurrency config.
- Native Gemma runtime is the primary consumer of the semaphore, artifact store, scratchboard tools, and criteria evaluation. CLI-based runtimes benefit from artifacts and event bus but don't directly use the semaphore or scratchboard tools.
- The controller is disabled by default and must be explicitly enabled in configuration.
- WebSocket clients are the web dashboard; the TUI dashboard uses its own polling mechanism.
- Artifact storage uses the local filesystem. No remote/cloud storage is assumed.
- The scratchboard is scoped per requirement run to prevent knowledge leaking across unrelated work.
