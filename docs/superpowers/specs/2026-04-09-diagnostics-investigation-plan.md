# Diagnostics, Investigation & Existing Codebase Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add requirement classification, a dedicated Investigator agent for pre-planning codebase analysis, and four diagnostic playbooks injected into agent prompts — transforming NXD from greenfield-only to full existing-codebase support.

**Architecture:** Three layers wired in sequence: (1) `ClassifyRepo` heuristic + `ClassifyRequirement` LLM call determine flags, (2) `Investigator` agent runs 6-phase investigation on existing repos producing a structured report, (3) diagnostic playbooks are injected into agent system/goal prompts based on the flags. All new stages are skipped for greenfield repos.

**Tech Stack:** Go 1.23+, existing `llm.Client` interface, `state.EventStore`/`SQLiteStore`, Gemma native runtime for investigator

**Spec:** `docs/superpowers/specs/2026-04-09-diagnostics-investigation-design.md`

---

## File Structure

```
internal/engine/
├── classifier.go           # ClassifyRepo (heuristic) + ClassifyRequirement (LLM)
├── classifier_test.go      # Tests with temp repos + mock LLM
├── investigator.go         # Investigator engine — runs investigation, produces report
├── investigator_test.go    # Tests with fixture repos, verify report structure

internal/agent/
├── diagnostics.go          # Four playbook constants
├── diagnostics_test.go     # Verify playbooks contain key sections
├── investigator.go         # RoleInvestigator definition, system prompt, tool schemas
├── investigator_test.go    # Investigator prompt + tool definition tests

internal/agent/prompts.go   # MODIFIED — add flags to PromptContext, injection logic
internal/agent/roles.go     # MODIFIED — add RoleInvestigator
internal/config/config.go   # MODIFIED — add Investigator to ModelsConfig
internal/config/loader.go   # MODIFIED — add Investigator defaults
internal/cli/req.go         # MODIFIED — insert classification + investigation before planning
internal/engine/planner.go  # MODIFIED — PlanWithContext accepting RequirementContext
internal/state/events.go    # MODIFIED — add REQ_CLASSIFIED, INVESTIGATION_COMPLETED
internal/state/sqlite.go    # MODIFIED — add columns to requirements table
```

---

### Task 1: Repo Classification (Heuristic)

**Files:**
- Create: `internal/engine/classifier.go`
- Create: `internal/engine/classifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/classifier_test.go
package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func createTestRepo(t *testing.T, fileCount int, commitCount int) string {
	t.Helper()
	dir := t.TempDir()

	// Init go module
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
	}

	run("go", "mod", "init", "testproject")
	run("git", "init")

	// Create source files
	for i := 0; i < fileCount; i++ {
		name := filepath.Join(dir, "pkg", "file"+string(rune('a'+i))+".go")
		os.MkdirAll(filepath.Dir(name), 0755)
		os.WriteFile(name, []byte("package pkg\n"), 0644)
	}

	// Create test files if source count > 3
	if fileCount > 3 {
		testFile := filepath.Join(dir, "pkg", "file_test.go")
		os.WriteFile(testFile, []byte("package pkg\n\nimport \"testing\"\n\nfunc TestPlaceholder(t *testing.T) {}\n"), 0644)
	}

	// Create commits
	for i := 0; i < commitCount; i++ {
		marker := filepath.Join(dir, "marker"+string(rune('0'+i))+".txt")
		os.WriteFile(marker, []byte("commit "+string(rune('0'+i))), 0644)
		run("git", "add", "-A")
		run("git", "commit", "-m", "commit "+string(rune('0'+i)))
	}

	return dir
}

func TestClassifyRepo_Greenfield(t *testing.T) {
	repo := createTestRepo(t, 2, 3)

	profile := ClassifyRepo(repo)

	if profile.IsExisting {
		t.Error("expected greenfield (IsExisting=false) for small repo")
	}
	if profile.Language != "go" {
		t.Errorf("Language = %q, want %q", profile.Language, "go")
	}
}

func TestClassifyRepo_Existing(t *testing.T) {
	repo := createTestRepo(t, 8, 15)

	profile := ClassifyRepo(repo)

	if !profile.IsExisting {
		t.Error("expected existing codebase (IsExisting=true) for large repo")
	}
	if profile.SourceFileCount < 8 {
		t.Errorf("SourceFileCount = %d, want >= 8", profile.SourceFileCount)
	}
	if profile.CommitCount < 15 {
		t.Errorf("CommitCount = %d, want >= 15", profile.CommitCount)
	}
	if !profile.TestsExist {
		t.Error("expected TestsExist=true")
	}
}

func TestClassifyRepo_DetectsCI(t *testing.T) {
	repo := createTestRepo(t, 8, 15)
	os.MkdirAll(filepath.Join(repo, ".github", "workflows"), 0755)
	os.WriteFile(filepath.Join(repo, ".github", "workflows", "ci.yml"), []byte("name: CI"), 0644)

	profile := ClassifyRepo(repo)

	if !profile.HasCI {
		t.Error("expected HasCI=true")
	}
}

func TestClassifyRepo_DetectsDocker(t *testing.T) {
	repo := createTestRepo(t, 8, 15)
	os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM golang:1.23"), 0644)

	profile := ClassifyRepo(repo)

	if !profile.HasDocker {
		t.Error("expected HasDocker=true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestClassifyRepo -v`
Expected: FAIL — ClassifyRepo not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/engine/classifier.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// RepoProfile describes the state of a target repository.
type RepoProfile struct {
	IsExisting      bool
	Language        string
	BuildTool       string
	SourceFileCount int
	TestFileCount   int
	CommitCount     int
	TopDirs         []string
	HasCI           bool
	HasDocker       bool
	BuildHealthy    bool
	TestsExist      bool
}

// RequirementClassification describes the intent of a requirement.
type RequirementClassification struct {
	Type       string   `json:"type"`
	Confidence float64  `json:"confidence"`
	Signals    []string `json:"signals"`
}

// RequirementContext bundles classification and investigation results.
type RequirementContext struct {
	Repo           RepoProfile
	Classification RequirementClassification
	Report         *InvestigationReport
	IsExisting     bool
	IsBugFix       bool
	IsRefactor     bool
	IsInfra        bool
}

// ClassifyRepo scans a repository and determines if it's an existing codebase.
// No LLM calls — pure file and git analysis.
func ClassifyRepo(repoPath string) RepoProfile {
	stack := nxdgit.ScanRepo(repoPath)

	profile := RepoProfile{
		Language:  stack.Language,
		BuildTool: stack.BuildTool,
	}

	// Count source files by language
	sourceExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".java": true, ".rs": true, ".rb": true, ".c": true, ".cpp": true,
	}
	testPatterns := []string{"_test.go", "test_", "_test.py", ".test.js", ".test.ts", ".spec.js", ".spec.ts"}

	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && (info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if sourceExts[ext] {
			profile.SourceFileCount++
			for _, pat := range testPatterns {
				if strings.Contains(filepath.Base(path), pat) {
					profile.TestFileCount++
					break
				}
			}
		}
		return nil
	})

	profile.TestsExist = profile.TestFileCount > 0

	// Count commits
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = repoPath
	if out, err := cmd.Output(); err == nil {
		count, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		profile.CommitCount = count
	}

	// Top-level directories
	entries, _ := os.ReadDir(repoPath)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			profile.TopDirs = append(profile.TopDirs, e.Name())
		}
	}

	// CI detection
	if _, err := os.Stat(filepath.Join(repoPath, ".github", "workflows")); err == nil {
		profile.HasCI = true
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".gitlab-ci.yml")); err == nil {
		profile.HasCI = true
	}

	// Docker detection
	if _, err := os.Stat(filepath.Join(repoPath, "Dockerfile")); err == nil {
		profile.HasDocker = true
	}
	if _, err := os.Stat(filepath.Join(repoPath, "docker-compose.yml")); err == nil {
		profile.HasDocker = true
	}

	// Build health (30-second timeout)
	profile.BuildHealthy = checkBuildHealth(repoPath, stack)

	// Heuristic: existing if >5 source files AND >10 commits
	profile.IsExisting = profile.SourceFileCount > 5 && profile.CommitCount > 10

	return profile
}

func checkBuildHealth(repoPath string, stack nxdgit.TechStack) bool {
	var cmd *exec.Cmd
	switch stack.Language {
	case "go":
		cmd = exec.Command("go", "build", "./...")
	case "javascript", "typescript":
		cmd = exec.Command("npm", "run", "build")
	case "python":
		cmd = exec.Command("python", "-m", "py_compile", ".")
	default:
		return true // assume healthy if we can't check
	}
	cmd.Dir = repoPath

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmd.Dir = repoPath

	return cmd.Run() == nil
}

// ClassifyRequirement uses an LLM call to determine the intent of a requirement.
// Only called for existing codebases.
func ClassifyRequirement(ctx context.Context, client llm.Client, requirement string, profile RepoProfile) (RequirementClassification, error) {
	prompt := fmt.Sprintf(`Classify this requirement into exactly one type.

Requirement: %s

Repository context: %s project, %d source files, %d test files, %d commits.

Respond with JSON only:
{"type": "<feature|bugfix|refactor|infrastructure>", "confidence": <0.0-1.0>, "signals": ["reason1", "reason2"]}

Types:
- feature: new functionality, new endpoints, new modules
- bugfix: fixing broken behavior, error messages, incorrect output
- refactor: restructuring existing code, improving quality, reducing tech debt
- infrastructure: Docker, CI/CD, deployment, build system, monitoring`,
		requirement, profile.Language, profile.SourceFileCount, profile.TestFileCount, profile.CommitCount)

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Model:     "gemma4:26b",
		System:    "You are a requirement classifier. Respond with JSON only. No prose.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		MaxTokens: 200,
	})
	if err != nil {
		return RequirementClassification{Type: "feature", Confidence: 0.5}, nil
	}

	var result RequirementClassification
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return RequirementClassification{Type: "feature", Confidence: 0.5}, nil
	}

	return result, nil
}

// BuildRequirementContext combines repo profile, classification, and investigation results.
func BuildRequirementContext(profile RepoProfile, classification RequirementClassification, report *InvestigationReport) RequirementContext {
	return RequirementContext{
		Repo:           profile,
		Classification: classification,
		Report:         report,
		IsExisting:     profile.IsExisting,
		IsBugFix:       classification.Type == "bugfix",
		IsRefactor:     classification.Type == "refactor",
		IsInfra:        classification.Type == "infrastructure",
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/engine/ -run TestClassifyRepo -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/classifier.go internal/engine/classifier_test.go
git commit -m "feat: add repo classification heuristic and requirement intent classifier"
```

---

### Task 2: Diagnostic Playbooks

**Files:**
- Create: `internal/agent/diagnostics.go`
- Create: `internal/agent/diagnostics_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/diagnostics_test.go
package agent

import (
	"strings"
	"testing"
)

func TestCodebaseArchaeology_ContainsKeySteps(t *testing.T) {
	text := CodebaseArchaeology
	for _, keyword := range []string{"Investigation Report", "modules", "test coverage", "build health", "file ownership", "existing patterns"} {
		if !strings.Contains(text, keyword) {
			t.Errorf("CodebaseArchaeology missing keyword %q", keyword)
		}
	}
}

func TestBugHuntingMethodology_ContainsPhases(t *testing.T) {
	text := BugHuntingMethodology
	for _, phase := range []string{"REPRODUCE", "ISOLATE", "ROOT CAUSE", "MINIMAL FIX", "VERIFY"} {
		if !strings.Contains(text, phase) {
			t.Errorf("BugHuntingMethodology missing phase %q", phase)
		}
	}
}

func TestBugHuntingMethodology_ContainsBugPatterns(t *testing.T) {
	text := BugHuntingMethodology
	for _, pattern := range []string{"Nil", "Race condition", "Off-by-one", "Resource leak"} {
		if !strings.Contains(text, pattern) {
			t.Errorf("BugHuntingMethodology missing pattern %q", pattern)
		}
	}
}

func TestInfrastructureDebugging_ContainsDomains(t *testing.T) {
	text := InfrastructureDebugging
	for _, domain := range []string{"Docker", "Database", "CI/CD", "Network", "Environment"} {
		if !strings.Contains(text, domain) {
			t.Errorf("InfrastructureDebugging missing domain %q", domain)
		}
	}
}

func TestLegacyCodeSurvival_ContainsRules(t *testing.T) {
	text := LegacyCodeSurvival
	for _, rule := range []string{"NEVER rewrite", "characterization test", "small, safe steps", "git blame", "What NOT to Do"} {
		if !strings.Contains(text, rule) {
			t.Errorf("LegacyCodeSurvival missing rule %q", rule)
		}
	}
}

func TestAllPlaybooks_NonEmpty(t *testing.T) {
	playbooks := map[string]string{
		"CodebaseArchaeology":      CodebaseArchaeology,
		"BugHuntingMethodology":    BugHuntingMethodology,
		"InfrastructureDebugging":  InfrastructureDebugging,
		"LegacyCodeSurvival":      LegacyCodeSurvival,
	}
	for name, text := range playbooks {
		if len(text) < 200 {
			t.Errorf("%s is too short (%d chars), expected substantial content", name, len(text))
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run "TestCodebase|TestBugHunting|TestInfra|TestLegacy|TestAllPlaybooks" -v`
Expected: FAIL — constants not defined

- [ ] **Step 3: Write the implementation**

Create `internal/agent/diagnostics.go` with the four playbook constants as defined in the spec (Section 3). Each is a `const string` containing the full playbook text. The file should contain:

- `const CodebaseArchaeology string` — 6-step Tech Lead orientation
- `const BugHuntingMethodology string` — 5-phase debugging workflow + common bug patterns
- `const InfrastructureDebugging string` — Docker/DB/CI/network/env diagnostics + common failures
- `const LegacyCodeSurvival string` — Golden rules, safe refactoring steps, what NOT to do, handling missing tests

Each constant contains the EXACT text from the spec's Section 3.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/diagnostics.go internal/agent/diagnostics_test.go
git commit -m "feat: add four diagnostic playbooks for existing codebase support"
```

---

### Task 3: Investigator Role Definition

**Files:**
- Create: `internal/agent/investigator.go`
- Create: `internal/agent/investigator_test.go`
- Modify: `internal/agent/roles.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/investigator_test.go
package agent

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func TestRoleInvestigator_Exists(t *testing.T) {
	if RoleInvestigator != "investigator" {
		t.Errorf("RoleInvestigator = %q, want %q", RoleInvestigator, "investigator")
	}
}

func TestRoleInvestigator_ExecutionMode(t *testing.T) {
	mode := RoleInvestigator.ExecutionMode()
	if mode != ExecHybrid {
		t.Errorf("Investigator ExecutionMode = %q, want %q", mode, ExecHybrid)
	}
}

func TestRoleInvestigator_ModelConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	mc := RoleInvestigator.ModelConfig(cfg.Models)
	if mc.Model == "" {
		t.Error("expected non-empty model for Investigator")
	}
}

func TestInvestigatorTools_Definitions(t *testing.T) {
	tools := InvestigatorTools()
	if len(tools) < 3 {
		t.Fatalf("expected at least 3 investigator tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("tool %q has invalid parameters: %v", tool.Name, err)
		}
	}

	for _, name := range []string{"read_file", "run_command", "submit_report"} {
		if !names[name] {
			t.Errorf("missing investigator tool %q", name)
		}
	}
}

func TestInvestigatorSystemPrompt_NonEmpty(t *testing.T) {
	prompt := InvestigatorSystemPrompt()
	if len(prompt) < 100 {
		t.Errorf("investigator prompt too short (%d chars)", len(prompt))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run TestRoleInvestigator -v`
Expected: FAIL — RoleInvestigator not defined

- [ ] **Step 3: Add RoleInvestigator to roles.go**

In `internal/agent/roles.go`, add `RoleInvestigator Role = "investigator"` to the const block. Add `case RoleInvestigator: return ExecHybrid` to `ExecutionMode()`. Add `case RoleInvestigator: return models.Investigator` to `ModelConfig()`.

- [ ] **Step 4: Add Investigator to ModelsConfig**

In `internal/config/config.go`, add `Investigator ModelConfig` to `ModelsConfig` struct. Update `All()` to include `"investigator": m.Investigator`.

In `internal/config/loader.go`, add `Investigator: gemma4Default(16000)` to the Models block in `DefaultConfig()`.

- [ ] **Step 5: Write investigator.go**

```go
// internal/agent/investigator.go
package agent

import (
	"encoding/json"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// InvestigatorSystemPrompt returns the system prompt for the Investigator role.
func InvestigatorSystemPrompt() string {
	return `You are a Codebase Investigator for NXD, an AI agent orchestration system.
Your job is to thoroughly analyze an existing codebase BEFORE any planning or implementation begins.

You have access to tools: read_file, run_command, submit_report.

Follow these 6 investigation phases IN ORDER:

Phase 1: ORIENTATION
- Read README.md, CLAUDE.md, Makefile, docker-compose.yml if they exist
- Identify the project's purpose and entry points

Phase 2: ARCHITECTURE
- List source files (find . -name "*.go" -o -name "*.py" -o -name "*.js" | head -50)
- Identify the largest files (wc -l on source files, sort by size)
- Read package/module boundaries

Phase 3: HEALTH CHECK
- Run the build command (go build ./... or npm run build)
- Run the test suite (go test ./... or npm test)
- Check test coverage if available (go test -cover ./...)

Phase 4: DEPENDENCY GRAPH
- Check dependency manifests (go.mod, package.json, requirements.txt)
- Map internal module dependencies via imports

Phase 5: CODE SMELLS
- Files exceeding 500 lines
- Source files with no corresponding test file
- Count of TODO/FIXME comments
- Deeply nested code (>4 levels)
- Hardcoded values (URLs, ports, credentials)

Phase 6: RISK ASSESSMENT
- Check git log for recent churn: git log --since=30d --name-only --pretty=format: | sort | uniq -c | sort -rn | head -20
- Cross-reference high-churn files with test coverage
- Identify untested critical paths

After all 6 phases, call submit_report with a structured JSON report.`
}

// InvestigatorTools returns the tool definitions available to the Investigator.
func InvestigatorTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read the contents of a file in the project.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"}},"required":["path"]}`),
		},
		{
			Name:        "run_command",
			Description: "Run a shell command in the project directory. Use for: ls, find, wc, grep, git, go build, go test, npm.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"}},"required":["command"]}`),
		},
		{
			Name:        "submit_report",
			Description: "Submit the final investigation report. Call this ONCE after completing all 6 phases.",
			Parameters: json.RawMessage(`{"type":"object","properties":{
				"summary":{"type":"string","description":"200-word architecture brief"},
				"entry_points":{"type":"array","items":{"type":"string"}},
				"modules":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"path":{"type":"string"},"file_count":{"type":"integer"},"line_count":{"type":"integer"},"has_tests":{"type":"boolean"}},"required":["name","path"]}},
				"build_passes":{"type":"boolean"},
				"test_passes":{"type":"boolean"},
				"test_count":{"type":"integer"},
				"coverage_pct":{"type":"number"},
				"code_smells":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"severity":{"type":"string"},"description":{"type":"string"}},"required":["file","severity","description"]}},
				"risk_areas":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"reason":{"type":"string"},"severity":{"type":"string"}},"required":["file","reason","severity"]}},
				"recommendations":{"type":"array","items":{"type":"string"}}
			},"required":["summary","entry_points","build_passes","test_passes","recommendations"]}`),
		},
	}
}
```

- [ ] **Step 6: Run all tests**

Run: `go test ./internal/agent/ -v && go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agent/investigator.go internal/agent/investigator_test.go internal/agent/roles.go internal/config/config.go internal/config/loader.go
git commit -m "feat: add Investigator role with tool schemas and model config"
```

---

### Task 4: Investigator Engine

**Files:**
- Create: `internal/engine/investigator.go`
- Create: `internal/engine/investigator_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/engine/investigator_test.go
package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestInvestigator_Investigate_ProducesReport(t *testing.T) {
	reportJSON := `{
		"summary": "A Go CLI project with 10 source files",
		"entry_points": ["cmd/main.go"],
		"build_passes": true,
		"test_passes": true,
		"test_count": 15,
		"coverage_pct": 72.5,
		"modules": [{"name": "engine", "path": "internal/engine", "file_count": 5, "line_count": 800, "has_tests": true}],
		"code_smells": [{"file": "main.go", "severity": "medium", "description": "file exceeds 500 lines"}],
		"risk_areas": [{"file": "engine/monitor.go", "reason": "high churn, low coverage", "severity": "high"}],
		"recommendations": ["Add tests for monitor.go", "Split main.go into smaller files"]
	}`

	client := llm.NewReplayClient(
		// First response: investigator calls tools
		llm.CompletionResponse{
			Model: "gemma4:26b",
			ToolCalls: []llm.ToolCall{
				{Name: "read_file", Arguments: json.RawMessage(`{"path": "README.md"}`)},
			},
		},
		// Second response: more tools
		llm.CompletionResponse{
			Model: "gemma4:26b",
			ToolCalls: []llm.ToolCall{
				{Name: "run_command", Arguments: json.RawMessage(`{"command": "go build ./..."}`)},
			},
		},
		// Third response: submit report
		llm.CompletionResponse{
			Model: "gemma4:26b",
			ToolCalls: []llm.ToolCall{
				{Name: "submit_report", Arguments: json.RawMessage(reportJSON)},
			},
		},
	)

	repo := createTestRepo(t, 10, 15)

	investigator := NewInvestigator(client, "gemma4:26b", 16000)
	report, err := investigator.Investigate(context.Background(), repo)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	if report.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if !report.BuildStatus.Passes {
		t.Error("expected BuildStatus.Passes=true")
	}
	if len(report.Recommendations) == 0 {
		t.Error("expected recommendations")
	}
	if len(report.CodeSmells) == 0 {
		t.Error("expected code smells")
	}
}

func TestInvestigator_Investigate_HandlesNoReport(t *testing.T) {
	// Model never calls submit_report — should return error
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "I couldn't analyze the codebase", Model: "gemma4:26b"},
	)

	repo := createTestRepo(t, 3, 5)
	investigator := NewInvestigator(client, "gemma4:26b", 16000)

	_, err := investigator.Investigate(context.Background(), repo)
	if err == nil {
		t.Error("expected error when model doesn't submit report")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestInvestigator -v`
Expected: FAIL — Investigator type not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/engine/investigator.go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// InvestigationReport is the structured output from the Investigator.
type InvestigationReport struct {
	Summary     string       `json:"summary"`
	EntryPoints []string     `json:"entry_points"`
	Modules     []ModuleInfo `json:"modules"`
	BuildStatus HealthStatus `json:"build_status"`
	TestStatus  HealthStatus `json:"test_status"`
	CodeSmells  []CodeSmell  `json:"code_smells"`
	RiskAreas   []RiskArea   `json:"risk_areas"`
	Recommendations []string `json:"recommendations"`
}

type ModuleInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
	LineCount int    `json:"line_count"`
	HasTests  bool   `json:"has_tests"`
}

type HealthStatus struct {
	Passes   bool    `json:"passes"`
	Output   string  `json:"output,omitempty"`
	Count    int     `json:"count,omitempty"`
	Coverage float64 `json:"coverage,omitempty"`
}

type CodeSmell struct {
	File        string `json:"file"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

type RiskArea struct {
	File     string `json:"file"`
	Reason   string `json:"reason"`
	Severity string `json:"severity"`
}

// Investigator runs a 6-phase codebase investigation.
type Investigator struct {
	client    llm.Client
	model     string
	maxTokens int
}

// NewInvestigator creates an Investigator with the given LLM client.
func NewInvestigator(client llm.Client, model string, maxTokens int) *Investigator {
	return &Investigator{client: client, model: model, maxTokens: maxTokens}
}

// Investigate runs the investigation loop and returns a structured report.
func (inv *Investigator) Investigate(ctx context.Context, repoPath string) (*InvestigationReport, error) {
	tools := agent.InvestigatorTools()
	systemPrompt := agent.InvestigatorSystemPrompt()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: fmt.Sprintf("Investigate the codebase at %s. Follow all 6 phases, then call submit_report.", repoPath)},
	}

	maxIterations := 20
	for i := 0; i < maxIterations; i++ {
		resp, err := inv.client.Complete(ctx, llm.CompletionRequest{
			Model:     inv.model,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: inv.maxTokens,
		})
		if err != nil {
			return nil, fmt.Errorf("investigator LLM call %d: %w", i, err)
		}

		if len(resp.ToolCalls) == 0 {
			return nil, fmt.Errorf("investigator did not produce a report (no tool calls at iteration %d)", i)
		}

		// Add assistant message
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: resp.ToolCalls})

		for _, call := range resp.ToolCalls {
			switch call.Name {
			case "submit_report":
				return parseInvestigationReport(call.Arguments)

			case "read_file":
				content := inv.execReadFile(call.Arguments, repoPath)
				messages = append(messages, llm.Message{Role: llm.RoleTool, Content: content, ToolCallID: call.ID})

			case "run_command":
				output := inv.execRunCommand(call.Arguments, repoPath)
				messages = append(messages, llm.Message{Role: llm.RoleTool, Content: output, ToolCallID: call.ID})

			default:
				messages = append(messages, llm.Message{Role: llm.RoleTool, Content: fmt.Sprintf("unknown tool: %s", call.Name), ToolCallID: call.ID})
			}
		}
	}

	return nil, fmt.Errorf("investigator exceeded %d iterations without submitting report", maxIterations)
}

func parseInvestigationReport(args json.RawMessage) (*InvestigationReport, error) {
	var raw struct {
		Summary         string       `json:"summary"`
		EntryPoints     []string     `json:"entry_points"`
		Modules         []ModuleInfo `json:"modules"`
		BuildPasses     bool         `json:"build_passes"`
		TestPasses      bool         `json:"test_passes"`
		TestCount       int          `json:"test_count"`
		CoveragePct     float64      `json:"coverage_pct"`
		CodeSmells      []CodeSmell  `json:"code_smells"`
		RiskAreas       []RiskArea   `json:"risk_areas"`
		Recommendations []string     `json:"recommendations"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("parse investigation report: %w", err)
	}

	return &InvestigationReport{
		Summary:     raw.Summary,
		EntryPoints: raw.EntryPoints,
		Modules:     raw.Modules,
		BuildStatus: HealthStatus{Passes: raw.BuildPasses},
		TestStatus:  HealthStatus{Passes: raw.TestPasses, Count: raw.TestCount, Coverage: raw.CoveragePct},
		CodeSmells:  raw.CodeSmells,
		RiskAreas:   raw.RiskAreas,
		Recommendations: raw.Recommendations,
	}, nil
}

func (inv *Investigator) execReadFile(args json.RawMessage, repoPath string) string {
	var p struct{ Path string `json:"path"` }
	if err := json.Unmarshal(args, &p); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	cleaned := filepath.Clean(p.Path)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "error: path traversal blocked"
	}
	data, err := os.ReadFile(filepath.Join(repoPath, cleaned))
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	// Truncate large files
	content := string(data)
	if len(content) > 8000 {
		content = content[:8000] + "\n... (truncated)"
	}
	return content
}

func (inv *Investigator) execRunCommand(args json.RawMessage, repoPath string) string {
	var p struct{ Command string `json:"command"` }
	if err := json.Unmarshal(args, &p); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	cmd := exec.Command("sh", "-c", p.Command)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	result := string(out)
	if err != nil {
		result += "\nexit error: " + err.Error()
	}
	// Truncate large output
	if len(result) > 4000 {
		result = result[:4000] + "\n... (truncated)"
	}
	return result
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/engine/ -run TestInvestigator -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/investigator.go internal/engine/investigator_test.go
git commit -m "feat: add Investigator engine with 6-phase codebase analysis"
```

---

### Task 5: Prompt Injection Logic

**Files:**
- Modify: `internal/agent/prompts.go`
- Create: `internal/agent/prompts_test.go` (or add to existing test file)

- [ ] **Step 1: Write the failing test**

```go
// internal/agent/prompts_test.go
package agent

import (
	"strings"
	"testing"
)

func TestSystemPrompt_ExistingCodebase_TechLead(t *testing.T) {
	ctx := PromptContext{
		IsExistingCodebase: true,
		TechStack:          "go (go)",
		RepoPath:           "/tmp/test",
	}
	prompt := SystemPrompt(RoleTechLead, ctx)
	if !strings.Contains(prompt, "Investigation Report") {
		t.Error("expected CodebaseArchaeology in TechLead prompt for existing codebase")
	}
}

func TestSystemPrompt_ExistingCodebase_Senior(t *testing.T) {
	ctx := PromptContext{
		IsExistingCodebase: true,
		TechStack:          "go (go)",
	}
	prompt := SystemPrompt(RoleSenior, ctx)
	if !strings.Contains(prompt, "REPRODUCE") {
		t.Error("expected BugHuntingMethodology in Senior prompt for existing codebase")
	}
	if !strings.Contains(prompt, "NEVER rewrite") {
		t.Error("expected LegacyCodeSurvival in Senior prompt for existing codebase")
	}
}

func TestSystemPrompt_BugFix_Intermediate(t *testing.T) {
	ctx := PromptContext{
		IsBugFix:  true,
		TechStack: "go (go)",
	}
	prompt := SystemPrompt(RoleIntermediate, ctx)
	if !strings.Contains(prompt, "REPRODUCE") {
		t.Error("expected BugHuntingMethodology in Intermediate prompt for bug fix")
	}
}

func TestSystemPrompt_Infrastructure_Junior(t *testing.T) {
	ctx := PromptContext{
		IsInfrastructure: true,
		TechStack:        "go (go)",
	}
	prompt := SystemPrompt(RoleJunior, ctx)
	if !strings.Contains(prompt, "Docker") {
		t.Error("expected InfrastructureDebugging in Junior prompt for infra")
	}
}

func TestSystemPrompt_Greenfield_NoPlaybooks(t *testing.T) {
	ctx := PromptContext{
		TechStack: "go (go)",
	}
	prompt := SystemPrompt(RoleSenior, ctx)
	if strings.Contains(prompt, "REPRODUCE") {
		t.Error("BugHuntingMethodology should NOT be in greenfield Senior prompt")
	}
	if strings.Contains(prompt, "NEVER rewrite") {
		t.Error("LegacyCodeSurvival should NOT be in greenfield Senior prompt")
	}
}

func TestGoalPrompt_BugFix_HasWorkflow(t *testing.T) {
	ctx := PromptContext{
		IsBugFix:     true,
		StoryTitle:   "Fix auth bug",
		StoryDescription: "JWT tokens expire early",
	}
	goal := GoalPrompt(RoleSenior, ctx)
	if !strings.Contains(goal, "REPRODUCE") {
		t.Error("expected bug fix workflow in goal prompt")
	}
}

func TestGoalPrompt_Existing_HasOrientWorkflow(t *testing.T) {
	ctx := PromptContext{
		IsExistingCodebase: true,
		StoryTitle:         "Add feature",
		StoryDescription:   "Add new endpoint",
	}
	goal := GoalPrompt(RoleIntermediate, ctx)
	if !strings.Contains(goal, "ORIENT") {
		t.Error("expected orientation workflow in goal prompt for existing codebase")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/ -run "TestSystemPrompt_Existing|TestSystemPrompt_BugFix|TestSystemPrompt_Infra|TestSystemPrompt_Green|TestGoalPrompt" -v`
Expected: FAIL — new PromptContext fields don't exist

- [ ] **Step 3: Update prompts.go**

Add to `PromptContext` struct:
```go
IsExistingCodebase  bool
IsBugFix            bool
IsRefactor          bool
IsInfrastructure    bool
InvestigationReport string // formatted markdown, injected by planner
```

Update `SystemPrompt()` to append playbooks after the base template:

```go
func SystemPrompt(role Role, ctx PromptContext) string {
	tmpl := promptTemplates[role]
	base := replacePlaceholders(tmpl, ctx)

	var extras []string

	// Existing codebase playbooks
	if ctx.IsExistingCodebase {
		switch role {
		case RoleTechLead:
			extras = append(extras, CodebaseArchaeology)
		case RoleSenior:
			extras = append(extras, BugHuntingMethodology, LegacyCodeSurvival)
		case RoleIntermediate, RoleJunior:
			extras = append(extras, LegacyCodeSurvival)
		}
	}

	// Bug fix playbook (if not already injected via IsExisting)
	if ctx.IsBugFix && !ctx.IsExistingCodebase {
		if role == RoleSenior || role == RoleIntermediate {
			extras = append(extras, BugHuntingMethodology)
		}
	}

	// Infrastructure playbook (all roles)
	if ctx.IsInfrastructure {
		extras = append(extras, InfrastructureDebugging)
	}

	// Investigation report for Tech Lead
	if ctx.InvestigationReport != "" && role == RoleTechLead {
		extras = append(extras, ctx.InvestigationReport)
	}

	if len(extras) > 0 {
		return base + "\n\n" + strings.Join(extras, "\n\n")
	}
	return base
}
```

Add `"strings"` to imports if not present.

Update `GoalPrompt()` to append mandatory workflow steps based on flags:

After the existing goal template construction, append:

```go
if ctx.IsExistingCodebase {
	goal += "\n\nMANDATORY WORKFLOW FOR EXISTING CODEBASE:\n1. ORIENT: ls -la, read README.md, read CLAUDE.md\n2. MAP: find source files relevant to this story\n3. HISTORY: git log --oneline -15\n4. BASELINE: run existing test suite, record what passes\n5. SEARCH: grep for functions/types related to this story\n6. READ: open and read the relevant files\n7. THEN implement, matching existing code style"
}
if ctx.IsBugFix {
	goal += "\n\nMANDATORY BUG FIX WORKFLOW:\n1. REPRODUCE: write a failing test\n2. ISOLATE: read stack trace, add logging\n3. ROOT CAUSE: understand WHY it's broken\n4. FIX: minimal change only\n5. VERIFY: test passes, full suite passes, no regressions"
}
if ctx.IsInfrastructure {
	goal += "\n\nMANDATORY INFRASTRUCTURE WORKFLOW:\n1. Check services: docker ps -a, lsof for LISTEN\n2. Check logs: docker logs --tail 50, journalctl\n3. Check config: env vars, .env, docker-compose.yml\n4. Check resources: df -h, memory\n5. Fix and verify with health checks"
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agent/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/prompts.go internal/agent/prompts_test.go
git commit -m "feat: add conditional playbook injection into agent prompts"
```

---

### Task 6: Event Types and Schema Migration

**Files:**
- Modify: `internal/state/events.go`
- Modify: `internal/state/sqlite.go`

- [ ] **Step 1: Add new event types**

In `internal/state/events.go`, add to the const block after `EventReqCompleted`:

```go
EventReqClassified           EventType = "REQ_CLASSIFIED"
EventInvestigationCompleted  EventType = "INVESTIGATION_COMPLETED"
```

- [ ] **Step 2: Add schema migration in sqlite.go**

In `internal/state/sqlite.go`, add to the migration block in `NewSQLiteStore` (after existing ALTER TABLE statements):

```go
// Migrate: add classification columns to requirements
db.Exec(`ALTER TABLE requirements ADD COLUMN req_type TEXT NOT NULL DEFAULT ''`)
db.Exec(`ALTER TABLE requirements ADD COLUMN is_existing BOOLEAN NOT NULL DEFAULT 0`)
db.Exec(`ALTER TABLE requirements ADD COLUMN investigation_report_json TEXT NOT NULL DEFAULT ''`)
```

Add projection handlers in `Project()`:

```go
case EventReqClassified:
	reqID, _ := payload["req_id"].(string)
	reqType, _ := payload["req_type"].(string)
	isExisting, _ := payload["is_existing"].(bool)
	isExistingInt := 0
	if isExisting {
		isExistingInt = 1
	}
	_, err := s.db.Exec(`UPDATE requirements SET req_type = ?, is_existing = ? WHERE id = ?`, reqType, isExistingInt, reqID)
	return err

case EventInvestigationCompleted:
	reqID, _ := payload["req_id"].(string)
	reportJSON, _ := payload["report"].(string)
	_, err := s.db.Exec(`UPDATE requirements SET investigation_report_json = ? WHERE id = ?`, reportJSON, reqID)
	return err
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: Success

- [ ] **Step 4: Run all state tests**

Run: `go test ./internal/state/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/state/events.go internal/state/sqlite.go
git commit -m "feat: add REQ_CLASSIFIED and INVESTIGATION_COMPLETED events with schema migration"
```

---

### Task 7: Pipeline Integration (req.go + planner.go)

**Files:**
- Modify: `internal/cli/req.go`
- Modify: `internal/engine/planner.go`

- [ ] **Step 1: Update req.go**

In `runReq()`, after loading stores and before creating the planner, insert the classification and investigation stages:

```go
// After: repoPath, _ := os.Getwd() (or however repoPath is determined)

// Stage 1: Classify repo
repoProfile := engine.ClassifyRepo(repoPath)

// Stage 2: Classify requirement (only for existing repos)
var classification engine.RequirementClassification
if repoProfile.IsExisting {
	classification, _ = engine.ClassifyRequirement(ctx, client, requirement, repoProfile)
	fmt.Fprintf(out, "Detected: %s codebase, requirement type: %s (confidence: %.0f%%)\n",
		repoProfile.Language, classification.Type, classification.Confidence*100)
} else {
	classification = engine.RequirementClassification{Type: "feature", Confidence: 1.0}
	fmt.Fprintf(out, "Detected: greenfield project (%s)\n", repoProfile.Language)
}

// Stage 3: Investigate (only for existing repos)
var report *engine.InvestigationReport
if repoProfile.IsExisting {
	fmt.Fprintf(out, "Running codebase investigation...\n")
	investigatorModel := s.Config.Models.Investigator
	inv := engine.NewInvestigator(client, investigatorModel.Model, investigatorModel.MaxTokens)
	report, err = inv.Investigate(ctx, repoPath)
	if err != nil {
		fmt.Fprintf(out, "Warning: investigation failed: %v (continuing without report)\n", err)
	} else {
		fmt.Fprintf(out, "Investigation complete: %d modules, %d smells, %d risk areas\n",
			len(report.Modules), len(report.CodeSmells), len(report.RiskAreas))
	}
}

// Build context
reqCtx := engine.BuildRequirementContext(repoProfile, classification, report)

// Emit classification event
classPayload := map[string]any{
	"req_id": reqID, "req_type": classification.Type,
	"is_existing": repoProfile.IsExisting, "confidence": classification.Confidence,
}
classEvt := state.NewEvent(state.EventReqClassified, "", "", classPayload)
s.Events.Append(classEvt)
s.Proj.Project(classEvt)

// Emit investigation event if report exists
if report != nil {
	reportJSON, _ := json.Marshal(report)
	invEvt := state.NewEvent(state.EventInvestigationCompleted, "", "", map[string]any{
		"req_id": reqID, "report": string(reportJSON),
	})
	s.Events.Append(invEvt)
	s.Proj.Project(invEvt)
}

// Stage 4: Plan with context
result, err := planner.PlanWithContext(ctx, reqID, requirement, repoPath, reqCtx)
```

Add `"encoding/json"` to imports if not present.

- [ ] **Step 2: Add PlanWithContext to planner.go**

Add a new method that wraps `Plan()` with context injection:

```go
// PlanWithContext plans a requirement with classification and investigation context.
func (p *Planner) PlanWithContext(ctx context.Context, reqID, requirement, repoPath string, reqCtx RequirementContext) (PlanResult, error) {
	// Store context for prompt injection
	p.reqCtx = &reqCtx
	return p.Plan(ctx, reqID, requirement, repoPath)
}
```

Add `reqCtx *RequirementContext` field to the `Planner` struct.

In `Plan()`, when building the PromptContext (around line 77-80), add the flags:

```go
promptCtx := agent.PromptContext{
	RepoPath:  repoPath,
	TechStack: fmt.Sprintf("%s (%s)", stack.Language, stack.BuildTool),
}
if p.reqCtx != nil {
	promptCtx.IsExistingCodebase = p.reqCtx.IsExisting
	promptCtx.IsBugFix = p.reqCtx.IsBugFix
	promptCtx.IsRefactor = p.reqCtx.IsRefactor
	promptCtx.IsInfrastructure = p.reqCtx.IsInfra
	if p.reqCtx.Report != nil {
		reportJSON, _ := json.Marshal(p.reqCtx.Report)
		promptCtx.InvestigationReport = fmt.Sprintf("## Codebase Investigation Report\n\n```json\n%s\n```", string(reportJSON))
	}
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/nxd/`
Expected: Success

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/req.go internal/engine/planner.go
git commit -m "feat: integrate classification and investigation into nxd req pipeline"
```

---

### Task 8: Final Verification

- [ ] **Step 1: Run full test suite with race detection**

Run: `go test ./... -race -count=1`
Expected: PASS

- [ ] **Step 2: Build binary**

Run: `go build -o /tmp/nxd ./cmd/nxd/`
Expected: Success

- [ ] **Step 3: Verify CLI commands**

Run:
```bash
cd /tmp && rm -f nxd.yaml && /tmp/nxd init && /tmp/nxd config validate
/tmp/nxd config show | grep investigator
```
Expected: Config validates, shows investigator model config

- [ ] **Step 4: Verify against NXD's own repo (existing codebase)**

Run:
```bash
cd /Users/mncedimini/Sites/misc/nexus-dispatch
/tmp/nxd config validate
```
Expected: Validates with new defaults including investigator

- [ ] **Step 5: Commit any fixes**

```bash
git status
```
