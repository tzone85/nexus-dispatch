// wiring_test.go — Integration wiring tests.
//
// RULE: Every new feature that modifies agent behavior MUST have a
// wiring test here that proves it activates under real conditions.
// Unit tests verify components work. Wiring tests verify components
// are connected.
package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// plannerResponseJSON is a valid 3-story planner response used by tests
// that need the Planner.PlanWithContext call to succeed.
const plannerResponseJSON = `[
	{"id": "s-001", "title": "Setup project scaffold", "description": "Create directory structure", "acceptance_criteria": "Scaffold exists", "complexity": 2, "depends_on": [], "owned_files": ["src/main.go"], "wave_hint": "sequential"},
	{"id": "s-002", "title": "Add core logic", "description": "Implement business rules", "acceptance_criteria": "Logic works", "complexity": 3, "depends_on": ["s-001"], "owned_files": ["src/core.go"], "wave_hint": "parallel"},
	{"id": "s-003", "title": "Add tests", "description": "Write test suite", "acceptance_criteria": "Tests pass", "complexity": 2, "depends_on": ["s-001"], "owned_files": ["src/core_test.go"], "wave_hint": "parallel"}
]`

// newTestStores creates an in-memory event store and projection store
// suitable for wiring tests. Both are cleaned up via t.Cleanup.
func newTestStores(t *testing.T) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	dir := t.TempDir()

	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	t.Cleanup(func() { es.Close() })

	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create projection store: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return es, ps
}

// initTestRepo creates a git repo in dir with the given number of commits.
// Each commit creates a unique file so the commit count is verifiable.
func initTestRepo(t *testing.T, dir string, commits int) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	for i := 0; i < commits; i++ {
		name := fmt.Sprintf("commit_%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("content %d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", fmt.Sprintf("commit %d", i))
	}
}

// --- Test 1: TechLeadGetsArchaeology ---

func TestWiring_TechLeadGetsArchaeology(t *testing.T) {
	// Setup: temp dir with go.mod so ScanRepo detects a Go project
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// ReplayClient returns a valid planner response
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: plannerResponseJSON,
		Model:   "test-model",
	})

	es, ps := newTestStores(t)
	cfg := config.DefaultConfig()
	planner := NewPlanner(client, cfg, es, ps)

	// Build a requirement context for an existing codebase
	reqCtx := RequirementContext{
		IsExisting: true,
		IsBugFix:   false,
		IsRefactor: false,
		IsInfra:    false,
	}

	_, err := planner.PlanWithContext(context.Background(), "r-wiring-001", "Refactor auth module", dir, reqCtx)
	if err != nil {
		t.Fatalf("PlanWithContext failed: %v", err)
	}

	// Inspect the captured LLM request's System field
	if client.CallCount() < 1 {
		t.Fatal("expected at least 1 LLM call")
	}
	req := client.CallAt(0)

	// The system prompt must contain CodebaseArchaeology markers
	if !strings.Contains(req.System, "BEFORE PLANNING") {
		t.Error("expected system prompt to contain 'BEFORE PLANNING' from CodebaseArchaeology")
	}
	if !strings.Contains(req.System, "Investigation Report") {
		t.Error("expected system prompt to contain 'Investigation Report' from CodebaseArchaeology")
	}
	if !strings.Contains(req.System, "ORIENTATION PROCEDURE") {
		t.Error("expected system prompt to contain 'ORIENTATION PROCEDURE' from CodebaseArchaeology")
	}
}

// --- Test 2: SeniorGetsBugHunting ---

func TestWiring_SeniorGetsBugHunting(t *testing.T) {
	ctx := agent.PromptContext{
		IsBugFix:           true,
		IsExistingCodebase: false,
	}

	prompt := agent.SystemPrompt(agent.RoleSenior, ctx)

	if !strings.Contains(prompt, "REPRODUCE") {
		t.Error("expected Senior bug-fix prompt to contain 'REPRODUCE' from BugHuntingMethodology")
	}
	if !strings.Contains(prompt, "ISOLATE") {
		t.Error("expected Senior bug-fix prompt to contain 'ISOLATE' from BugHuntingMethodology")
	}
	if !strings.Contains(prompt, "BUG HUNTING METHODOLOGY") {
		t.Error("expected Senior bug-fix prompt to contain 'BUG HUNTING METHODOLOGY' header")
	}
}

// --- Test 3: InfraGetsDebugging ---

func TestWiring_InfraGetsDebugging(t *testing.T) {
	roles := []agent.Role{
		agent.RoleSenior,
		agent.RoleIntermediate,
		agent.RoleJunior,
	}

	ctx := agent.PromptContext{
		IsInfrastructure: true,
	}

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			prompt := agent.SystemPrompt(role, ctx)

			// InfrastructureDebugging contains "DOCKER" and "Docker" sections
			hasDocker := strings.Contains(prompt, "Docker") || strings.Contains(prompt, "docker")
			if !hasDocker {
				t.Errorf("expected %s infra prompt to contain Docker references from InfrastructureDebugging", role)
			}
			if !strings.Contains(prompt, "INFRASTRUCTURE DEBUGGING TOOLKIT") {
				t.Errorf("expected %s infra prompt to contain 'INFRASTRUCTURE DEBUGGING TOOLKIT' header", role)
			}
		})
	}
}

// --- Test 4: JuniorGetsLegacySurvival ---

func TestWiring_JuniorGetsLegacySurvival(t *testing.T) {
	ctx := agent.PromptContext{
		IsExistingCodebase: true,
	}

	prompt := agent.SystemPrompt(agent.RoleJunior, ctx)

	if !strings.Contains(prompt, "NEVER rewrite") {
		t.Error("expected Junior existing-codebase prompt to contain 'NEVER rewrite' from LegacyCodeSurvival")
	}
	if !strings.Contains(prompt, "characterization test") {
		t.Error("expected Junior existing-codebase prompt to contain 'characterization test' from LegacyCodeSurvival")
	}
	if !strings.Contains(prompt, "LEGACY CODE SURVIVAL GUIDE") {
		t.Error("expected Junior existing-codebase prompt to contain 'LEGACY CODE SURVIVAL GUIDE' header")
	}
}

// --- Test 5: GreenfieldNoPlaybooks ---

func TestWiring_GreenfieldNoPlaybooks(t *testing.T) {
	// All flags false: greenfield scenario
	ctx := agent.PromptContext{
		IsExistingCodebase: false,
		IsBugFix:           false,
		IsRefactor:         false,
		IsInfrastructure:   false,
	}

	// All roles that have prompt templates
	roles := []agent.Role{
		agent.RoleTechLead,
		agent.RoleSenior,
		agent.RoleIntermediate,
		agent.RoleJunior,
	}

	// Markers from each playbook that should NOT appear
	playBookMarkers := []string{
		"REPRODUCE",                       // BugHuntingMethodology
		"NEVER rewrite",                   // LegacyCodeSurvival
		"INFRASTRUCTURE DEBUGGING TOOLKIT", // InfrastructureDebugging
		"Investigation Report",            // CodebaseArchaeology
		"BEFORE PLANNING",                 // CodebaseArchaeology
		"BUG HUNTING METHODOLOGY",         // BugHuntingMethodology
		"LEGACY CODE SURVIVAL GUIDE",      // LegacyCodeSurvival
	}

	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			prompt := agent.SystemPrompt(role, ctx)
			for _, marker := range playBookMarkers {
				if strings.Contains(prompt, marker) {
					t.Errorf("greenfield %s prompt should NOT contain playbook marker %q", role, marker)
				}
			}
		})
	}
}

// --- Test 6: GoalPromptInstructions ---

func TestWiring_GoalPromptInstructions(t *testing.T) {
	ctx := agent.PromptContext{
		IsBugFix:           true,
		IsExistingCodebase: true,
		StoryID:            "s-test",
		StoryTitle:         "Fix login bug",
		StoryDescription:   "Login returns 500",
		AcceptanceCriteria: "Login works",
	}

	goal := agent.GoalPrompt(agent.RoleSenior, ctx)

	// Must contain the REPRODUCE workflow (from IsBugFix)
	if !strings.Contains(goal, "REPRODUCE") {
		t.Error("expected GoalPrompt with IsBugFix to contain 'REPRODUCE'")
	}
	if !strings.Contains(goal, "MANDATORY BUG FIX WORKFLOW") {
		t.Error("expected GoalPrompt with IsBugFix to contain 'MANDATORY BUG FIX WORKFLOW'")
	}

	// Must contain the ORIENT workflow (from IsExistingCodebase)
	if !strings.Contains(goal, "ORIENT") {
		t.Error("expected GoalPrompt with IsExistingCodebase to contain 'ORIENT'")
	}
	if !strings.Contains(goal, "MANDATORY WORKFLOW FOR EXISTING CODEBASE") {
		t.Error("expected GoalPrompt with IsExistingCodebase to contain 'MANDATORY WORKFLOW FOR EXISTING CODEBASE'")
	}
}

// --- Test 7: DetectorsProduceFlags ---

func TestWiring_DetectorsProduceFlags(t *testing.T) {
	// Create a real temp repo with >5 source files and >10 commits
	dir := t.TempDir()
	initTestRepo(t, dir, 15)

	// Add go.mod for language detection
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create >5 source files
	srcDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("module%d.go", i))
		if err := os.WriteFile(name, []byte("package pkg"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// ClassifyRepo should detect this as an existing project
	profile := ClassifyRepo(dir)
	if !profile.IsExisting {
		t.Fatalf("expected IsExisting=true for repo with %d source files and %d commits",
			profile.SourceFileCount, profile.CommitCount)
	}

	// Build RequirementContext from profile
	reqCtx := NewRequirementContext(profile, RequirementClassification{
		Type:       "bugfix",
		Confidence: 0.9,
		Signals:    []string{"error in production"},
	})

	if !reqCtx.IsExisting {
		t.Fatal("expected RequirementContext.IsExisting=true")
	}
	if !reqCtx.IsBugFix {
		t.Fatal("expected RequirementContext.IsBugFix=true")
	}

	// Build PromptContext from RequirementContext and call SystemPrompt
	promptCtx := agent.PromptContext{
		IsExistingCodebase: reqCtx.IsExisting,
		IsBugFix:           reqCtx.IsBugFix,
		IsRefactor:         reqCtx.IsRefactor,
		IsInfrastructure:   reqCtx.IsInfra,
	}

	// Senior gets BugHuntingMethodology + LegacyCodeSurvival for existing+bugfix
	seniorPrompt := agent.SystemPrompt(agent.RoleSenior, promptCtx)
	if !strings.Contains(seniorPrompt, "BUG HUNTING METHODOLOGY") {
		t.Error("expected Senior prompt for existing bugfix to contain BugHuntingMethodology")
	}
	if !strings.Contains(seniorPrompt, "LEGACY CODE SURVIVAL GUIDE") {
		t.Error("expected Senior prompt for existing bugfix to contain LegacyCodeSurvival")
	}

	// TechLead gets CodebaseArchaeology for existing codebase
	techLeadPrompt := agent.SystemPrompt(agent.RoleTechLead, promptCtx)
	if !strings.Contains(techLeadPrompt, "BEFORE PLANNING") {
		t.Error("expected TechLead prompt for existing codebase to contain CodebaseArchaeology")
	}
}

// --- Test 8: PlannerArchaeology (full chain) ---

func TestWiring_PlannerArchaeology(t *testing.T) {
	// Create a real temp repo that qualifies as "existing"
	dir := t.TempDir()
	initTestRepo(t, dir, 15)

	// Add go.mod for language detection
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create >5 source files
	srcDir := filepath.Join(dir, "internal")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("svc%d.go", i))
		if err := os.WriteFile(name, []byte("package internal"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Step 1: ClassifyRepo
	profile := ClassifyRepo(dir)
	if !profile.IsExisting {
		t.Fatalf("expected IsExisting=true, got source=%d commits=%d",
			profile.SourceFileCount, profile.CommitCount)
	}

	// Step 2: Build RequirementContext with Type:"bugfix"
	reqCtx := NewRequirementContext(profile, RequirementClassification{
		Type:       "bugfix",
		Confidence: 0.85,
		Signals:    []string{"crash in prod"},
	})

	// Step 3: Create ReplayClient + Planner
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: plannerResponseJSON,
		Model:   "test-model",
	})
	es, ps := newTestStores(t)
	cfg := config.DefaultConfig()
	planner := NewPlanner(client, cfg, es, ps)

	// Step 4: PlanWithContext
	_, err := planner.PlanWithContext(context.Background(), "r-wiring-008", "Fix crash on login", dir, reqCtx)
	if err != nil {
		t.Fatalf("PlanWithContext failed: %v", err)
	}

	// Step 5: Inspect the captured LLM request
	if client.CallCount() < 1 {
		t.Fatal("expected at least 1 LLM call")
	}
	req := client.CallAt(0)

	// System prompt must contain CodebaseArchaeology (TechLead + existing codebase)
	if !strings.Contains(req.System, "BEFORE PLANNING") {
		t.Error("expected system prompt to contain CodebaseArchaeology ('BEFORE PLANNING')")
	}
	if !strings.Contains(req.System, "ORIENTATION PROCEDURE") {
		t.Error("expected system prompt to contain CodebaseArchaeology ('ORIENTATION PROCEDURE')")
	}

	// BugHuntingMethodology should NOT be in TechLead prompt — it is for Senior role
	if strings.Contains(req.System, "BUG HUNTING METHODOLOGY") {
		t.Error("TechLead prompt should NOT contain BugHuntingMethodology (that is for Senior role)")
	}
}
