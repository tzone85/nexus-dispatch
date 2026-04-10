# Plugin System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a file-based plugin system supporting custom playbooks, prompt overrides, QA checks, and LLM providers — all without Go compilation.

**Architecture:** New `internal/plugin/` package loads plugins from `~/.nxd/plugins/` based on config. `PluginManager` holds loaded plugins and is passed to engine components. Custom providers use `SubprocessClient` (JSON stdin/stdout). Plugin playbooks/prompts wire into existing `SystemPrompt()`. Plugin QA checks insert into the existing check pipeline.

**Tech Stack:** Go 1.23+, `os/exec` for subprocess providers and QA scripts, `encoding/json` for provider protocol

**Spec:** `docs/superpowers/specs/2026-04-10-plugin-system-design.md`

---

## Phase 1: Config + Core Types

### Task 1: Plugin Config Types

**Files:**
- Create: `internal/config/plugins.go`
- Create: `internal/config/plugins_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`

- [ ] **Step 1: Create plugin config types**

```go
// internal/config/plugins.go
package config

// PluginConfig holds all plugin configuration.
type PluginConfig struct {
	Playbooks []PluginPlaybookConfig          `yaml:"playbooks"`
	Prompts   map[string]string               `yaml:"prompts"`
	QA        []PluginQAConfig                `yaml:"qa"`
	Providers map[string]PluginProviderConfig  `yaml:"providers"`
}

// PluginPlaybookConfig defines a custom diagnostic playbook.
type PluginPlaybookConfig struct {
	Name       string   `yaml:"name"`
	File       string   `yaml:"file"`
	InjectWhen string   `yaml:"inject_when"` // "existing", "bugfix", "infra", "always"
	Roles      []string `yaml:"roles"`       // empty = all roles
}

// PluginQAConfig defines a custom QA check script.
type PluginQAConfig struct {
	Name  string `yaml:"name"`
	File  string `yaml:"file"`
	After string `yaml:"after"` // "lint", "build", "test"
}

// PluginProviderConfig defines a custom LLM provider via subprocess.
type PluginProviderConfig struct {
	Command string   `yaml:"command"`
	Models  []string `yaml:"models"`
}
```

- [ ] **Step 2: Add Plugins field to Config struct**

In `internal/config/config.go`, add to `Config`:
```go
Plugins       PluginConfig             `yaml:"plugins"`
```

- [ ] **Step 3: Add empty default in loader.go**

In `DefaultConfig()`, add:
```go
Plugins: PluginConfig{},
```

- [ ] **Step 4: Write test**

```go
// internal/config/plugins_test.go
package config

import "testing"

func TestDefaultConfig_EmptyPlugins(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Plugins.Playbooks) != 0 {
		t.Error("expected empty playbooks by default")
	}
	if len(cfg.Plugins.Providers) != 0 {
		t.Error("expected empty providers by default")
	}
	if len(cfg.Plugins.QA) != 0 {
		t.Error("expected empty QA by default")
	}
	if len(cfg.Plugins.Prompts) != 0 {
		t.Error("expected empty prompts by default")
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/config/ -v && go test ./... -count=1`

- [ ] **Step 6: Commit**

```bash
git add internal/config/plugins.go internal/config/plugins_test.go internal/config/config.go internal/config/loader.go
git commit -m "feat: add plugin config types (playbooks, prompts, QA, providers)"
```

---

### Task 2: Plugin Loader + Playbook Type

**Files:**
- Create: `internal/plugin/loader.go`
- Create: `internal/plugin/loader_test.go`
- Create: `internal/plugin/playbook.go`

- [ ] **Step 1: Write playbook type**

```go
// internal/plugin/playbook.go
package plugin

import "strings"

// PluginPlaybook is a loaded custom diagnostic playbook.
type PluginPlaybook struct {
	Name       string
	Content    string
	InjectWhen string
	Roles      []string
}

// ShouldInject returns true if this playbook should be injected for the given role and context flags.
func (p PluginPlaybook) ShouldInject(role string, isExisting, isBugFix, isInfra bool) bool {
	// Check inject condition
	switch p.InjectWhen {
	case "existing":
		if !isExisting { return false }
	case "bugfix":
		if !isBugFix { return false }
	case "infra":
		if !isInfra { return false }
	case "always":
		// always inject
	default:
		return false
	}

	// Check role filter
	if len(p.Roles) == 0 {
		return true
	}
	for _, r := range p.Roles {
		if strings.EqualFold(r, role) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Write plugin loader**

```go
// internal/plugin/loader.go
package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// PluginQACheck is a loaded custom QA check.
type PluginQACheck struct {
	Name       string
	ScriptPath string
	After      string
}

// SubprocessInfo describes a custom LLM provider script.
type SubprocessInfo struct {
	Command string
	Models  []string
}

// PluginManager holds all loaded plugins.
type PluginManager struct {
	Playbooks []PluginPlaybook
	Prompts   map[string]string        // role name → override template
	QAChecks  []PluginQACheck
	Providers map[string]*SubprocessInfo
}

// LoadPlugins reads plugin config and loads files from the plugin directory.
// Returns an empty manager (not error) if no plugins configured.
func LoadPlugins(cfg config.PluginConfig, pluginDir string) (*PluginManager, error) {
	pm := &PluginManager{
		Prompts:   make(map[string]string),
		Providers: make(map[string]*SubprocessInfo),
	}

	// Load playbooks
	for _, pb := range cfg.Playbooks {
		path := resolvePath(pluginDir, "playbooks", pb.File)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("plugin playbook %q: %w", pb.Name, err)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("plugin playbook %q: file is empty", pb.Name)
		}
		pm.Playbooks = append(pm.Playbooks, PluginPlaybook{
			Name: pb.Name, Content: string(content),
			InjectWhen: pb.InjectWhen, Roles: pb.Roles,
		})
	}

	// Load prompt overrides
	for role, file := range cfg.Prompts {
		path := resolvePath(pluginDir, "prompts", file)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("plugin prompt %q: %w", role, err)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("plugin prompt %q: file is empty", role)
		}
		pm.Prompts[role] = string(content)
	}

	// Load QA checks
	for _, qa := range cfg.QA {
		path := resolvePath(pluginDir, "qa", qa.File)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("plugin QA %q: %w", qa.Name, err)
		}
		pm.QAChecks = append(pm.QAChecks, PluginQACheck{
			Name: qa.Name, ScriptPath: path, After: qa.After,
		})
	}

	// Load providers
	for name, prov := range cfg.Providers {
		path := resolvePath(pluginDir, "providers", prov.Command)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("plugin provider %q: %w", name, err)
		}
		pm.Providers[name] = &SubprocessInfo{Command: path, Models: prov.Models}
	}

	return pm, nil
}

// EmptyManager returns a PluginManager with no plugins loaded.
func EmptyManager() *PluginManager {
	return &PluginManager{
		Prompts:   make(map[string]string),
		Providers: make(map[string]*SubprocessInfo),
	}
}

func resolvePath(pluginDir, subdir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(pluginDir, subdir, file)
}
```

- [ ] **Step 3: Write tests**

```go
// internal/plugin/loader_test.go
package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestLoadPlugins_Empty(t *testing.T) {
	pm, err := LoadPlugins(config.PluginConfig{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pm.Playbooks) != 0 {
		t.Error("expected 0 playbooks")
	}
	if len(pm.Providers) != 0 {
		t.Error("expected 0 providers")
	}
}

func TestLoadPlugins_Playbook(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	os.MkdirAll(pbDir, 0755)
	os.WriteFile(filepath.Join(pbDir, "test.md"), []byte("# Test Playbook\nDo stuff."), 0644)

	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "test", File: "test.md", InjectWhen: "always", Roles: []string{"senior"}},
		},
	}

	pm, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if len(pm.Playbooks) != 1 {
		t.Fatalf("expected 1 playbook, got %d", len(pm.Playbooks))
	}
	if pm.Playbooks[0].Name != "test" {
		t.Errorf("name = %q", pm.Playbooks[0].Name)
	}
	if pm.Playbooks[0].Content != "# Test Playbook\nDo stuff." {
		t.Errorf("content mismatch")
	}
}

func TestLoadPlugins_PromptOverride(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	os.MkdirAll(promptDir, 0755)
	os.WriteFile(filepath.Join(promptDir, "tech_lead.md"), []byte("Custom Tech Lead prompt for {repo_path}"), 0644)

	cfg := config.PluginConfig{
		Prompts: map[string]string{"tech_lead": "tech_lead.md"},
	}

	pm, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if pm.Prompts["tech_lead"] == "" {
		t.Error("expected tech_lead prompt override")
	}
}

func TestLoadPlugins_QACheck(t *testing.T) {
	dir := t.TempDir()
	qaDir := filepath.Join(dir, "qa")
	os.MkdirAll(qaDir, 0755)
	os.WriteFile(filepath.Join(qaDir, "scan.sh"), []byte("#!/bin/bash\necho ok"), 0755)

	cfg := config.PluginConfig{
		QA: []config.PluginQAConfig{
			{Name: "scan", File: "scan.sh", After: "test"},
		},
	}

	pm, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if len(pm.QAChecks) != 1 {
		t.Fatalf("expected 1 QA check, got %d", len(pm.QAChecks))
	}
}

func TestLoadPlugins_Provider(t *testing.T) {
	dir := t.TempDir()
	provDir := filepath.Join(dir, "providers")
	os.MkdirAll(provDir, 0755)
	os.WriteFile(filepath.Join(provDir, "groq.sh"), []byte("#!/bin/bash\necho '{}'"), 0755)

	cfg := config.PluginConfig{
		Providers: map[string]config.PluginProviderConfig{
			"groq": {Command: "groq.sh", Models: []string{"llama-3.3-70b"}},
		},
	}

	pm, err := LoadPlugins(cfg, dir)
	if err != nil {
		t.Fatalf("LoadPlugins: %v", err)
	}
	if pm.Providers["groq"] == nil {
		t.Error("expected groq provider")
	}
	if pm.Providers["groq"].Command == "" {
		t.Error("expected non-empty command path")
	}
}

func TestLoadPlugins_MissingFile(t *testing.T) {
	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "missing", File: "nope.md", InjectWhen: "always"},
		},
	}
	_, err := LoadPlugins(cfg, t.TempDir())
	if err == nil {
		t.Error("expected error for missing playbook file")
	}
}

func TestLoadPlugins_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "playbooks"), 0755)
	os.WriteFile(filepath.Join(dir, "playbooks", "empty.md"), []byte(""), 0644)

	cfg := config.PluginConfig{
		Playbooks: []config.PluginPlaybookConfig{
			{Name: "empty", File: "empty.md", InjectWhen: "always"},
		},
	}
	_, err := LoadPlugins(cfg, dir)
	if err == nil {
		t.Error("expected error for empty playbook file")
	}
}

func TestPluginPlaybook_ShouldInject(t *testing.T) {
	pb := PluginPlaybook{InjectWhen: "existing", Roles: []string{"senior"}}
	if !pb.ShouldInject("senior", true, false, false) {
		t.Error("should inject for senior on existing codebase")
	}
	if pb.ShouldInject("junior", true, false, false) {
		t.Error("should NOT inject for junior (not in roles)")
	}
	if pb.ShouldInject("senior", false, false, false) {
		t.Error("should NOT inject when not existing codebase")
	}

	pbAlways := PluginPlaybook{InjectWhen: "always", Roles: nil}
	if !pbAlways.ShouldInject("junior", false, false, false) {
		t.Error("should inject for any role when always + empty roles")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/plugin/ -v && go test ./... -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/plugin/loader.go internal/plugin/loader_test.go internal/plugin/playbook.go
git commit -m "feat: add plugin loader with playbook, prompt, QA, provider support"
```

---

## Phase 2: Subprocess Provider + QA Plugin

### Task 3: SubprocessClient (Custom LLM Provider)

**Files:**
- Create: `internal/llm/subprocess.go`
- Create: `internal/llm/subprocess_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/llm/subprocess_test.go
package llm_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestSubprocessClient_Complete(t *testing.T) {
	// Create a mock provider script that echoes a valid response
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-provider.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read input
echo '{"content":"hello from subprocess","model":"test-model","usage":{"input_tokens":10,"output_tokens":5}}'
`), 0755)

	client := llm.NewSubprocessClient(script, 10*time.Second)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello from subprocess" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d", resp.Usage.InputTokens)
	}
}

func TestSubprocessClient_ScriptError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "bad-provider.sh")
	os.WriteFile(script, []byte("#!/bin/bash\nexit 1\n"), 0755)

	client := llm.NewSubprocessClient(script, 5*time.Second)
	_, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err == nil {
		t.Error("expected error for non-zero exit")
	}
}

func TestSubprocessClient_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "bad-json.sh")
	os.WriteFile(script, []byte("#!/bin/bash\nread input\necho 'not json'\n"), 0755)

	client := llm.NewSubprocessClient(script, 5*time.Second)
	_, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSubprocessClient_ImplementsInterface(t *testing.T) {
	var _ llm.Client = (*llm.SubprocessClient)(nil)
}
```

- [ ] **Step 2: Implement subprocess.go**

```go
// internal/llm/subprocess.go
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// SubprocessClient implements Client by calling an external script.
// The script receives a JSON CompletionRequest on stdin and must
// return a JSON CompletionResponse on stdout.
type SubprocessClient struct {
	command string
	timeout time.Duration
}

// NewSubprocessClient creates a client that delegates to an external script.
func NewSubprocessClient(command string, timeout time.Duration) *SubprocessClient {
	return &SubprocessClient{command: command, timeout: timeout}
}

func (c *SubprocessClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, c.command)
	cmd.Stdin = bytes.NewReader(reqJSON)

	out, err := cmd.Output()
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("subprocess provider %s: %w", c.command, err)
	}

	var resp CompletionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return CompletionResponse{}, fmt.Errorf("parse subprocess response: %w (output: %s)", err, string(out))
	}

	return resp, nil
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/llm/ -run TestSubprocess -v`

- [ ] **Step 4: Commit**

```bash
git add internal/llm/subprocess.go internal/llm/subprocess_test.go
git commit -m "feat: add SubprocessClient for custom LLM provider plugins"
```

---

### Task 4: Plugin QA Check Execution

**Files:**
- Create: `internal/plugin/qa.go`
- Create: `internal/plugin/qa_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/plugin/qa_test.go
package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPluginQACheck_Passes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "pass.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho 'all good'\nexit 0\n"), 0755)

	check := PluginQACheck{Name: "pass-check", ScriptPath: script, After: "test"}
	result := RunPluginQACheck(context.Background(), check, dir)

	if !result.Passed {
		t.Error("expected check to pass")
	}
	if result.Name != "pass-check" {
		t.Errorf("Name = %q", result.Name)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunPluginQACheck_Fails(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho 'vulnerability found'\nexit 1\n"), 0755)

	check := PluginQACheck{Name: "fail-check", ScriptPath: script, After: "test"}
	result := RunPluginQACheck(context.Background(), check, dir)

	if result.Passed {
		t.Error("expected check to fail")
	}
	if result.Output == "" {
		t.Error("expected output from failed check")
	}
}
```

- [ ] **Step 2: Implement qa.go**

```go
// internal/plugin/qa.go
package plugin

import (
	"context"
	"os/exec"
	"time"
)

// QACheckResult matches the engine's QACheckResult structure.
type QACheckResult struct {
	Name    string
	Passed  bool
	Output  string
	Elapsed time.Duration
}

// RunPluginQACheck executes a plugin QA script in the given working directory.
func RunPluginQACheck(ctx context.Context, check PluginQACheck, workDir string) QACheckResult {
	start := time.Now()

	cmd := exec.CommandContext(ctx, check.ScriptPath)
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	return QACheckResult{
		Name:    check.Name,
		Passed:  err == nil,
		Output:  string(out),
		Elapsed: elapsed,
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/plugin/ -v`

- [ ] **Step 4: Commit**

```bash
git add internal/plugin/qa.go internal/plugin/qa_test.go
git commit -m "feat: add plugin QA check execution"
```

---

## Phase 3: Wire Into Pipeline

### Task 5: Wire Plugins into Prompts

**Files:**
- Modify: `internal/agent/prompts.go`

- [ ] **Step 1: Add plugin integration to SystemPrompt**

Read `internal/agent/prompts.go`. The `SystemPrompt` function currently checks built-in playbooks. Add plugin support.

Add a package-level variable for plugin state:

```go
var (
	pluginPlaybooks      []pluginPlaybookEntry
	pluginPromptOverrides map[string]string
	pluginMu             sync.RWMutex
)

type pluginPlaybookEntry struct {
	Content    string
	InjectWhen string
	Roles      []string
}

// SetPlugins configures plugin playbooks and prompt overrides.
func SetPlugins(playbooks []pluginPlaybookEntry, prompts map[string]string) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	pluginPlaybooks = playbooks
	pluginPromptOverrides = prompts
}
```

In `SystemPrompt()`, at the very start, check for prompt override:

```go
pluginMu.RLock()
override, hasOverride := pluginPromptOverrides[string(role)]
pluginMu.RUnlock()

var base string
if hasOverride {
	base = replacePlaceholders(override, ctx)
} else {
	tmpl := promptTemplates[role]
	base = replacePlaceholders(tmpl, ctx)
}
```

After built-in playbook injection, add plugin playbooks:

```go
pluginMu.RLock()
for _, pb := range pluginPlaybooks {
	shouldInject := false
	switch pb.InjectWhen {
	case "existing":
		shouldInject = ctx.IsExistingCodebase
	case "bugfix":
		shouldInject = ctx.IsBugFix
	case "infra":
		shouldInject = ctx.IsInfrastructure
	case "always":
		shouldInject = true
	}
	if !shouldInject { continue }
	if len(pb.Roles) > 0 {
		roleMatch := false
		for _, r := range pb.Roles {
			if strings.EqualFold(r, string(role)) { roleMatch = true; break }
		}
		if !roleMatch { continue }
	}
	extras = append(extras, pb.Content)
}
pluginMu.RUnlock()
```

Add `"sync"` to imports.

- [ ] **Step 2: Run tests**

Run: `go test ./internal/agent/ -v && go test ./... -count=1`

- [ ] **Step 3: Commit**

```bash
git add internal/agent/prompts.go
git commit -m "feat: wire plugin playbooks and prompt overrides into SystemPrompt"
```

---

### Task 6: Wire Plugins into CLI (req.go + resume.go)

**Files:**
- Modify: `internal/cli/req.go`
- Modify: `internal/cli/resume.go`

- [ ] **Step 1: Wire plugins into req.go**

Read `internal/cli/req.go`. In `runReq()`, after loading config but before building the LLM client:

```go
// Load plugins
pluginDir := filepath.Join(expandHome(s.Config.Workspace.StateDir), "..", "plugins")
if home, err := os.UserHomeDir(); err == nil {
	pluginDir = filepath.Join(home, ".nxd", "plugins")
}
pm, err := plugin.LoadPlugins(s.Config.Plugins, pluginDir)
if err != nil {
	fmt.Fprintf(out, "Warning: plugin loading failed: %v\n", err)
	pm = plugin.EmptyManager()
}

// Apply plugin prompts and playbooks
var pbEntries []agent.PluginPlaybookEntry
for _, pb := range pm.Playbooks {
	pbEntries = append(pbEntries, agent.PluginPlaybookEntry{
		Content: pb.Content, InjectWhen: pb.InjectWhen, Roles: pb.Roles,
	})
}
agent.SetPlugins(pbEntries, pm.Prompts)
```

**IMPORTANT:** The actual type name for the playbook entry needs to match what's defined in prompts.go. Check and align.

In `buildLLMClient`, update the default case to check plugin providers:

```go
default:
	// Check plugin providers
	if pm != nil {
		if provInfo, ok := pm.Providers[provider]; ok {
			return llm.NewSubprocessClient(provInfo.Command, 5*time.Minute), nil
		}
	}
	return nil, fmt.Errorf("unknown LLM provider: %s", provider)
```

This requires `pm` to be accessible in `buildLLMClient`. The simplest approach: make `pm` a package-level variable set in `runReq`, or pass it as a parameter. Check the current signature and decide.

If `buildLLMClient` can't easily access `pm`, add a package-level `pluginProviders` map set from `runReq`:

```go
var pluginProviders map[string]*plugin.SubprocessInfo

// In runReq, after loading plugins:
pluginProviders = pm.Providers
```

- [ ] **Step 2: Wire plugins into resume.go**

Same pattern: load plugins at the top of `runResume`, apply to prompts. Plugin providers also need to be available for `buildLLMClient`.

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/nxd/ && go test ./... -count=1`

- [ ] **Step 4: Commit**

```bash
git add internal/cli/req.go internal/cli/resume.go
git commit -m "feat: wire plugin loader into req and resume CLI commands"
```

---

## Phase 4: Wiring Tests + Verification

### Task 7: Wiring Tests

**Files:**
- Modify: `internal/engine/wiring_test.go`

- [ ] **Step 1: Add 4 wiring tests**

```go
func TestWiring_PluginPlaybookInjected(t *testing.T) {
	// Set a plugin playbook, then check SystemPrompt includes it
	agent.SetPlugins(
		[]agent.PluginPlaybookEntry{
			{Content: "## Custom Security Audit\nCheck for hardcoded secrets.", InjectWhen: "always", Roles: nil},
		},
		nil,
	)
	defer agent.SetPlugins(nil, nil) // cleanup

	ctx := agent.PromptContext{TechStack: "go (go)"}
	prompt := agent.SystemPrompt(agent.RoleSenior, ctx)
	if !strings.Contains(prompt, "Custom Security Audit") {
		t.Error("expected plugin playbook in prompt")
	}
}

func TestWiring_PluginPromptOverrides(t *testing.T) {
	agent.SetPlugins(nil, map[string]string{
		"tech_lead": "Custom Tech Lead for {repo_path}. Decompose the requirement.",
	})
	defer agent.SetPlugins(nil, nil)

	ctx := agent.PromptContext{RepoPath: "/my/project", TechStack: "go (go)"}
	prompt := agent.SystemPrompt(agent.RoleTechLead, ctx)
	if !strings.Contains(prompt, "Custom Tech Lead for /my/project") {
		t.Error("expected custom prompt with placeholder substitution")
	}
	// Built-in playbooks should still inject
	ctx.IsExistingCodebase = true
	prompt2 := agent.SystemPrompt(agent.RoleTechLead, ctx)
	if !strings.Contains(prompt2, "BEFORE PLANNING") {
		t.Error("built-in CodebaseArchaeology should still inject on top of custom prompt")
	}
}

func TestWiring_PluginQACheckRuns(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "check.sh")
	os.WriteFile(script, []byte("#!/bin/bash\necho 'passed'\nexit 0\n"), 0755)

	check := plugin.PluginQACheck{Name: "test-check", ScriptPath: script, After: "test"}
	result := plugin.RunPluginQACheck(context.Background(), check, dir)
	if !result.Passed {
		t.Error("expected QA check to pass")
	}
	if result.Name != "test-check" {
		t.Errorf("Name = %q", result.Name)
	}
}

func TestWiring_SubprocessProviderCompletes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "provider.sh")
	os.WriteFile(script, []byte(`#!/bin/bash
read input
echo '{"content":"plugin response","model":"custom","usage":{"input_tokens":50,"output_tokens":25}}'
`), 0755)

	client := llm.NewSubprocessClient(script, 10*time.Second)
	resp, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "custom"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "plugin response" {
		t.Errorf("Content = %q", resp.Content)
	}
}
```

Add imports: `plugin`, `os`, `context`, `filepath` if not present.

- [ ] **Step 2: Run all wiring tests**

Run: `go test ./internal/engine/ -run TestWiring -v`

- [ ] **Step 3: Commit**

```bash
git add internal/engine/wiring_test.go
git commit -m "test: add wiring tests for plugin playbooks, prompts, QA, and subprocess provider"
```

---

### Task 8: Final Verification

- [ ] **Step 1: Full test suite**

Run: `go test ./... -count=1`

- [ ] **Step 2: Build + CLI**

Run: `go build -o /tmp/nxd ./cmd/nxd/ && /tmp/nxd --help`

- [ ] **Step 3: Verify config**

Run: `cd /tmp && rm -f nxd.yaml && /tmp/nxd init && /tmp/nxd config show | grep -A2 plugins`

- [ ] **Step 4: Commit any fixes**

```bash
git status
```
