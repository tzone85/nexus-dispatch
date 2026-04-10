# Plugin System Design Spec

**Date:** 2026-04-10
**Status:** Approved
**Branch:** `feat/plugin-system`

## Overview

A file-based plugin system that lets users extend NXD without modifying source code. Four extension points: custom diagnostic playbooks (markdown files), custom prompt templates (override role prompts), custom QA checks (executable scripts), and custom LLM providers (subprocess JSON protocol). Config declares what's active, files contain the content.

### Constraints

- No Go compilation needed — plugins are files (markdown, shell scripts, any-language scripts)
- Backward compatible — empty `plugins:` section or missing `~/.nxd/plugins/` = no overhead
- Plugin playbooks append to (not replace) built-in playbooks
- Plugin prompts replace the base template but diagnostic playbooks still inject on top
- Plugin QA checks follow the same pass/fail contract as built-in checks
- Plugin providers use a simple stdin/stdout JSON protocol

---

## Section 1: Plugin Directory Structure

**Convention:** `~/.nxd/plugins/` is the plugin root.

```
~/.nxd/plugins/
├── playbooks/           # Custom diagnostic playbooks (.md)
├── prompts/             # Role prompt overrides (.md)
├── qa/                  # QA check scripts (executable)
└── providers/           # LLM provider scripts (executable, JSON stdin/stdout)
```

**Config declares what's active** in `nxd.yaml`:

```yaml
plugins:
  playbooks:
    - name: security-audit
      file: security-audit.md
      inject_when: "existing"    # "existing", "bugfix", "infra", "always"
      roles: ["senior", "intermediate"]  # empty = all roles

  prompts:
    tech_lead: tech_lead.md      # overrides default Tech Lead prompt
    senior: senior.md

  qa:
    - name: security-scan
      file: security-scan.sh
      after: "test"              # "lint", "build", "test"

  providers:
    groq:
      command: groq.sh
      models: ["llama-3.3-70b"]
```

File paths are relative to `~/.nxd/plugins/<type>/`. Absolute paths also work.

---

## Section 2: Custom Playbooks

Markdown files injected into agent system prompts based on conditions.

**File format:** Plain markdown. No frontmatter, no special syntax. Config controls injection.

**Injection logic:** After built-in playbook injection in `SystemPrompt()`:

```go
for _, pb := range pluginPlaybooks {
    if pb.ShouldInject(role, ctx) {
        extras = append(extras, pb.Content)
    }
}
```

**ShouldInject rules:**
- `inject_when: "existing"` → only when `ctx.IsExistingCodebase`
- `inject_when: "bugfix"` → only when `ctx.IsBugFix`
- `inject_when: "infra"` → only when `ctx.IsInfrastructure`
- `inject_when: "always"` → every time
- `roles` list → only for matching roles (empty = all roles)

**Types:**

```go
type PluginPlaybook struct {
    Name       string
    Content    string
    InjectWhen string
    Roles      []string
}
```

---

## Section 3: Custom Prompt Templates

Markdown files that override the default system prompt for a role.

**File format:** Markdown with `{placeholder}` substitution (same placeholders as built-in: `{team_name}`, `{repo_path}`, `{tech_stack}`, `{story_id}`, etc.)

**Override logic:** In `SystemPrompt()`, check for plugin override before built-in template:

```go
if override, ok := pluginPromptOverrides[role]; ok {
    base := replacePlaceholders(override, ctx)
    // Still append diagnostic playbooks on top
    return base + extras
}
// Fall back to built-in
```

**Key principle:** Override replaces ONLY the base template. Diagnostic playbooks (built-in + custom) still append. A custom Tech Lead prompt still gets CodebaseArchaeology when working on an existing codebase.

**Validation:** File non-empty, role name valid.

---

## Section 4: Custom QA Checks

Executable scripts that run in the story's worktree. Exit 0 = pass, non-zero = fail. Stdout/stderr captured as check output.

**Execution flow:**

Built-in: lint → build → test

With plugins: lint → [after-lint plugins] → build → [after-build plugins] → test → [after-test plugins]

**Config:**

```yaml
qa:
  - name: security-scan
    file: security-scan.sh
    after: "test"
```

**Type:**

```go
type PluginQACheck struct {
    Name       string
    ScriptPath string
    After      string
}
```

**Security:** Scripts run with NXD's permissions. Same trust level as runtimes.

**Failure handling:** Plugin QA failures follow the same path as built-in failures — output fed back to agent, then escalation if retry fails.

---

## Section 5: Custom LLM Providers (Subprocess Protocol)

Scripts that speak JSON over stdin/stdout, implementing the LLM completion API.

**Protocol:**

Request (JSON on stdin):
```json
{
  "model": "llama-3.3-70b",
  "messages": [{"role": "user", "content": "..."}],
  "max_tokens": 4096,
  "temperature": 0.0,
  "tools": [],
  "tool_choice": ""
}
```

Response (JSON on stdout):
```json
{
  "content": "Response text...",
  "model": "llama-3.3-70b",
  "usage": {"input_tokens": 500, "output_tokens": 200},
  "tool_calls": []
}
```

Non-zero exit = LLM error.

**Go implementation:** `SubprocessClient` implementing `llm.Client`:

```go
type SubprocessClient struct {
    command string
    timeout time.Duration
}
```

**Registration:** In `buildLLMClient`, the default case checks plugin providers:

```go
default:
    if pluginProvider, ok := pluginProviders[provider]; ok {
        return llm.NewSubprocessClient(pluginProvider.Command, 5*time.Minute), nil
    }
    return nil, fmt.Errorf("unknown provider: %s", provider)
```

**User config:**
```yaml
models:
  tech_lead:
    provider: groq
    model: llama-3.3-70b
```

**Tool calling:** If script returns `tool_calls`, NXD handles them. If not, graceful degradation (schema in prompt).

---

## Section 6: Plugin Loader + Files Changed

### Plugin Loader

**New package:** `internal/plugin/`

```go
type PluginManager struct {
    Playbooks []PluginPlaybook
    Prompts   map[string]string
    QAChecks  []PluginQACheck
    Providers map[string]*SubprocessInfo
}

func LoadPlugins(cfg PluginConfig, pluginDir string) (*PluginManager, error)
```

Loads on startup: reads config, resolves file paths, loads content, validates. Returns empty manager if no plugins configured.

### New Files (8)

| File | Purpose |
|------|---------|
| `internal/plugin/loader.go` | LoadPlugins, PluginManager |
| `internal/plugin/loader_test.go` | Tests with temp plugin dirs |
| `internal/plugin/playbook.go` | PluginPlaybook, ShouldInject |
| `internal/plugin/qa.go` | PluginQACheck, script execution |
| `internal/llm/subprocess.go` | SubprocessClient implementing llm.Client |
| `internal/llm/subprocess_test.go` | Tests with mock scripts |
| `internal/config/plugins.go` | PluginConfig and sub-types |
| `internal/config/plugins_test.go` | Config parsing tests |

### Modified Files (7)

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `Plugins PluginConfig` to Config |
| `internal/config/loader.go` | Add empty Plugins default |
| `internal/agent/prompts.go` | Check plugin overrides, inject plugin playbooks |
| `internal/engine/qa.go` | Insert plugin QA checks at configured positions |
| `internal/cli/req.go` | Load plugins, check plugin providers |
| `internal/cli/resume.go` | Load plugins, pass to QA |
| `internal/cli/root.go` | No changes needed |

### Wiring Tests (4 new)

| Test | What it proves |
|------|---------------|
| PluginPlaybookInjected | Custom playbook in SystemPrompt when conditions match |
| PluginPromptOverrides | Custom prompt replaces built-in, playbooks still append |
| PluginQACheckRuns | Script exit code determines pass/fail |
| SubprocessProviderCompletes | JSON stdin/stdout protocol works |
