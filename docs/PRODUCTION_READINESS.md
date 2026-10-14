# NXD Production Readiness

Status snapshot as of commit `7ddd15d` (2026-05-11). Everything in the
"What's done" section was verified directly in the repo at that commit —
not aspirational.

> **2026-07-02 update:** a dependency-upgrade fleet landed `14b2c7c`
> ("chore(deps): update Go and Python dependencies to latest") on branch
> `chore/update-deps-latest` — go.mod/go.sum minor & patch bumps plus one
> Python major (`mempalace 2.0.0 → 3.5.0` in requirements.txt). Lowest-risk
> upgrade in the portfolio; the commit claims build/vet/test pass but this
> was not independently re-verified. Run `go test ./...` locally before
> merging the branch to main.

## Current State

- **Codebase:** Go 1.26 module (`github.com/tzone85/nexus-dispatch`), 173
  source files / 193 test files under `cmd/`, `internal/` (27 packages),
  `test/` (E2E harness).
- **Version:** `0.1.0` released 2026-04-24 (see CHANGELOG.md); substantial
  unreleased work since (Bayesian routing, criteria-gated completion,
  repo learning, codegraph).
- **Storage:** file-based append-only event log + SQLite projections
  (`internal/state/`). No Postgres anywhere in the tree — if marketing copy
  mentions "ephemeral Postgres per story," correct it to "ephemeral
  SQLite/event-store state per run" before launch.
- **Distribution:** GoReleaser config (`.goreleaser.yml`, linux/darwin,
  amd64/arm64), multi-stage Alpine `Dockerfile` (non-root `nxd` user),
  `make release` / `make release-snapshot` targets.

## What's Done

### Tests & CI
- `make test` runs the full suite with `-race` and coverage; committed
  coverage roadmap at `docs/plans/coverage-roadmap-to-95.md`. Project notes
  (CLAUDE.md, 2026-05-04) record **77.8% total coverage**, 20 packages ≥ 85%.
- CI (`.github/workflows/ci.yml`): vet + race tests with a **50% coverage
  floor gate**, build matrix (ubuntu + macos), Docker image build,
  MemPalace bridge round-trip job, `govulncheck` scan (non-blocking),
  golangci-lint (non-blocking pending Go 1.26 support).
- E2E harness in `test/` (replay, dry-run, live scenarios) plus
  `llm.DryRunClient` / `ReplayClient` / `ErrorClient` test doubles.

### Security hardening (verified in code)
- Web dashboard binds **localhost only** (`internal/web/server.go`:
  `net.Listen("tcp", "localhost:<port>")`), CSP headers set, WebSocket
  origin allowlist restricted to `localhost:*` / `127.0.0.1:*`
  (`internal/web/ws.go`).
- Native runtime requires a non-empty `command_allowlist`; config
  validation rejects native runtimes without one and compiles all
  detection regexes at load time (ReDoS guard) — `internal/config/config.go`.
- Prompt/response sanitization layer (`internal/sanitize/`,
  `llm.NewSanitizingClient` wrapping every provider client).
- API keys come from env only; `ANTHROPIC_API_KEY` deliberately not
  exported into Claude Code agent sessions (`internal/runtime/registry.go`).
- Docker image runs as non-root user.
- Project notes record 7/8 audit findings resolved; **SG-7 (secrets
  manager) deferred** — keys still live as plain env vars (see gaps).

### Features (shipping at 7ddd15d)
- Event-sourced pipeline: intake → planning → wave dispatch → execution →
  review → QA → rebase/conflict resolution → merge → cleanup.
- Bayesian adaptive routing (`internal/routing/bayesian.go`) layered on
  Fibonacci complexity routing; priors persisted to `bayesian_priors.json`.
- Native Gemma runtime with criteria-gated completion + rejection budget;
  CLI runtimes (Aider, Claude Code, Codex) via tmux.
- TUI + web dashboards; web has `/healthz` and `/readyz` probes
  (`internal/web/health.go`), event bus → WebSocket push, DAG SVG.
- Cost estimation / client reports, reputation scoring, plugin system,
  MemPalace semantic memory bridge, repo learning, config schema
  versioning with forward-compat rules.

## Gap Checklist to Production

### (a) Engineering gaps (found in this audit)

- [ ] **`migrations/001_init.sql` has drifted from the embedded schema.**
      `internal/state/sqlite.go` `initSQL` includes
      `stories.acceptance_criteria`; the SQL file does not. The comment in
      sqlite.go claims they mirror each other. Sync them or delete the file
      and declare the embedded schema canonical.
- [ ] **No versioned migration story.** Single `001_init.sql`, schema
      applied via `CREATE TABLE IF NOT EXISTS` on store creation — fine for
      v0.1, but adding a column later has no upgrade path for existing
      `~/.nxd/nxd.db` files. Decide on a migration mechanism before schema
      v1.1.
- [ ] **README staleness** (partially fixed in this pass): Go badge said
      1.23+ vs go.mod 1.26.1; CLI table missing ~14 shipped commands
      (`approve`, `reject`, `pause`, `plan`, `spec`, `learn`, `improve`,
      `models`, `metrics`, `merge`, `review`, `direct`, `watch`, `archive`);
      no mention of Bayesian routing.
- [ ] **`nxd-automate.yml` workflow pins Go 1.23** while ci.yml and go.mod
      use 1.26 — that workflow will fail to build. Also pulls `gemma4:26b`
      (~18GB) on a GitHub-hosted runner per issue-labeled run; likely
      times out. Fix or disable before launch.
- [ ] **golangci-lint and govulncheck are non-blocking** in CI
      (`continue-on-error: true`). Acceptable pre-launch, but revisit the
      vulncheck gate for a security-marketed product.
- [ ] **Docker image is of limited use as shipped:** entrypoint is the CLI,
      but the pipeline needs Ollama (host service) and spawns tmux
      sessions; no docker-compose or documented `OLLAMA_HOST` wiring
      existed (now covered in docs/DEPLOYMENT.md). Decide whether Docker is
      a supported deployment target or a CI build check only.
- [ ] **Secrets management (SG-7)** deferred: API keys are plain env vars
      propagated via `tmux set-environment -g` (visible to anything that
      can talk to the tmux server). Acceptable single-user/local; document
      it as a known limitation for launch.
- [ ] **Coverage below stated 80% target** in cli (66%), tmux (73%),
      engine (74%), web (78%) — documented architectural ceilings, but the
      CI gate is only 50%. Consider ratcheting the floor to ~70%.
- [ ] **Pricing tiers have no enforcement hook.** The planned Free tier
      limit (1 concurrent agent) has a natural lever — the LLM semaphore
      (`internal/llm/semaphore.go`, default 1) and runtime `concurrency`
      config — but there is no license/entitlement check anywhere in the
      code. Pro/Team gating is unbuilt.
- [ ] **Repo hygiene:** `vxd.yaml` (sibling VXD project config) is tracked
      at the repo root; `coverage.out` (514KB) sits untracked in working
      trees — harmless but confusing for new clones. `PORTING_REQUIREMENTS.md`
      and `REQUIREMENT_PHASE1.md` read as internal planning docs; move to
      `docs/` or `.specify/` before public launch polish.
- [ ] **Version 0.1.0 vs launch:** cut a tagged release (v0.2.0 or v1.0.0)
      so `make release` / GoReleaser publishes binaries matching launch docs.

### (b) Owner decisions pending (launch blockers)

- [ ] **License.** Repo currently ships **Apache-2.0** (LICENSE file,
      README badge, NOTICE file, and the 0.1.0 release all say Apache 2.0).
      Advisory recommendation is BSL-1.1. Note: code has already been
      published under Apache 2.0 — moving to BSL applies to *future*
      versions only; existing releases remain Apache-licensed. Decide, then
      update LICENSE, NOTICE, README badge, and `.goreleaser.yml` release
      notes together. **Deliberately not changed in this audit.**
- [ ] **Pricing.** Proposed: Free OSS (1 concurrent agent) / Pro $20/mo
      (5 agents) / Team $99/mo. Needs the entitlement mechanism above.
- [ ] **Domain** — nxd.dev proposed; not referenced anywhere in the repo yet.
- [ ] **Billing rail** — Polar.sh proposed; no integration exists in code.
- [ ] **Hero copy** — README tagline exists ("Hand off a requirement, walk
      away, come back to merged code"); landing-page copy undecided.
- [ ] **90-second demo video.** A VHS tape exists (`docs/demo.tape`) and a
      hosted GIF is embedded in the README — raw material is ready, the
      produced video is not.

### (c) Launch-day runbook

1. **Freeze:** confirm CI green on `main`; run `make check` (vet + race
   tests + build) and `make vulncheck` locally.
2. **License:** commit the license decision (LICENSE, NOTICE via
   `make notice`, README badge) as its own PR.
3. **Version & release:** tag (`git tag vX.Y.Z && git push --tags`), run
   `make release` (GoReleaser publishes linux/darwin amd64/arm64 archives
   to GitHub Releases).
4. **Smoke the release artifact:** download binary, `nxd init`,
   `nxd doctor`, `nxd req --dry-run "smoke"`, `nxd dashboard --web` and
   check `http://localhost:8787/healthz` + `/readyz`.
5. **Docs:** verify README quickstart against the released binary;
   publish docs/DEPLOYMENT.md steps on the site.
6. **Site & billing:** point domain at landing page; wire billing rail
   checkout to Pro/Team (once entitlements exist — otherwise launch
   free-only and gate paid tiers behind a waitlist).
7. **Demo:** publish the 90-sec video; regenerate the README GIF
   (`vhs docs/demo.tape`) if the CLI surface changed.
8. **Announce**, then watch `stats/` (weekly traffic snapshots are
   already automated via `.github/workflows/traffic-stats.yml`).
9. **Rollback plan:** GoReleaser releases are additive — if the release
   binary is bad, delete the GitHub release + tag and re-tag; local users
   are unaffected (offline-first, no server component to roll back).

## Verdict

**6.5/10.** Engineering is genuinely strong for a v0.x — real test
discipline, CI gates, security-conscious defaults, honest docs. What keeps
it from 8+: the entire commercial layer (entitlements, billing, license
decision) is absent from the code, the schema-migration story is
single-file-and-drifting, and one CI workflow is broken. None of these are
deep problems; most are decisions plus days, not weeks, of work.
