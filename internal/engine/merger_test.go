package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/git"
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

	result, err := merger.Merge("s-001", "Add user model", "/tmp/repo", "nxd/s-001")
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

	result, err := merger.Merge("s-001", "Task", "/tmp/repo", "nxd/s-001")
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

	_, err := merger.Merge("s-001", "Task", "/tmp/repo", "nxd/s-001")
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

	_, err := merger.Merge("s-001", "Task", "/tmp/repo", "nxd/s-001")
	if err == nil {
		t.Fatal("expected create PR error")
	}
}

// --- Local merge mode tests ---

// mockLocalMergeOps is a test double for the LocalMergeOps interface.
type mockLocalMergeOps struct {
	mergeResult git.MergeResult
	mergeErr    error
	canMerge    bool
	conflicts   []string
	canMergeErr error
	mergeCalls  int
}

func (m *mockLocalMergeOps) Merge(featureBranch, baseBranch string) (git.MergeResult, error) {
	m.mergeCalls++
	return m.mergeResult, m.mergeErr
}

func (m *mockLocalMergeOps) CanMerge(featureBranch, baseBranch string) (bool, []string, error) {
	return m.canMerge, m.conflicts, m.canMergeErr
}

func TestMerger_LocalMerge_Success(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-010", map[string]any{
		"id": "s-010", "req_id": "r-001", "title": "Local task", "description": "desc", "complexity": 2,
	}))

	localOps := &mockLocalMergeOps{
		mergeResult: git.MergeResult{
			Branch:     "nxd/s-010",
			BaseBranch: "main",
			MergedSHA:  "abc123def456abc123def456abc123def456abc1",
		},
	}

	cfg := config.MergeConfig{AutoMerge: true, BaseBranch: "main"}
	merger := engine.NewLocalMerger(cfg, localOps, es, ps)

	result, err := merger.Merge("s-010", "Local task", "/tmp/repo", "nxd/s-010")
	if err != nil {
		t.Fatalf("local merge: %v", err)
	}

	if !result.Merged {
		t.Fatal("expected merged=true for local merge")
	}
	if result.PRURL != "local://merged" {
		t.Fatalf("expected PR URL 'local://merged', got %q", result.PRURL)
	}
	if result.PRNumber != 0 {
		t.Fatalf("expected PR number 0 for local merge, got %d", result.PRNumber)
	}
	if localOps.mergeCalls != 1 {
		t.Fatalf("expected 1 local merge call, got %d", localOps.mergeCalls)
	}

	// Verify STORY_PR_CREATED event was emitted
	prEvents, err := es.List(state.EventFilter{Type: state.EventStoryPRCreated})
	if err != nil {
		t.Fatalf("list pr events: %v", err)
	}
	if len(prEvents) != 1 {
		t.Fatalf("expected 1 PR_CREATED event, got %d", len(prEvents))
	}

	// Verify STORY_MERGED event was emitted
	mergeEvents, err := es.List(state.EventFilter{Type: state.EventStoryMerged})
	if err != nil {
		t.Fatalf("list merge events: %v", err)
	}
	if len(mergeEvents) != 1 {
		t.Fatalf("expected 1 STORY_MERGED event, got %d", len(mergeEvents))
	}

	// Verify projection updated
	story, err := ps.GetStory("s-010")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "merged" {
		t.Fatalf("expected story status 'merged', got %s", story.Status)
	}
}

func TestMerger_LocalMerge_WithConflicts(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	localOps := &mockLocalMergeOps{
		mergeErr: fmt.Errorf("merge conflicts in 1 file(s): README"),
		mergeResult: git.MergeResult{
			Conflicts: []string{"README"},
		},
	}

	cfg := config.MergeConfig{AutoMerge: true, BaseBranch: "main"}
	merger := engine.NewLocalMerger(cfg, localOps, es, ps)

	_, err := merger.Merge("s-011", "Conflict task", "/tmp/repo", "nxd/s-011")
	if err == nil {
		t.Fatal("expected error from conflicting local merge")
	}
	if !strings.Contains(err.Error(), "local merge") {
		t.Fatalf("expected error to mention 'local merge', got: %v", err)
	}

	// No events should have been emitted
	prEvents, err := es.List(state.EventFilter{Type: state.EventStoryPRCreated})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(prEvents) != 0 {
		t.Fatalf("expected 0 PR_CREATED events after conflict, got %d", len(prEvents))
	}
}
