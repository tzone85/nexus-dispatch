package engine_test

import (
	"fmt"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// mockGitHubOps is a test double for the GitHubOps interface.
type mockGitHubOps struct {
	pushErr    error
	createPR   engine.PRCreationResult
	createErr  error
	mergeErr   error
	pushCalls  int
	mergeCalls int
}

func (m *mockGitHubOps) PushBranch(_, _ string) error {
	m.pushCalls++
	return m.pushErr
}

func (m *mockGitHubOps) CreatePR(_, _, _, _ string) (engine.PRCreationResult, error) {
	return m.createPR, m.createErr
}

func (m *mockGitHubOps) MergePR(_ string, _ int) error {
	m.mergeCalls++
	return m.mergeErr
}

func TestMerger_Merge_WithAutoMerge(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	ghOps := &mockGitHubOps{
		createPR: engine.PRCreationResult{Number: 42, URL: "https://github.com/org/repo/pull/42"},
	}

	cfg := config.MergeConfig{AutoMerge: true, BaseBranch: "main"}
	merger := engine.NewMerger(cfg, ghOps, es, ps)

	result, err := merger.Merge("s-001", "Add user model", "/tmp/repo", "vxd/s-001")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if result.PRNumber != 42 {
		t.Fatalf("expected PR #42, got #%d", result.PRNumber)
	}
	if !result.Merged {
		t.Fatal("expected auto-merge")
	}
	if ghOps.pushCalls != 1 {
		t.Fatalf("expected 1 push call, got %d", ghOps.pushCalls)
	}
	if ghOps.mergeCalls != 1 {
		t.Fatalf("expected 1 merge call, got %d", ghOps.mergeCalls)
	}

	// Verify events
	prEvents, err := es.List(state.EventFilter{Type: state.EventStoryPRCreated})
	if err != nil {
		t.Fatalf("list pr events: %v", err)
	}
	if len(prEvents) != 1 {
		t.Fatalf("expected 1 PR_CREATED event, got %d", len(prEvents))
	}

	mergeEvents, err := es.List(state.EventFilter{Type: state.EventStoryMerged})
	if err != nil {
		t.Fatalf("list merge events: %v", err)
	}
	if len(mergeEvents) != 1 {
		t.Fatalf("expected 1 STORY_MERGED event, got %d", len(mergeEvents))
	}

	// Verify projection
	story, err := ps.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "merged" {
		t.Fatalf("expected story status 'merged', got %s", story.Status)
	}
}

func TestMerger_Merge_WithoutAutoMerge(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	ghOps := &mockGitHubOps{
		createPR: engine.PRCreationResult{Number: 10, URL: "https://github.com/org/repo/pull/10"},
	}

	cfg := config.MergeConfig{AutoMerge: false, BaseBranch: "main"}
	merger := engine.NewMerger(cfg, ghOps, es, ps)

	result, err := merger.Merge("s-001", "Task", "/tmp/repo", "vxd/s-001")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if result.Merged {
		t.Fatal("expected no auto-merge when disabled")
	}
	if ghOps.mergeCalls != 0 {
		t.Fatalf("expected 0 merge calls, got %d", ghOps.mergeCalls)
	}

	// PR created event should exist, merged should not
	prEvents, err := es.List(state.EventFilter{Type: state.EventStoryPRCreated})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(prEvents) != 1 {
		t.Fatalf("expected 1 PR_CREATED event, got %d", len(prEvents))
	}

	mergeEvents, err := es.List(state.EventFilter{Type: state.EventStoryMerged})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(mergeEvents) != 0 {
		t.Fatalf("expected 0 STORY_MERGED events, got %d", len(mergeEvents))
	}
}

func TestMerger_Merge_PushError(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ghOps := &mockGitHubOps{
		pushErr: fmt.Errorf("auth failed"),
	}

	cfg := config.MergeConfig{AutoMerge: true, BaseBranch: "main"}
	merger := engine.NewMerger(cfg, ghOps, es, ps)

	_, err := merger.Merge("s-001", "Task", "/tmp/repo", "vxd/s-001")
	if err == nil {
		t.Fatal("expected push error")
	}
}

func TestMerger_Merge_CreatePRError(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ghOps := &mockGitHubOps{
		createErr: fmt.Errorf("gh not authenticated"),
	}

	cfg := config.MergeConfig{AutoMerge: true, BaseBranch: "main"}
	merger := engine.NewMerger(cfg, ghOps, es, ps)

	_, err := merger.Merge("s-001", "Task", "/tmp/repo", "vxd/s-001")
	if err == nil {
		t.Fatal("expected create PR error")
	}
}
