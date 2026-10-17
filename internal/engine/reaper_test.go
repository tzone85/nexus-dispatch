package engine_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// mockGitCleanupOps is a test double for GitCleanupOps.
type mockGitCleanupOps struct {
	worktreeDeleted  []string
	branchesDeleted  []string
	existingBranches map[string]bool
	deleteWTErr      error
	deleteBranchErr  error
}

func (m *mockGitCleanupOps) DeleteWorktree(_, worktreePath string) error {
	if m.deleteWTErr != nil {
		return m.deleteWTErr
	}
	m.worktreeDeleted = append(m.worktreeDeleted, worktreePath)
	return nil
}

func (m *mockGitCleanupOps) DeleteBranch(_, branch string) error {
	if m.deleteBranchErr != nil {
		return m.deleteBranchErr
	}
	m.branchesDeleted = append(m.branchesDeleted, branch)
	delete(m.existingBranches, branch)
	return nil
}

func (m *mockGitCleanupOps) BranchExists(_, branch string) bool {
	return m.existingBranches[branch]
}

func TestReaper_Reap_Immediate(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	gitOps := &mockGitCleanupOps{
		existingBranches: map[string]bool{"nxd/s-001": true},
	}

	cfg := config.CleanupConfig{
		WorktreePrune:       "immediate",
		BranchRetentionDays: 0, // delete immediately
	}

	reaper := engine.NewReaper(cfg, gitOps, es, ps)
	result, err := reaper.Reap("s-001", "/tmp/repo", "/tmp/worktree/s-001", "nxd/s-001")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if !result.WorktreePruned {
		t.Fatal("expected worktree pruned")
	}
	if !result.BranchDeleted {
		t.Fatal("expected branch deleted")
	}
	if result.Deferred {
		t.Fatal("expected not deferred in immediate mode")
	}
	if len(gitOps.worktreeDeleted) != 1 {
		t.Fatalf("expected 1 worktree deletion, got %d", len(gitOps.worktreeDeleted))
	}
	if len(gitOps.branchesDeleted) != 1 {
		t.Fatalf("expected 1 branch deletion, got %d", len(gitOps.branchesDeleted))
	}

	// Verify events
	wtEvents, err := es.List(state.EventFilter{Type: state.EventWorktreePruned})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(wtEvents) != 1 {
		t.Fatalf("expected 1 WORKTREE_PRUNED event, got %d", len(wtEvents))
	}

	brEvents, err := es.List(state.EventFilter{Type: state.EventBranchDeleted})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(brEvents) != 1 {
		t.Fatalf("expected 1 BRANCH_DELETED event, got %d", len(brEvents))
	}
}

func TestReaper_Reap_Deferred(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	gitOps := &mockGitCleanupOps{
		existingBranches: map[string]bool{"nxd/s-001": true},
	}

	cfg := config.CleanupConfig{
		WorktreePrune:       "deferred",
		BranchRetentionDays: 7, // retain branch
	}

	reaper := engine.NewReaper(cfg, gitOps, es, ps)
	result, err := reaper.Reap("s-001", "/tmp/repo", "/tmp/worktree/s-001", "nxd/s-001")
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if result.WorktreePruned {
		t.Fatal("expected worktree not pruned in deferred mode")
	}
	if result.BranchDeleted {
		t.Fatal("expected branch not deleted with retention > 0")
	}
	if !result.Deferred {
		t.Fatal("expected deferred flag set")
	}
}

func TestReaper_GarbageCollect(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	gitOps := &mockGitCleanupOps{
		existingBranches: map[string]bool{
			"nxd/s-001": true,
			"nxd/s-002": true,
			"nxd/s-003": true,
		},
	}

	cfg := config.CleanupConfig{
		WorktreePrune:       "immediate",
		BranchRetentionDays: 7,
	}

	reaper := engine.NewReaper(cfg, gitOps, es, ps)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reaper.SetNow(func() time.Time { return now })

	branches := []engine.BranchInfo{
		{Name: "nxd/s-001", StoryID: "s-001", MergedAt: now.AddDate(0, 0, -10)}, // expired
		{Name: "nxd/s-002", StoryID: "s-002", MergedAt: now.AddDate(0, 0, -3)},  // still valid
		{Name: "nxd/s-003", StoryID: "s-003", MergedAt: now.AddDate(0, 0, -8)},  // expired
	}

	deleted, err := reaper.GarbageCollect("/tmp/repo", branches)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 branches deleted, got %d", deleted)
	}
	if len(gitOps.branchesDeleted) != 2 {
		t.Fatalf("expected 2 branch deletions, got %d", len(gitOps.branchesDeleted))
	}

	// Verify GC completed event
	gcEvents, err := es.List(state.EventFilter{Type: state.EventGCCompleted})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(gcEvents) != 1 {
		t.Fatalf("expected 1 GC_COMPLETED event, got %d", len(gcEvents))
	}
}

func TestReaper_GarbageCollect_NoBranches(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	gitOps := &mockGitCleanupOps{}
	cfg := config.CleanupConfig{BranchRetentionDays: 7}

	reaper := engine.NewReaper(cfg, gitOps, es, ps)
	deleted, err := reaper.GarbageCollect("/tmp/repo", nil)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deletions, got %d", deleted)
	}
}

func TestReaper_GarbageCollect_ZeroRetention(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	gitOps := &mockGitCleanupOps{}
	cfg := config.CleanupConfig{BranchRetentionDays: 0}

	reaper := engine.NewReaper(cfg, gitOps, es, ps)
	deleted, err := reaper.GarbageCollect("/tmp/repo", []engine.BranchInfo{
		{Name: "nxd/s-001", StoryID: "s-001", MergedAt: time.Now()},
	})
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deletions with zero retention, got %d", deleted)
	}
}

// failOnceGitOps refuses to delete one named branch and deletes the rest.
type failOnceGitOps struct {
	mockGitCleanupOps
	refuse string
}

func (f *failOnceGitOps) DeleteBranch(repoDir, branch string) error {
	if branch == f.refuse {
		return errors.New("branch is checked out at /elsewhere")
	}
	return f.mockGitCleanupOps.DeleteBranch(repoDir, branch)
}

// TestGarbageCollect_ContinuesPastFailedBranch: one undeletable branch must
// not skip the later ones, and GC_COMPLETED is still emitted for the
// branches that were deleted; the failure is returned at the end. With a
// projector wired, the projection watermark stays level with the log.
func TestGarbageCollect_ContinuesPastFailedBranch(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()
	gitOps := &failOnceGitOps{
		mockGitCleanupOps: mockGitCleanupOps{existingBranches: map[string]bool{"nxd/a": true, "nxd/b": true, "nxd/c": true}},
		refuse:            "nxd/b",
	}
	reaper := engine.NewReaper(config.CleanupConfig{BranchRetentionDays: 7}, gitOps, es, ps)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	reaper.SetNow(func() time.Time { return now })
	old := now.AddDate(0, 0, -30)
	deleted, err := reaper.GarbageCollect("/tmp/repo", []engine.BranchInfo{
		{Name: "nxd/a", StoryID: "a", MergedAt: old},
		{Name: "nxd/b", StoryID: "b", MergedAt: old},
		{Name: "nxd/c", StoryID: "c", MergedAt: old},
	})
	if err == nil || !strings.Contains(err.Error(), "nxd/b") {
		t.Fatalf("expected the nxd/b failure to be returned, got %v", err)
	}
	if deleted != 2 {
		t.Fatalf("want 2 deleted (a and c), got %d", deleted)
	}
	gcEvents, _ := es.List(state.EventFilter{Type: state.EventGCCompleted})
	brEvents, _ := es.List(state.EventFilter{Type: state.EventBranchDeleted})
	if len(gcEvents) != 1 || len(brEvents) != 2 {
		t.Fatalf("want 1 GC_COMPLETED and 2 BRANCH_DELETED, got %d/%d", len(gcEvents), len(brEvents))
	}
	logged, _ := es.Count(state.EventFilter{})
	sqlite, ok := ps.(*state.SQLiteStore)
	if !ok {
		t.Fatalf("test store is %T, want *state.SQLiteStore", ps)
	}
	applied, err := sqlite.AppliedEventCount()
	if err != nil {
		t.Fatal(err)
	}
	if applied != logged {
		t.Fatalf("projection watermark %d != log %d: the reaper must project what it appends", applied, logged)
	}
}
