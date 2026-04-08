# Model Update Checker Design Spec

**Date:** 2026-04-08
**Status:** Approved
**Branch:** `feat/model-update-checker`

## Overview

Add a lightweight model update checker that notifies users when newer versions of their configured models are available. The checker queries Ollama registry and Google AI Studio, caches results to `~/.nxd/update-status.json`, and shows one-line notices in the CLI. Fully offline-safe: checks fail silently with a 3-second timeout, and an opt-out config flag exists for air-gapped environments.

### Constraints

- Never blocks CLI commands — checks run in fire-and-forget goroutines
- Never auto-downloads — only notifies with the command to run
- Never fails when offline — silent timeout, stale cache is kept
- Notices go to stderr, not stdout (no pipe pollution)
- `nxd models check` always works regardless of opt-out setting

---

## Section 1: Update Status Cache

### Cache File

Location: `~/.nxd/update-status.json`

```json
{
  "checked_at": "2026-04-08T10:00:00Z",
  "models": [
    {
      "name": "gemma4:26b",
      "source": "ollama",
      "local_digest": "5571076f3d70",
      "remote_digest": "8a2b3c4d5e6f",
      "update_available": true,
      "update_command": "ollama pull gemma4:26b"
    },
    {
      "name": "gemma-4-26b-a4b-it",
      "source": "google_ai",
      "current_version": "gemma-4-26b-a4b-it",
      "latest_version": "gemma-4-26b-a4b-it-002",
      "update_available": true,
      "update_command": "Update google_model in nxd.yaml to: gemma-4-26b-a4b-it-002"
    }
  ]
}
```

### Checker Logic

**New file:** `internal/update/checker.go`

```go
type CheckResult struct {
    CheckedAt time.Time      `json:"checked_at"`
    Models    []ModelStatus  `json:"models"`
}

type ModelStatus struct {
    Name            string `json:"name"`
    Source          string `json:"source"`            // "ollama" or "google_ai"
    LocalDigest     string `json:"local_digest,omitempty"`
    RemoteDigest    string `json:"remote_digest,omitempty"`
    CurrentVersion  string `json:"current_version,omitempty"`
    LatestVersion   string `json:"latest_version,omitempty"`
    UpdateAvailable bool   `json:"update_available"`
    UpdateCommand   string `json:"update_command"`
}
```

**Ollama check:** Compare local model digest via `GET http://localhost:11434/api/show` (POST with `{"model": "gemma4:26b"}`) against remote digest via `GET https://registry.ollama.ai/v2/library/{name}/manifests/{tag}`. If digests differ, update is available.

**Google AI check:** `GET https://generativelanguage.googleapis.com/v1beta/models?key={API_KEY}` returns available models. Compare configured `google_model` against the list to detect new versions (e.g., `-002` suffix). If no API key is set, skip this source silently.

**Timeouts:** Each source gets a 3-second HTTP client timeout. Failures are silently skipped — that source is omitted from results.

**Unique models:** The checker deduplicates models from the config. If all 7 roles use `gemma4:26b`, it checks once, not 7 times.

---

## Section 2: CLI Integration

### Background Check Hook

**Modified file:** `internal/cli/root.go` — Add `PersistentPreRun` hook.

On every `nxd` command:

1. If `update_check` is `false` in config or `NXD_UPDATE_CHECK=false` in env: skip.
2. Read `~/.nxd/update-status.json`.
3. If file doesn't exist or `checked_at` is older than `update_interval_hours` (default 48):
   - Spawn a fire-and-forget goroutine running `RunCheck()` with per-source 3-second timeouts.
   - The goroutine writes results to the cache file on success.
   - The current command does NOT wait.
4. If the cache has any `update_available: true`, print one-line notices to stderr:
   ```
   [update] gemma4:26b has a newer version available. Run: ollama pull gemma4:26b
   ```

**Key behaviors:**
- Notices come from the **cached** result, not the in-flight goroutine. First command after 48 hours sees stale data; next command sees fresh data. Zero latency impact.
- Maximum one notice per model, one line each.
- No interactive prompts, no blocking.
- Goroutine is fire-and-forget — CLI exiting before it completes is fine.

### `nxd models check` Command

**New file:** `internal/cli/models.go`

Runs the checker **synchronously** (blocks until complete) and prints a detailed report:

```
$ nxd models check

Checking Ollama registry...
  gemma4:26b          ✓ up to date (digest: 5571076f3d70)

Checking Google AI Studio...
  gemma-4-26b-a4b-it  ✓ up to date

Last checked: just now
Next auto-check: in 2 days
```

When updates are available:

```
$ nxd models check

Checking Ollama registry...
  gemma4:26b          ⬆ update available
                      Local:  5571076f3d70
                      Remote: 8a2b3c4d5e6f
                      Run:    ollama pull gemma4:26b

Checking Google AI Studio...
  gemma-4-26b-a4b-it  ✓ up to date

Last checked: just now
Next auto-check: in 2 days
```

This command also refreshes the cache, resetting the staleness timer. It always runs even when `update_check: false` (explicit manual check is always honored).

---

## Section 3: Opt-Out Configuration

### Config Fields

**Modified file:** `internal/config/config.go` — Add to `WorkspaceConfig`:

```go
type WorkspaceConfig struct {
    StateDir            string `yaml:"state_dir"`
    Backend             string `yaml:"backend"`
    LogLevel            string `yaml:"log_level"`
    LogRetentionDays    int    `yaml:"log_retention_days"`
    UpdateCheck         bool   `yaml:"update_check"`           // NEW
    UpdateIntervalHours int    `yaml:"update_interval_hours"`  // NEW
}
```

**Defaults** (in `loader.go`): `UpdateCheck: true`, `UpdateIntervalHours: 48`

### Behavior Matrix

| Config | Env Var | Background check | Notices | `nxd models check` |
|--------|---------|-----------------|---------|---------------------|
| `true` (default) | unset | Yes | Yes | Yes |
| `true` | `NXD_UPDATE_CHECK=false` | No | No | Yes |
| `false` | unset | No | No | Yes |
| `false` | `NXD_UPDATE_CHECK=true` | No | No | Yes |

Environment variable only disables, never enables (if config says false, env can't override to true).

### Validation

- `update_interval_hours` must be >= 0. Value of 0 is equivalent to `update_check: false`.
- No validation on `update_check` (boolean, defaults to true).

---

## Section 4: File Structure

### New Files (6)

| File | Purpose |
|------|---------|
| `internal/update/checker.go` | Core checker: `CheckOllama()`, `CheckGoogleAI()`, `RunCheck()` |
| `internal/update/cache.go` | Read/write `update-status.json`, staleness check |
| `internal/update/notify.go` | Format stderr notices, format detailed report |
| `internal/update/checker_test.go` | Tests with httptest mock servers |
| `internal/update/cache_test.go` | Cache read/write and expiry tests |
| `internal/cli/models.go` | `nxd models` and `nxd models check` commands |

### Modified Files (4)

| File | Change |
|------|--------|
| `internal/cli/root.go` | Add `PersistentPreRun` hook calling update check |
| `internal/config/config.go` | Add `UpdateCheck`, `UpdateIntervalHours` to `WorkspaceConfig` |
| `internal/config/loader.go` | Add defaults |
| `internal/config/config_test.go` | Test new fields |

### Test Files (2 new)

| File | Purpose |
|------|---------|
| `internal/update/checker_test.go` | Mock Ollama + Google AI responses, verify digest comparison, timeout handling, offline behavior |
| `internal/update/cache_test.go` | Cache write/read round-trip, staleness calculation, missing file handling |
