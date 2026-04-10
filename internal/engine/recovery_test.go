package engine_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newRecoveryStores creates an event store and a concrete SQLiteStore for
// recovery tests (RunRecovery requires *state.SQLiteStore, not the interface).
func newRecoveryStores(t *testing.T) (state.EventStore, *state.SQLiteStore, func()) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create projection store: %v", err)
	}
	cleanup := func() {
		es.Close()
		ps.Close()
	}
	return es, ps, cleanup
}

// emitStory creates a story in the projection store via STORY_CREATED +
// a status-transition event so it ends up in the desired state.
func emitStory(t *testing.T, es state.EventStore, ps *state.SQLiteStore, storyID, reqID, targetStatus string) {
	t.Helper()

	// Create the story.
	create := state.NewEvent(state.EventStoryCreated, "", storyID, map[string]any{
		"id":          storyID,
		"req_id":      reqID,
		"title":       "Story " + storyID,
		"description": "test story",
		"complexity":  3,
	})
	if err := es.Append(create); err != nil {
		t.Fatalf("append create: %v", err)
	}
	if err := ps.Project(create); err != nil {
		t.Fatalf("project create: %v", err)
	}

	// Transition to the desired status if it differs from the default "draft".
	statusEvents := map[string]state.EventType{
		"in_progress":  state.EventStoryStarted,
		"review":       state.EventStoryCompleted,
		"pr_submitted": state.EventStoryQAPassed,
		"merged":       state.EventStoryMerged,
	}

	if evtType, ok := statusEvents[targetStatus]; ok {
		transition := state.NewEvent(evtType, "", storyID, nil)
		if err := es.Append(transition); err != nil {
			t.Fatalf("append transition: %v", err)
		}
		if err := ps.Project(transition); err != nil {
			t.Fatalf("project transition: %v", err)
		}
	}
}

func TestRecovery_OrphanedWorktree(t *testing.T) {
	es, ps, cleanup := newRecoveryStores(t)
	defer cleanup()

	// Put a story into in_progress — it has no actual worktree on disk,
	// so recovery should detect the orphan and reset to draft.
	emitStory(t, es, ps, "s-orphan-001", "r-001", "in_progress")

	// Use a temp dir as repoDir — git worktree list will return nothing
	// (or fail), so findWorktreePath returns "".
	repoDir := t.TempDir()

	// Initialise a bare git repo so `git worktree list` doesn't error
	// on "not a git repository".
	initRecoveryRepo(t, repoDir)

	actions := engine.RunRecovery(repoDir, es, ps)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
	}

	a := actions[0]
	if a.StoryID != "s-orphan-001" {
		t.Errorf("StoryID = %q, want %q", a.StoryID, "s-orphan-001")
	}
	if a.Type != "orphaned_worktree" {
		t.Errorf("Type = %q, want %q", a.Type, "orphaned_worktree")
	}

	// Verify the story was actually reset to draft in the projection.
	story, err := ps.GetStory("s-orphan-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "draft" {
		t.Errorf("story status = %q, want %q", story.Status, "draft")
	}
}

func TestRecovery_ReviewStatusOrphan(t *testing.T) {
	es, ps, cleanup := newRecoveryStores(t)
	defer cleanup()

	emitStory(t, es, ps, "s-review-001", "r-001", "review")

	repoDir := t.TempDir()
	initRecoveryRepo(t, repoDir)

	actions := engine.RunRecovery(repoDir, es, ps)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %v", len(actions), actions)
	}
	if actions[0].Type != "orphaned_worktree" {
		t.Errorf("Type = %q, want %q", actions[0].Type, "orphaned_worktree")
	}

	story, err := ps.GetStory("s-review-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "draft" {
		t.Errorf("story status = %q, want %q", story.Status, "draft")
	}
}

func TestRecovery_NoIssues(t *testing.T) {
	es, ps, cleanup := newRecoveryStores(t)
	defer cleanup()

	// Clean state — no stories at all.
	repoDir := t.TempDir()
	initRecoveryRepo(t, repoDir)

	actions := engine.RunRecovery(repoDir, es, ps)
	if len(actions) != 0 {
		t.Fatalf("expected 0 actions, got %d: %v", len(actions), actions)
	}
}

func TestRecovery_DraftStoriesUntouched(t *testing.T) {
	es, ps, cleanup := newRecoveryStores(t)
	defer cleanup()

	// A draft story should NOT be flagged by recovery.
	emitStory(t, es, ps, "s-draft-001", "r-001", "draft")

	repoDir := t.TempDir()
	initRecoveryRepo(t, repoDir)

	actions := engine.RunRecovery(repoDir, es, ps)
	if len(actions) != 0 {
		t.Fatalf("expected 0 actions for draft story, got %d: %v", len(actions), actions)
	}
}

func TestRecovery_MergedStoriesUntouched(t *testing.T) {
	es, ps, cleanup := newRecoveryStores(t)
	defer cleanup()

	// A merged story should not trigger any recovery.
	emitStory(t, es, ps, "s-merged-001", "r-001", "merged")

	repoDir := t.TempDir()
	initRecoveryRepo(t, repoDir)

	actions := engine.RunRecovery(repoDir, es, ps)
	if len(actions) != 0 {
		t.Fatalf("expected 0 actions for merged story, got %d: %v", len(actions), actions)
	}
}

// initRecoveryRepo creates a minimal git repository in dir with an initial commit
// so that git plumbing commands (worktree list, branch --merged) work.
func initRecoveryRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "-C", dir, "init"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git setup %v: %v (output: %s)", args, err, out)
		}
	}
}
