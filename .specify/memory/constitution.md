<!--
Sync Impact Report
==================
Version change: (none) → 1.0.0
Modified principles: N/A (initial adoption)
Added sections:
  - Core Principles (6 principles)
  - Technical Constraints
  - Development Workflow
  - Governance
Removed sections: N/A
Templates requiring updates:
  - .specify/templates/plan-template.md — no update needed
    (Constitution Check section is generic: "[Gates determined based on
    constitution file]" — plans will derive gates from these principles)
  - .specify/templates/spec-template.md — no update needed
    (spec template is technology-agnostic; principles do not add
    mandatory spec sections beyond what the template already covers)
  - .specify/templates/tasks-template.md — no update needed
    (task phases and parallel markers are compatible with the
    principles; no new principle-driven task types required)
  - .specify/templates/commands/*.md — no command files exist
Follow-up TODOs: none
-->

# NXD (Nexus Dispatch) Constitution

## Core Principles

### I. Offline-First

All features MUST function without network access. This is the
defining constraint of NXD.

- Every new capability MUST work with local LLMs via Ollama and
  local git operations. Cloud providers (Anthropic, OpenAI, GitHub)
  are optional overlays, never requirements.
- No feature may introduce a hard dependency on an external API,
  cloud service, or network endpoint.
- Tests MUST pass in a fully offline environment (no network stubs
  that mask online dependencies).

**Rationale**: NXD exists so developers can orchestrate agents on
planes, off-grid, and without API costs. Any online dependency
violates the product's core value proposition.

### II. Event-Sourced State

All state mutations MUST flow through the append-only event log.
SQLite projections are derived read models, never the source of truth.

- Events are immutable once written — no updates, no deletes.
- Projections MUST be rebuildable from the event log at any time.
- New state queries SHOULD be served by projections, not by scanning
  the raw event log at runtime.
- Every new domain action MUST emit a typed event (see
  `docs/reference/event-reference.md` for the canonical list).

**Rationale**: Event sourcing provides full audit trails, enables
replay-based debugging, and makes the system deterministic.

### III. Test-First (NON-NEGOTIABLE)

TDD is mandatory. Tests MUST be written before implementation code.

- Red-Green-Refactor cycle strictly enforced: write a failing test,
  write minimal code to pass, then refactor.
- Minimum 80% test coverage across the codebase.
- Three test tiers are required:
  1. **Unit** — individual functions and utilities (`go test ./...`)
  2. **Integration** — cross-package interactions, SQLite operations
  3. **E2E** — full pipeline flows (`go test -tags e2e ./test/`)
- Race detection MUST be enabled in CI (`-race` flag).

**Rationale**: NXD orchestrates autonomous agents that modify code
and merge branches. Untested orchestration logic risks silent data
corruption and irreversible git state changes.

### IV. Pluggable Architecture

Runtimes, LLM providers, and merge strategies MUST be swappable
via configuration — never hardcoded.

- All LLM calls MUST go through the `internal/llm` abstraction
  layer, never directly to a provider client.
- New runtimes MUST register through the plugin registry in
  `internal/runtime` and be selectable via `nxd.yaml`.
- Merge mode (local vs. GitHub) MUST be a config toggle, not a
  code branch.
- Agent roles are defined by configuration; adding a new role
  MUST NOT require changes to the dispatch or orchestration logic.

**Rationale**: Users range from air-gapped machines with 7B models
to cloud-connected teams with Opus. The architecture MUST serve
both without code changes.

### V. Immutable Data

ALWAYS create new objects; NEVER mutate existing ones.

- Functions MUST return new values rather than modifying inputs
  in place.
- Shared state (event store, projections, config) MUST NOT be
  mutated after initialization — use functional update patterns
  or copy-on-write semantics.
- Go slices and maps passed between packages MUST be defensively
  copied if the caller retains a reference.

**Rationale**: Immutable data prevents hidden side effects, makes
concurrent agent execution safe, and simplifies debugging of the
event-sourced pipeline.

### VI. Simplicity

Start simple. YAGNI. Complexity MUST be justified.

- Files SHOULD be 200–400 lines; MUST NOT exceed 800 lines.
- Functions MUST be under 50 lines with no more than 4 levels of
  nesting.
- Do not add abstractions, configuration knobs, or indirection
  layers for hypothetical future requirements.
- Three similar lines of code are preferable to a premature
  abstraction.

**Rationale**: NXD's codebase is already complex by nature (agent
orchestration, event sourcing, multiple runtimes). Simplicity in
individual components keeps the overall system comprehensible.

## Technical Constraints

- **Language**: Go 1.23+ (all production code).
- **State backend**: SQLite for projections; file-based append-only
  event log for the source of truth.
- **LLM abstraction**: `internal/llm` package — Ollama, Anthropic,
  and OpenAI clients behind a unified interface.
- **Runtime registry**: `internal/runtime` — Aider (default), Claude
  Code, Codex, Gemini CLI. New runtimes register here.
- **Agent hierarchy**: Tech Lead, Senior, Intermediate, Junior, QA,
  Supervisor — routed by Fibonacci complexity scoring.
- **Concurrency**: Wave-based parallel execution with topological
  dependency resolution via `internal/graph`.
- **CLI framework**: Cobra commands in `internal/cli`.
- **No CGO dependencies** unless explicitly justified and approved.
  Pure Go is strongly preferred for cross-platform portability.

## Development Workflow

1. **Plan**: Use the planner agent or `/speckit.plan` for non-trivial
   features. Identify dependencies and risks before writing code.
2. **Branch**: Create a feature branch with conventional naming
   (`feat/`, `fix/`, `refactor/`, etc.).
3. **TDD**: Write tests first (RED), implement (GREEN), refactor
   (IMPROVE). Verify 80%+ coverage.
4. **Review**: Use the code-reviewer agent after writing code.
   Address all CRITICAL and HIGH issues before proceeding.
5. **Security**: Before committing, verify:
   - No hardcoded secrets (API keys, passwords, tokens)
   - All user inputs validated
   - Error messages do not leak sensitive data
6. **Commit**: Conventional commits format
   (`feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`).
7. **CI gates**: `go vet`, `golangci-lint`, `go test -race`, and
   coverage threshold MUST pass before merge.

## Governance

This constitution supersedes all other development practices for the
NXD project. When a practice conflicts with a principle above, the
constitution wins.

- **Amendments** require: (1) a written proposal describing the
  change and its rationale, (2) review and approval, and (3) a
  migration plan for any existing code that violates the new rule.
- **Versioning** follows semantic versioning:
  - MAJOR: principle removal or backward-incompatible redefinition.
  - MINOR: new principle or materially expanded guidance.
  - PATCH: clarifications, wording, typo fixes.
- **Compliance review**: All PRs and code reviews MUST verify
  adherence to these principles. Violations MUST be flagged and
  resolved before merge.
- **Complexity justification**: Any deviation from Principle VI
  (Simplicity) MUST be documented in the PR description with a
  rationale for why the added complexity is necessary.

**Version**: 1.0.0 | **Ratified**: 2026-04-01 | **Last Amended**: 2026-04-01
