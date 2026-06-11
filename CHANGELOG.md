# Changelog

All notable changes to Nexus Dispatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_(no entries yet — open a PR to add a line under the relevant subsection.)_

## [0.2.0] — 2026-06-02

### Added
- `nxd req --background`: self-daemonize after planning (Setsid detach) so requirement runs survive parent shell teardown and macOS app-nap
- `nxd req-logs <req-id>`: tail the daemon log captured under `~/.nxd/logs/req-<req-id>.log`
- `nxd db` subtree (list, connect, sql, schema, delete, gc, ping, template list/create) for inspecting devdb-provisioned ephemeral databases
- Tech-Lead conflict resolver: textual three-way merges now go through the Tech-Lead LLM with binary-conflict short-circuit via `git numstat` + null-byte sniff
- Post-merge integration build runs after every merge to catch compile-level regressions; release binaries are stripped of debug symbols
- DevDB criteria types: `migration_succeeds`, `sql_query_returns`, `schema_changed` (gated by `.nxd-db/connect.env` provisioned by the lifecycle hook)
- Web dashboard: per-story **DB** column + aggregate **Databases** panel (created/failed/deleted counts) sourced from the `story_databases` projection
- `state.ListStoryDatabases(StoryDBFilter)`: projection-store read API for the per-story devdb status
- Bayesian adaptive routing (Beta distribution per role/complexity)
- Criteria-gated completion — agents self-correct before declaring done
- Criteria rejection budget — escalate instead of infinite thrashing
- Resume auto-select — automatically picks next ready wave
- Codegraph integration for AST-aware context building
- Repo Learning System (3-pass: static scan, git history, LLM deep dive)
- Comprehensive architecture documentation (22 sections + appendices)
- Contributing guide
- This changelog

### Fixed
- Prevent hallucination pass-through in QA and reviewer pipelines
- Reviewer plain-text fallback for non-JSON LLM responses
- Planning timeout increased from 5 to 15 minutes for local GPU models
- Auto-commit before rebase to prevent unstaged changes failure
- Graceful failure handling — artifact filter, re-planner guardrails, sub-story validation
- Command injection, path traversal, and input validation hardening
- Port 3 VXD fixes — split suffix, gitDiff master, duplicate validation

### Improved
- Engine test coverage: 66% → 72%
- CLI test coverage: 56% → 65%
- Dashboard test coverage: 57% → 84%
- Config, metrics, memory, runtime package coverage boosted

## [0.1.0] — 2026-04-24

### Added
- Initial open-source release
- Event-sourced architecture with SQLite projections
- Multi-tier agent dispatch (Junior, Intermediate, Senior, Tech Lead, QA)
- Wave-based parallel execution with DAG dependency resolution
- TUI dashboard (Bubbletea) with real-time updates
- Web dashboard (WebSocket) with DAG visualization
- 4 LLM providers: Ollama (local), Anthropic, OpenAI, Google
- 4 coding runtimes: Aider, Claude Code, Codex, Gemma (native)
- Automated code review with approve/reject/feedback
- QA pipeline with 6 declarative success criteria
- Auto-merge with conflict resolution
- Cost tracking and performance metrics
- Memory system (persistent cross-run knowledge)
- Scratchboard (cross-agent knowledge sharing)
- Plugin system for extensibility
- Doctor command (preflight checks)
- GoReleaser for binary distribution
- GitHub Actions CI/CD (test, lint, build, release)
