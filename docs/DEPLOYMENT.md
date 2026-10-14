# NXD Deployment Runbook

Concrete, verified steps for building, releasing, and running NXD, based
on what exists in the repo at commit `7ddd15d`. NXD is a local-first CLI —
"deployment" usually means *distributing binaries* and *setting up a
workstation*, not running a server fleet.

## 1. Build from source

```bash
git clone https://github.com/tzone85/nexus-dispatch.git
cd nexus-dispatch
make setup       # go mod tidy + pip install MemPalace + nxd doctor
make check       # vet + race tests + build (the pre-push gate CI mirrors)
make install     # copies ./nxd to $(go env GOPATH)/bin
```

Requirements: Go 1.26+ (`go.mod` pins 1.26.1), git, tmux (for CLI
runtimes), Python 3.11+ with pip (for the MemPalace bridge), and
[Ollama](https://ollama.com) with at least one pulled model.

Useful extra targets (see `Makefile`):

| Target | What it does |
|--------|--------------|
| `make test` | Race-enabled tests + coverage summary |
| `make coverage-html` | Opens an HTML coverage report |
| `make vulncheck` | govulncheck over the dependency graph |
| `make doctor` | Builds then runs `nxd doctor` preflight |
| `make mempalace-check` | End-to-end smoke of the MemPalace bridge |
| `make notice` | Regenerates NOTICE from go.mod licenses |

## 2. Workstation setup (operator machine)

```bash
ollama pull gemma4:26b        # or gemma4:e4b on 16GB machines
nxd init                      # writes nxd.yaml + creates ~/.nxd state dirs
nxd doctor                    # preflight: go, git, tmux, ollama, config
nxd config validate
```

Configuration lives in `nxd.yaml` (schema-versioned, `version: "1.0"`);
`nxd.config.example.yaml` in the repo root is a documented reference.
Optional environment variables are documented in `.env.example` — with a
local Ollama, none are required.

### State & data locations

| Path | Contents |
|------|----------|
| `~/.nxd/` | State dir (`workspace.state_dir`) |
| `~/.nxd/events.jsonl` | Append-only event log (source of truth) |
| `~/.nxd/nxd.db` | SQLite projections (rebuildable from events) |
| `~/.nxd/nxd.lock` | Process lock file |
| `bayesian_priors.json` | Adaptive routing priors |

**Backup:** the event log is the source of truth; back up `~/.nxd/`.
**Reset (dev only):** `rm -f ~/.nxd/nxd.lock ~/.nxd/events.jsonl ~/.nxd/nxd.db`.

### Migrations

There is no separate migration step. The SQLite schema is embedded in
`internal/state/sqlite.go` and applied idempotently
(`CREATE TABLE IF NOT EXISTS ...`) when the projection store opens.
`migrations/001_init.sql` is a reference copy for external tooling —
**note:** at `7ddd15d` it lags the embedded schema (missing
`stories.acceptance_criteria`); trust the embedded schema.

## 3. Web dashboard

```bash
nxd dashboard --web --port 8787
```

- Binds **localhost only** (hardcoded in `internal/web/server.go`) — it
  cannot be exposed on 0.0.0.0 without a code change. To reach it from
  another machine, use an SSH tunnel:
  `ssh -L 8787:localhost:8787 user@host`.
- Probes: `GET /healthz` (liveness) and `GET /readyz` (readiness — checks
  event store + projection store), both unauthenticated, JSON responses.

## 4. Docker

The `Dockerfile` (multi-stage, Alpine, non-root `nxd` user, CGO-enabled
build for go-sqlite3) is verified by the CI `docker` job.

```bash
docker build -t nxd:latest .                          # dev build (symbols kept)
docker build --build-arg RELEASE=1 --build-arg VERSION=v0.1.0 -t nxd:v0.1.0 .
docker run --rm nxd:latest --help
```

Caveats — know these before treating Docker as a first-class target:

- **Ollama is not in the image.** Point the container at the host's
  Ollama: `-e OLLAMA_HOST=http://host.docker.internal:11434` (macOS/Win)
  or `--network host` plus `-e OLLAMA_HOST=http://localhost:11434` (Linux).
- **State**: mount a volume for `/home/nxd/.nxd` or state is lost when the
  container exits.
- **Your repo**: mount the project you want NXD to work on and run `nxd`
  from that directory.
- tmux and git are installed in the image, so CLI runtimes work, but
  interactive dashboards want a TTY (`-it`).

Example working invocation:

```bash
docker run -it --rm \
  -e OLLAMA_HOST=http://host.docker.internal:11434 \
  -v nxd-state:/home/nxd/.nxd \
  -v "$PWD":/work -w /work \
  nxd:latest req "Add input validation to the signup form"
```

## 5. Releasing binaries

Releases are GoReleaser-driven (`.goreleaser.yml`: linux + darwin,
amd64 + arm64, tar.gz/zip archives, publishes to
`github.com/tzone85/nexus-dispatch` releases).

```bash
# 1. Ensure main is green and NOTICE is current
make check && make vulncheck && make notice

# 2. Tag — GoReleaser derives the version from the latest tag
git tag v0.2.0
git push origin v0.2.0

# 3. Dry-run locally first, then release
make release-snapshot     # builds into dist/ without publishing
make release              # publishes the GitHub release

# 4. Smoke the published artifact
#    download → nxd init → nxd doctor → nxd req --dry-run "smoke"
```

CI also builds binaries per-push (ubuntu + macos artifacts) — see
`.github/workflows/ci.yml`.

## 6. Operational notes

- **Logging:** `NXD_LOG_LEVEL=debug` / `NXD_LOG_FORMAT=json` for
  aggregator-friendly output (see `.env.example`).
- **Stuck pipelines:** `nxd status`, `nxd escalations`, `nxd logs
  <story-id>`; the active controller (`controller.enabled: true`) can
  auto-restart stuck stories.
- **Cleanup:** `nxd gc --dry-run` then `nxd gc` prunes merged branches and
  worktrees past retention.
- **Update checker:** phones home for model/binary updates unless
  `NXD_UPDATE_CHECK=false` or `workspace.update_check: false` — relevant
  for air-gapped installs.

See [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) for the current gap
list and launch-day checklist.
