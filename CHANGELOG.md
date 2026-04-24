# Changelog

All notable changes to Nexus Dispatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
