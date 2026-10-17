package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestCleanupStoryBranch_RemovesExternalWorktree: the worktree is found
// through git (it lives under the state dir, not under the repo).
func TestCleanupStoryBranch_RemovesExternalWorktree(t *testing.T) {
	repo := t.TempDir()
	initTestRepo(t, repo)
	wt := addTestWorktree(t, repo, "nxd/s-1")

	if err := cleanupStoryBranch(repo, state.Story{ID: "s-1", Status: "merged", Branch: "nxd/s-1"}); err != nil {
		t.Fatalf("cleanupStoryBranch: %v", err)
	}

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree %s should have been removed (stat err=%v)", wt, err)
	}
	if b := gitOutput(t, repo, "branch", "--list", "nxd/s-1"); strings.TrimSpace(b) != "" {
		t.Errorf("branch nxd/s-1 should have been deleted, got %q", b)
	}
}

// TestArchiveCmd_KeepsUnmergedWorktreesWithoutForce: archiving must not
// destroy an in-flight story's worktree unless --force is given.
func TestArchiveCmd_KeepsUnmergedWorktreesWithoutForce(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{}) // in progress, never merged
	wt := addTestWorktree(t, repo, "nxd/s-1")
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "kept nxd/s-1") {
		t.Errorf("expected the unmerged story to be reported as kept, got: %s", out)
	}
	if _, statErr := os.Stat(filepath.Join(wt, "wip.txt")); statErr != nil {
		t.Errorf("uncommitted work must survive archive without --force: %v", statErr)
	}

	// --force removes it.
	cmd := newArchiveCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	// The requirement is already archived; ListStories still returns its stories.
	out, err = execCmd(t, cmd, env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive --force: %v", err)
	}
	if !strings.Contains(out, "removed worktree and branch nxd/s-1") {
		t.Errorf("expected --force to remove the worktree, got: %s", out)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("worktree should be gone after --force (stat err=%v)", statErr)
	}
}

// TestArchiveCmd_RemovesMergedWorktreeWithoutForce: a MERGED story's worktree
// and branch are removed by a plain archive — the merged check must read the
// pre-archive status, not the "archived" status the command writes first.
func TestArchiveCmd_RemovesMergedWorktreeWithoutForce(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	wt := addTestWorktree(t, repo, "nxd/s-1")

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "removed worktree and branch nxd/s-1") {
		t.Fatalf("expected the merged story's worktree to be removed, got: %s", out)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("worktree should be gone (stat err=%v)", statErr)
	}
	if b := gitOutput(t, repo, "branch", "--list", "nxd/s-1"); strings.TrimSpace(b) != "" {
		t.Errorf("branch nxd/s-1 should have been deleted, got %q", b)
	}
}

// TestArchiveCmd_ForceRefusesWhileLocked: --force is the only path that
// deletes unmerged work, so while the pipeline lock is held (nxd resume is
// working in those worktrees) it must refuse before anything is archived —
// the requirement stays as it was and the worktree survives.
func TestArchiveCmd_ForceRefusesWhileLocked(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})
	wt := addTestWorktree(t, repo, "nxd/s-1")

	lock, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	cmd := newArchiveCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	_, err = execCmd(t, cmd, env.Config, "r-1")
	if err == nil || !strings.Contains(err.Error(), "pipeline lock") {
		t.Fatalf("archive --force must refuse while the pipeline lock is held, got %v", err)
	}
	req, err := env.Proj.GetRequirement("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status == "archived" {
		t.Error("a refused --force must leave the requirement unarchived")
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Errorf("a refused --force must leave the worktree in place: %v", statErr)
	}
}

// TestCleanupStoryBranch_MissingRepoIsAnError: a moved or deleted repo must
// never be reported as "already removed".
func TestCleanupStoryBranch_MissingRepoIsAnError(t *testing.T) {
	err := cleanupStoryBranch(filepath.Join(t.TempDir(), "gone"), state.Story{ID: "s-1", Status: "merged", Branch: "nxd/s-1"})
	if err == nil || errors.Is(err, errAlreadyClean) || !strings.Contains(err.Error(), "locate worktree") {
		t.Fatalf("expected a lookup error for a missing repo, got %v", err)
	}
}

// TestCleanupStoryBranch_AlreadyClean: a merged story whose worktree and
// branch the monitor already removed is the normal case, not a failure.
func TestCleanupStoryBranch_AlreadyClean(t *testing.T) {
	repo := t.TempDir()
	initTestRepo(t, repo)
	err := cleanupStoryBranch(repo, state.Story{ID: "s-9", Status: "merged", Branch: "nxd/already-gone"})
	if !errors.Is(err, errAlreadyClean) {
		t.Fatalf("expected errAlreadyClean, got %v", err)
	}
}

// TestCleanupStoryBranch_ReportsFailure: a branch that git refuses to delete
// (checked out in the main worktree) is reported, not claimed removed.
func TestCleanupStoryBranch_ReportsFailure(t *testing.T) {
	repo := t.TempDir()
	initTestRepo(t, repo)
	co := exec.Command("git", "checkout", "-q", "-b", "nxd/checked-out")
	co.Dir = repo
	if out, err := co.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, out)
	}
	err := cleanupStoryBranch(repo, state.Story{ID: "s-9", Status: "merged", Branch: "nxd/checked-out"})
	if err == nil || errors.Is(err, errAlreadyClean) || !strings.Contains(err.Error(), "delete branch nxd/checked-out") {
		t.Fatalf("expected a delete-branch failure, got %v", err)
	}
}

// TestArchiveCmd_MissingRepo_StillArchivesAndReports: a requirement whose
// repo has moved is archived, and each story's cleanup is reported as "could
// not remove" with git's error — on one line — never as already removed.
func TestArchiveCmd_MissingRepo_StillArchivesAndReports(t *testing.T) {
	env := setupTestEnv(t)
	gone := filepath.Join(env.Dir, "gone")
	seedTestReq(t, env, "r-1", "Moved", gone)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "could not remove nxd/s-1") || strings.Contains(out, "already removed") {
		t.Fatalf("a missing repo must be reported as a failed removal, got: %s", out)
	}
	assertReportLines(t, out)
	req, err := env.Proj.GetRequirement("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != "archived" {
		t.Fatalf("the requirement must still be archived, got %q", req.Status)
	}
}

// TestArchiveCmd_EmptyRepoPath_PrintsCwdNote: like gc, archive says when it
// falls back to the current directory for a requirement without a repo path.
func TestArchiveCmd_EmptyRepoPath_PrintsCwdNote(t *testing.T) {
	env := setupTestEnv(t)
	// The fallback IS the cwd, so run from a temp repo: a later edit to this
	// test must never be able to `branch -D` in the real checkout.
	tmpRepo := initTestRepoAt(t, env.Dir, "cwd")
	t.Chdir(tmpRepo)       // restored by the framework, and it refuses t.Parallel rather than corrupting a sibling
	cwd, err := os.Getwd() // as resolveRepoDir sees it (macOS resolves /var to /private/var)
	if err != nil {
		t.Fatal(err)
	}
	seedTestReq(t, env, "r-1", "No repo", "")

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "requirement r-1 has no repo path; using the current directory "+cwd) {
		t.Fatalf("expected the cwd fallback note, got: %s", out)
	}
}

// assertReportLines: every line of an archive report is a story line or the
// final summary — a multi-line git error must have been folded onto its
// story's line.
func assertReportLines(t *testing.T, out string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "Archived requirement") {
			t.Fatalf("stray continuation line %q in:\n%s", line, out)
		}
	}
}

// TestArchiveCmd_LockedWorktree_FailureOnOneLine: a locked worktree makes
// both the worktree removal and the branch deletion fail; errors.Join
// renders them on two lines, and the report must fold them onto one.
func TestArchiveCmd_LockedWorktree_FailureOnOneLine(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	wt := addTestWorktree(t, repo, "nxd/s-1")
	gitOutput(t, repo, "worktree", "lock", wt)

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "could not remove nxd/s-1") || !strings.Contains(out, "remove worktree") || !strings.Contains(out, "delete branch") {
		t.Fatalf("both failures must be reported, got:\n%s", out)
	}
	assertReportLines(t, out)
}

// TestArchiveCmd_UnstartedStory_NothingToRemove: a story that never started
// has no worktree and no branch; archive must not claim to keep or to have
// already removed something that never existed.
func TestArchiveCmd_UnstartedStory_NothingToRemove(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedTestStory(t, env, "s-1", "r-1", "Never started", 1) // draft, no STORY_STARTED

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "nothing to remove for story s-1") || strings.Contains(out, "kept ") {
		t.Fatalf("want 'nothing to remove', got:\n%s", out)
	}
	cmd := newArchiveCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	out, err = execCmd(t, cmd, env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive --force: %v", err)
	}
	if !strings.Contains(out, "nothing to remove for story s-1") || strings.Contains(out, "already removed") {
		t.Fatalf("--force on a never-started story: want 'nothing to remove', got:\n%s", out)
	}
	if strings.Contains(out, "never started") {
		t.Fatalf("a re-run on an archived story must not claim it never started, got:\n%s", out)
	}
}

// TestArchiveCmd_UnmergedStory_MissingRepo_NotReportedAsKept: without
// --force, an unmerged story whose repo cannot be checked is reported as
// "could not check", never as "kept" (nothing was verified, and --force would
// not help).
func TestArchiveCmd_UnmergedStory_MissingRepo_NotReportedAsKept(t *testing.T) {
	env := setupTestEnv(t)
	gone := filepath.Join(env.Dir, "gone")
	seedTestReq(t, env, "r-1", "Moved", gone)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "could not check nxd/s-1 (story s-1)") || strings.Contains(out, "kept ") || strings.Contains(out, "nothing to remove") {
		t.Fatalf("a lookup failure must be reported as such, got:\n%s", out)
	}
	assertReportLines(t, out)
}

// TestArchiveCmd_Force_SplitParent_NothingToRemove: a split parent never
// started, so --force must not claim its branch was "already removed". The
// status list StoryStarted replaced had no "split" case and did exactly that.
func TestArchiveCmd_Force_SplitParent_NothingToRemove(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedTestStory(t, env, "s-1", "r-1", "Too big", 8) // draft, no STORY_STARTED
	split := state.NewEvent(state.EventStorySplit, "tech-lead", "s-1", map[string]any{"id": "s-1"})
	if err := env.Events.Append(split); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(split); err != nil {
		t.Fatal(err)
	}

	cmd := newArchiveCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive --force: %v", err)
	}
	if !strings.Contains(out, "nothing to remove for story s-1") || strings.Contains(out, "already removed") {
		t.Fatalf("a split parent never had a branch: want 'nothing to remove', got:\n%s", out)
	}
}

// TestArchiveCmd_DraftAfterReviewFailure_BranchKept: a story reset to draft
// after a failed review still has its branch and its work. Without --force it
// must be reported as kept, not as "nothing to remove" — the status list read
// "draft" as never started.
func TestArchiveCmd_DraftAfterReviewFailure_BranchKept(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{}) // in_progress, branch projected
	reset := state.NewEvent(state.EventStoryReset, "monitor", "s-1", map[string]any{"reason": "review failed"})
	if err := env.Events.Append(reset); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(reset); err != nil {
		t.Fatal(err)
	}
	br := exec.Command("git", "branch", "nxd/s-1")
	br.Dir = repo
	if out, err := br.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	out, err := execCmd(t, newArchiveCmd(), env.Config, "r-1")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "kept nxd/s-1") || strings.Contains(out, "nothing to remove") {
		t.Fatalf("a draft with a live branch must be kept, got:\n%s", out)
	}
	if !nxdgit.BranchExists(repo, "nxd/s-1") {
		t.Fatal("archive without --force must not delete the branch")
	}
}

// TestArchiveCmd_Default_RefusesWhileLockHeld: the merged-only default path
// removes worktrees and branches and appends the archive event, so it takes
// the pipeline lock exactly as --force does. Nothing may be archived when it
// cannot get it.
func TestArchiveCmd_Default_RefusesWhileLockHeld(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)

	lock, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	before, err := env.Events.Count(state.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execCmd(t, newArchiveCmd(), env.Config, "r-1"); err == nil || !strings.Contains(err.Error(), "pipeline lock") {
		t.Fatalf("archive must refuse while the pipeline lock is held, got %v", err)
	}
	req, err := env.Proj.GetRequirement("r-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status == "archived" {
		t.Error("a refused archive must leave the requirement unarchived")
	}
	after, err := env.Events.Count(state.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("a refused archive must append nothing: %d -> %d events", before, after)
	}
}

// TestNeverStartedNote_ReadsTheProjection: "never started" is a claim about
// the projection, not about the status. A draft story whose branch was
// projected did start, whatever happened to the branch since — the status
// list this replaced said the opposite, which is the bug the whole
// StoryStarted rule exists for.
func TestNeverStartedNote_ReadsTheProjection(t *testing.T) {
	merged := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		story state.Story
		want  string
	}{
		{"never projected a branch", state.Story{ID: "s-1", Status: "draft"}, "nxd/s-1 never started"},
		{"branch projected, since removed", state.Story{ID: "s-1", Status: "draft", Branch: "nxd/s-1"}, "nxd/s-1"},
		{"merged, branch cleaned up", state.Story{ID: "s-1", Status: "merged", MergedAt: merged}, "nxd/s-1"},
		{"archived re-run", state.Story{ID: "s-1", Status: "archived"}, "nxd/s-1"},
		{"split parent", state.Story{ID: "s-1", Status: "split"}, "nxd/s-1 never started"},
	} {
		if got := neverStartedNote(tc.story, "nxd/s-1"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
