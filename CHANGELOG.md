# Changelog

All notable changes to Nexus Dispatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_(no entries yet — open a PR to add a line under the relevant subsection.)_

## [0.3.0] — 2026-09-03

Everything on `main` since 0.2.0 plus the `feat/attempts-sandbox-state` workstreams (attempt identity, event-log durability, sandboxed execution, human approvals, hardened LLM clients).

### Added
- **Story attempts**: every dispatch mints an attempt id (`<story-id>-a<ULID>`) stamped on `AGENT_SPAWNED`, `STORY_ASSIGNED`, `STORY_STARTED`, `STORY_PROGRESS`, `STORY_COMPLETED` and the pipeline's `STORY_QA_FAILED` / `STORY_REVIEW_FAILED` / `STORY_ESCALATED`; `Event.AttemptID` + `EventFilter.AttemptID` let one attempt be told apart from a re-dispatch, and native completion is attempt-scoped so a stale `STORY_COMPLETED` can no longer finish a later attempt
- **Sandboxed native tool execution** (`sandbox.{mode,image,network,extra_mounts,cpus,memory,auto_approve_prompts}`): the gemma runtime's `run_command`, the criteria evaluator's `command_succeeds`/`test_passes` and the investigator's `run_command` run through a `CommandSandbox` — `docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges` by default when the daemon is reachable, host argv exec (no shell) with a loud one-time warning otherwise; `nxd doctor` reports where agent commands run and names unsandboxed CLI runtimes
- **Human approval queue** (`internal/approvals`, `approvals.{require_for,timeout_action}`): conflict resolutions, integration-build failures, security findings and (opt-in) every merge create an `APPROVAL_REQUESTED` item that pauses the requirement until `nxd approvals approve|reject <id> [--note]` (or the dashboard **Approvals** panel / `GET /api/approvals`) records `APPROVAL_RESOLVED`
- **State maintenance**: `nxd state check|repair|compact|rebuild` (`state.Check/Repair/Compact/Rebuild`) — torn-tail and malformed-line detection, atomic repair with `.bak`, archiving of `STORY_PROGRESS`/`AGENT_CHECKPOINT` noise for completed requirements, and projection rebuild under the pipeline lock
- `nxd cancel <req|story> [--reason]`, `nxd version`, global `--state-dir`, `nxd resume --repo`, `nxd init --local-state` (repo-relative `./.nxd`, git-ignored), `--json` on `doctor`, `agents`, `events`, `escalations`
- `workspace.max_event_bytes` / `workspace.fsync_events` (event-log durability), `models.ollama_host`, `review.max_diff_bytes`, `models.<role>.num_ctx`, `qa.pause_on_integration_failure`, `security.gate_scope`, `security.llm_findings_block`, `qa.criteria_authoritative`
- **LLM clients**: typed `*APIError` (status, provider, redacted body, `Retry-After`) from every provider; 120 s default timeouts for Anthropic/OpenAI; context-aware Ollama back-off; real native tool calling for Anthropic (`tool_use`/`tool_result`), OpenAI (`tools`/`tool_calls`) and Google (`functionCall`/`functionResponse`); `sanitize.RedactSecrets` / `RedactPromptInjection` span redaction
- **Runners**: `runtimes.<name>.runner: docker|ssh` is honoured by the registry; secrets travel in a 0600 `.nxd-prompts/env.sh` / `--env-file` instead of argv; unattended flags (`--dangerously-skip-permissions`, `--full-auto`) are appended only when the runner is sandboxed
- `TryAcquireLock` / `ForceClearLock` for the pipeline lock, so read-only commands degrade instead of failing and `--force` only clears a lock whose holder is dead
- Notifications (`internal/notify`): webhook (JSON or Slack-compatible) and macOS desktop pushes for `REQ_COMPLETED`, `REQ_BLOCKED`, `REQ_PAUSED`, `HUMAN_REVIEW_NEEDED`, `STORY_SECURITY_FAILED`, budget events
- LLM budget guard: `billing.budget_usd` / `billing.budget_warn_pct` pause a requirement (`REQ_BUDGET_EXCEEDED`) before metered spend runs away
- `nxd timeline [req-id] [--json]` — chronological requirement history with per-story durations
- Security agent (`internal/security`, `engine/security_gate.go`): OWASP/CWE knowledge base, gosec/govulncheck/gitleaks/semgrep/npm-audit runner, per-story pre-merge gate, `nxd security scan|kb`, `SECURITY_*` events
- Requirement-completion gate (`qa.disable_completion_gate`, `qa.completion_fix_cycles`): `REQ_COMPLETED` only after the composed mainline builds and tests green; otherwise `REQ_BLOCKED` with gaps in `.nxd-fix-gaps.md`
- Planner factory stories (`planning.emit_integration_story`, `planning.emit_scribe_story`), docs subsystem (README/SVG/ADR generation), frontend design brief for UI stories
- Human-readable acceptance criteria in `nxd review`, the dashboard and prompts; `nxd doctor` projection-drift check; `cleanup.delete_dangling_branches`; `monitor.pipeline_timeout_s`
- CI: leak-check gate, per-package coverage floors, e2e and schema-drift lanes; migrations generated from `internal/state/sqlite.go` (`scripts/check-schema-drift.sh`)

### Changed
- Stories in `pr_submitted` / `merge_ready` are "awaiting merge": neither completed nor dispatchable. Dependents stay blocked and the monitor emits `REQ_PENDING_REVIEW` (with `open_pr_story_ids`) instead of `REQ_COMPLETED` or `PIPELINE_STALLED`; `REQ_COMPLETED` fires only when every story is merged or split
- Escalation tier is the **latest** `to_tier`, not the maximum ever reached, so a manager retry that lowers the tier really lowers it; `executeRetryAction` resets with `STORY_RESET` instead of a synthetic `STORY_REVIEW_FAILED`
- Integration-build failure after a merge now records `STORY_INTEGRATION_FAILED` (with the Tech Lead's `fix_hint` or `fix_error`) and pauses the requirement by default instead of branching the next wave from a red mainline
- `AGENT_STUCK` is emitted once per frozen-output episode with agent/story/attempt; tmux agents emit `AGENT_CHECKPOINT` on output change and the controller treats it as progress
- The command allowlist is argv-aware: an entry's tokens must equal the leading argv tokens; exec-style flags, env-var prefixes, absolute/`~`/`..` paths and shell metacharacters are rejected; an empty allowlist denies everything (was allow-all for the investigator)
- The reviewer diff is capped at `review.max_diff_bytes` with an explicit truncation marker, and a reply with no tool call and no parseable verdict is a **failed** review (fail closed)
- Per-model LLM pricing is deterministic (exact → longest prefix → `default`); unpriced models are reported instead of silently under-counted; `nxd report` scopes usage to the requirement and expands `~` in `state_dir`
- `nxd resume`/`req` take the pipeline lock before the projection rebuild check, verify the requirement's `repo_path` matches the current repository, and `req --background` releases the lock before forking
- `workspace.state_dir` is `~`-expanded and made absolute at load; relative values resolve against the config file's directory
- `merge.base_branch` defaults to empty so the repository's real default branch (`master` vs `main`) is detected; diffs, stats and rebases use the resolved base
- Background model-update checks are opt-in (`workspace.update_check: false`); `nxd init` generates project-type-aware success criteria; LLM-only security findings are advisory by default and the per-story gate is scoped to changed files
- CI runs per-package floors from the single coverage profile; `permissions: contents: write` is confined to the release job; govulncheck is pinned; the `nxd-automate` workflow is disabled with its reasons documented
- Docs: CLAUDE.md restructured (dated sections archived under `docs/history/`), configuration guide covers every key (enforced by a test), CLI/event references updated for the commands and events above

### Fixed
- Release binaries were built with `CGO_ENABLED=0` although `mattn/go-sqlite3` needs cgo; goreleaser now builds each target with cgo on its own OS
- QA re-runs on the rebased tree when the conflict resolver rewrote files during the rebase (`post_rebase_qa`), instead of merging unverified LLM-resolved code
- Conflicted files over the size limit are escalated (`file_too_large`) instead of truncated to 24 KB and written back; every LLM resolution is validated (no markers, no chatter, ≥ 50 % of input) before it touches disk; lock files are resolved deterministically
- Torn or oversized event-log lines no longer brick every command: the JSONL reader accepts 16 MB lines, payloads are truncated (never dropped) above `max_event_bytes`, appends fsync, torn tails are skipped and quarantined; `Project` and `RebuildFrom` run in single transactions
- Controller cancel terminates the tmux session of CLI agents (a "cancelled" aider/claude-code agent kept running); native-runtime goroutines drain on shutdown
- Watchdog stuck detection could never trip (the fingerprint timestamp was refreshed on every poll)
- Tool-call story splits produced duplicate child suffixes and were always rejected; children are now `<parent>-a`, `-b`, …
- Inline tool-call recovery no longer turns `{"name","arguments"}` objects quoted in prose into tool calls
- `SanitizingClient` scans tool-call arguments and redacts only the matched span (it replaced the whole response and ignored arguments)
- Investigator `read_file` refuses symlinks that resolve outside the repository; `cat /etc/passwd`, `find -exec` and friends are denied even with `cat`/`find` allowlisted
- Completion gate counted test packages that fail to *compile* as 0 failures; govulncheck that never ran was recorded as clean; the post-merge integration-fix goroutine was cancelled before it could run
- Transient Ollama overload (429/503/529, "server busy", "model is loading", OOM) pauses cleanly instead of burning the escalation chain
- Empty local merges are flagged; `List` with `Limit` returns the most recent N events; metrics count escalations from the event store; command output is carried into criteria failure summaries and escalation feedback; dashboard token cookie is seeded so CSS/JS load; gitleaks ANSI noise no longer corrupts the JSON report; devdb admin password no longer leaks into story PRs; planner retries empty Tech Lead responses and rejects degenerate plans; conflict paths with spaces/non-ASCII are parsed from `git status -z`

### Security
- Secrets never enter a command line or `ps`: CLI sessions source a 0600 env file, docker uses `--env-file`, ssh copies the file; every `%q` shell interpolation replaced by `QuoteShellArg`
- `DockerRunner` defaults to `--network none` (host is never allowed), `extra_flags` is an allowlist, `--cap-drop ALL --security-opt no-new-privileges` always on; devdb containers bind 127.0.0.1 only
- API error bodies are redacted (`sanitize.RedactSecrets`) before they are logged or surfaced
- Native `run_command` and the investigator share one hardened allowlist matcher (see *Changed*); `command_succeeds`/`test_passes` criteria go through `shellexec`, not `sh -c`
- Hostile requirement text is rejected before any LLM call; `WaveBrief` is sanitised before prompt injection; leak-check gate keeps private terms out of the public repo (terms now live in a git-ignored file / CI secret, not in the script)
- CI runs with `contents: read` except for the release job; GitHub Actions are pinned to commit SHAs
- `x/text` bumped to v0.39.0 (GO-2026-5970)

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
- Port 3 upstream fixes — split suffix, gitDiff master, duplicate validation

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
