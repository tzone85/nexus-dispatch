package engine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// mockRunner is a test double for CommandRunner that returns pre-configured results.
type mockRunner struct {
	results map[string]mockRunResult // command name -> result
}

type mockRunResult struct {
	output string
	err    error
}

func (m *mockRunner) Run(_ context.Context, _, name string, args ...string) (string, error) {
	// Look up by first arg or by command name
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	if r, ok := m.results[key]; ok {
		return r.output, r.err
	}
	if r, ok := m.results[name]; ok {
		return r.output, r.err
	}
	return "", fmt.Errorf("unexpected command: %s", name)
}

func TestQA_Run_AllPass(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate story
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	runner := &mockRunner{results: map[string]mockRunResult{
		"golangci-lint": {output: "All checks passed", err: nil},
		"go":            {output: "Build succeeded", err: nil},
	}}

	qa := engine.NewQA(engine.QAConfig{
		LintCommand:  "golangci-lint run",
		BuildCommand: "go build ./...",
		TestCommand:  "go test ./...",
	}, runner, es, ps)

	result, err := qa.Run(context.Background(), "s-001", "/tmp/worktree")
	if err != nil {
		t.Fatalf("qa run: %v", err)
	}
	if !result.Passed {
		t.Fatal("expected QA to pass")
	}
	if len(result.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(result.Checks))
	}

	// Verify QA_PASSED event
	events, err := es.List(state.EventFilter{Type: state.EventStoryQAPassed})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 STORY_QA_PASSED event, got %d", len(events))
	}
}

func TestQA_Run_LintFails(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	runner := &mockRunner{results: map[string]mockRunResult{
		"golangci-lint": {output: "Error: unused variable", err: fmt.Errorf("exit status 1")},
		"go":            {output: "ok", err: nil},
	}}

	qa := engine.NewQA(engine.QAConfig{
		LintCommand:  "golangci-lint run",
		BuildCommand: "go build ./...",
		TestCommand:  "go test ./...",
	}, runner, es, ps)

	result, err := qa.Run(context.Background(), "s-001", "/tmp/worktree")
	if err != nil {
		t.Fatalf("qa run: %v", err)
	}
	if result.Passed {
		t.Fatal("expected QA to fail when lint fails")
	}

	// Find lint check
	var lintCheck *engine.QACheckResult
	for i := range result.Checks {
		if result.Checks[i].Name == "lint" {
			lintCheck = &result.Checks[i]
			break
		}
	}
	if lintCheck == nil {
		t.Fatal("lint check not found in results")
	}
	if lintCheck.Passed {
		t.Fatal("lint check should have failed")
	}

	// Verify STORY_QA_FAILED event (QA failures emit QA-specific event so the
	// projection and escalation engine can distinguish them from review failures)
	events, err := es.List(state.EventFilter{Type: state.EventStoryQAFailed})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 STORY_QA_FAILED event, got %d", len(events))
	}
}

func TestQA_Run_SkipsEmptyCommands(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	runner := &mockRunner{results: map[string]mockRunResult{
		"go": {output: "ok", err: nil},
	}}

	// Only build command, lint and test empty
	qa := engine.NewQA(engine.QAConfig{
		BuildCommand: "go build ./...",
	}, runner, es, ps)

	result, err := qa.Run(context.Background(), "s-001", "/tmp/worktree")
	if err != nil {
		t.Fatalf("qa run: %v", err)
	}
	if !result.Passed {
		t.Fatal("expected QA to pass with only build")
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check (skipping empty), got %d", len(result.Checks))
	}
}

func TestQA_Run_ProjectionUpdated(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	runner := &mockRunner{results: map[string]mockRunResult{
		"go": {output: "ok", err: nil},
	}}

	qa := engine.NewQA(engine.QAConfig{
		BuildCommand: "go build",
	}, runner, es, ps)

	_, err := qa.Run(context.Background(), "s-001", "/tmp/worktree")
	if err != nil {
		t.Fatalf("qa run: %v", err)
	}

	// Verify story status updated
	story, err := ps.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "pr_submitted" {
		t.Fatalf("expected story status 'pr_submitted' after QA pass, got %s", story.Status)
	}
}
