package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"

	_ "github.com/mattn/go-sqlite3"
)

// --- matchesSequentialPattern ---

func TestMatchesSequentialPattern(t *testing.T) {
	d := &Dispatcher{
		config: config.Config{
			Planning: config.PlanningConfig{
				SequentialFilePatterns: []string{"go.mod", "package.json", "*.lock"},
			},
		},
	}

	tests := []struct {
		file string
		want bool
	}{
		{"go.mod", true},
		{"package.json", true},
		{"src/package.json", true},
		{"yarn.lock", true},
		{"src/go.mod", true},
		{"main.go", false},
		{"README.md", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := d.matchesSequentialPattern(tt.file); got != tt.want {
			t.Errorf("matchesSequentialPattern(%q) = %v, want %v", tt.file, got, tt.want)
		}
	}
}

func TestMatchesSequentialPattern_Empty(t *testing.T) {
	d := &Dispatcher{config: config.Config{}}
	if d.matchesSequentialPattern("go.mod") {
		t.Error("empty patterns should match nothing")
	}
}

// --- runtimeForRole ---

func TestRuntimeForRole(t *testing.T) {
	e := &Executor{
		config: config.Config{
			Models: config.ModelsConfig{
				Junior: config.ModelConfig{Provider: "ollama", Model: "qwen2.5-coder:7b"},
				Senior: config.ModelConfig{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			},
			Runtimes: map[string]config.RuntimeConfig{
				"aider":       {},
				"claude-code": {},
			},
		},
	}

	// Ollama provider should map to aider
	got := e.runtimeForRole(agent.RoleJunior)
	if got != "aider" {
		t.Errorf("Junior (ollama) runtime = %q, want aider", got)
	}

	// Anthropic should map to claude-code
	got = e.runtimeForRole(agent.RoleSenior)
	if got != "claude-code" {
		t.Errorf("Senior (anthropic) runtime = %q, want claude-code", got)
	}
}

func TestRuntimeForRole_NativePreferred(t *testing.T) {
	e := &Executor{
		config: config.Config{
			Models: config.ModelsConfig{
				Junior: config.ModelConfig{Provider: "ollama", Model: "qwen2.5-coder:7b"},
			},
			Runtimes: map[string]config.RuntimeConfig{
				"native-ollama": {Native: true, Models: []string{"qwen2.5-coder"}},
				"aider":         {},
			},
		},
	}

	got := e.runtimeForRole(agent.RoleJunior)
	if got != "native-ollama" {
		t.Errorf("expected native-ollama for matching native model, got %q", got)
	}
}

func TestRuntimeForRole_Fallback(t *testing.T) {
	e := &Executor{
		config: config.Config{
			Models: config.ModelsConfig{
				Junior: config.ModelConfig{Provider: "unknown", Model: "mystery-model"},
			},
			Runtimes: map[string]config.RuntimeConfig{
				"default-rt": {},
			},
		},
	}

	got := e.runtimeForRole(agent.RoleJunior)
	if got != "default-rt" {
		t.Errorf("expected fallback runtime, got %q", got)
	}
}

// --- latestReviewFeedback ---

func TestLatestReviewFeedback_NoEvents(t *testing.T) {
	dir := t.TempDir()
	es, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer es.Close()

	e := &Executor{eventStore: es}
	got := e.latestReviewFeedback("s-001")
	if got != "" {
		t.Errorf("expected empty feedback for no events, got %q", got)
	}
}

func TestLatestReviewFeedback_WithFeedback(t *testing.T) {
	dir := t.TempDir()
	es, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer es.Close()

	feedback := "Missing error handling in ProcessOrder function"
	evt := state.NewEvent(state.EventStoryReviewFailed, "monitor", "s-001", map[string]any{
		"feedback": feedback,
	})
	es.Append(evt)

	e := &Executor{eventStore: es}
	got := e.latestReviewFeedback("s-001")
	if got != feedback {
		t.Errorf("feedback = %q, want %q", got, feedback)
	}
}

// --- marshalReviewComments ---

func TestMarshalReviewComments_Empty(t *testing.T) {
	got := marshalReviewComments(nil)
	if got != "[]" {
		t.Errorf("expected '[]' for nil, got %q", got)
	}
	got = marshalReviewComments([]ReviewComment{})
	if got != "[]" {
		t.Errorf("expected '[]' for empty slice, got %q", got)
	}
}

func TestMarshalReviewComments_WithComments(t *testing.T) {
	comments := []ReviewComment{
		{File: "main.go", Line: 10, Severity: "critical", Comment: "missing nil check"},
	}
	got := marshalReviewComments(comments)
	var parsed []ReviewComment
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Severity != "critical" {
		t.Errorf("unexpected parsed comments: %+v", parsed)
	}
}

// --- isRequirementPaused ---

func TestIsRequirementPaused_UnknownStory(t *testing.T) {
	dir := t.TempDir()
	es, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	ps, _ := state.NewSQLiteStore(filepath.Join(dir, "proj.db"))
	defer es.Close()
	defer ps.Close()

	m := &Monitor{eventStore: es, projStore: ps}
	// Unknown story should return false (not panic)
	if m.isRequirementPaused("nonexistent") {
		t.Error("unknown story should return false")
	}
}

// --- isGitignoreOnlyDiff ---

func TestIsGitignoreOnlyDiff(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}

	// Initial commit
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Capture the merge base hash
	hashCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	hashOut, _ := hashCmd.Output()
	mergeBase := strings.TrimSpace(string(hashOut))

	// Add only .gitignore
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "gitignore").Run()

	if !isGitignoreOnlyDiff(dir, mergeBase) {
		t.Error("expected gitignore-only diff to return true")
	}

	// Add another file
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "add new.go").Run()

	if isGitignoreOnlyDiff(dir, mergeBase) {
		t.Error("expected mixed diff to return false")
	}

	// Non-git directory should return false
	if isGitignoreOnlyDiff(t.TempDir(), "HEAD") {
		t.Error("non-git dir should return false")
	}
}

func TestDryRunSimulationProducesReviewableDiff(t *testing.T) {
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()
	exec.Command("git", "-C", dir, "switch", "-c", "nxd/story-1").Run()

	simulateDryRunChanges(dir, "story-1")
	diff, err := gitDiff(dir)
	if err != nil {
		t.Fatalf("gitDiff: %v", err)
	}
	if !strings.Contains(diff, "dry-run-simulation.txt") {
		t.Fatalf("expected dry-run simulation file in diff, got:\n%s", diff)
	}
}

// --- mapEscalationActionToManagerAction ---

func TestMapEscalationActionToManagerAction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"retry", "retry"},
		{"split_story", "split"},
		{"reassign_higher_tier", "escalate_to_techlead"},
		{"mark_blocked", "escalate_to_techlead"},
		{"abandon", "escalate_to_techlead"},
		{"unknown_action", "escalate_to_techlead"},
	}
	for _, tt := range tests {
		got := mapEscalationActionToManagerAction(tt.input)
		if got != tt.want {
			t.Errorf("mapEscalationAction(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- convertToolResultToManagerAction ---

func TestConvertToolResultToManagerAction_Retry(t *testing.T) {
	tr := ManagerToolResult{
		Decision: &EscalationDecision{
			Action:     "retry",
			Reason:     "transient network error",
			AssignedTo: "junior",
		},
	}
	ma := convertToolResultToManagerAction(tr)
	if ma.Action != "retry" {
		t.Errorf("action = %q, want retry", ma.Action)
	}
	if ma.RetryConfig == nil {
		t.Fatal("expected RetryConfig for retry action")
	}
	if ma.RetryConfig.TargetRole != "junior" {
		t.Errorf("TargetRole = %q, want junior", ma.RetryConfig.TargetRole)
	}
}

func TestConvertToolResultToManagerAction_Split(t *testing.T) {
	tr := ManagerToolResult{
		Split: &StorySplit{
			OriginalStoryID: "s-001",
			NewStories: []ManagerSplitChild{
				{Title: "Part A", Description: "First half"},
				{Title: "Part B", Description: "Second half"},
			},
		},
	}
	ma := convertToolResultToManagerAction(tr)
	if ma.Action != "split" {
		t.Errorf("action = %q, want split", ma.Action)
	}
	if ma.SplitConfig == nil {
		t.Fatal("expected SplitConfig")
	}
	if len(ma.SplitConfig.Children) != 2 {
		t.Errorf("expected 2 children, got %d", len(ma.SplitConfig.Children))
	}
}

func TestConvertToolResultToManagerAction_Empty(t *testing.T) {
	ma := convertToolResultToManagerAction(ManagerToolResult{})
	if ma.Action != "" {
		t.Errorf("expected empty action for empty result, got %q", ma.Action)
	}
}

// --- Executor.SetProjectDir ---

func TestExecutor_SetProjectDir(t *testing.T) {
	e := &Executor{}
	e.SetProjectDir("/tmp/test-project")
	if e.projectDir != "/tmp/test-project" {
		t.Errorf("projectDir = %q, want /tmp/test-project", e.projectDir)
	}
}

// --- Monitor setters ---

func TestMonitor_Setters(t *testing.T) {
	m := &Monitor{}

	// All setters should not panic
	m.SetArtifactStore(nil)
	m.SetConflictResolver(nil)
	m.SetPlanner(nil)
	m.SetCodeGraph(nil)
	m.SetMemPalace(nil)

	// Setters should accept values without panicking
}
