package cli

import (
	"database/sql"
	"fmt"
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

var longAgo = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // well past the 7-day retention in setupTestEnv

// pinGCNow fixes the retention clock for a test: gc passes gcNow to the
// reaper, so "recent" and "expired" are decided here rather than by the day
// the suite happens to run (misc CLAUDE.md, Tests — no wall-clock).
func pinGCNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := gcNow
	gcNow = func() time.Time { return now }
	t.Cleanup(func() { gcNow = prev })
}

// TestRunGC_DryRun_UsesMergedAt: retention is measured from the merge time,
// not the story's creation time (which would print 2026-01-01 here).
func TestRunGC_DryRun_UsesMergedAt(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Req", env.Dir)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))

	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nxd/s-1 (story: s-1, merged: 2026-03-15") {
		t.Errorf("dry-run output must show the merge date, got: %s", out)
	}
}

// TestRunGC_SkipsZeroMergedAt: a merged row without a merge time (NULL
// merged_at) must not be eligible — the zero time is always past the cutoff.
func TestRunGC_SkipsZeroMergedAt(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Req", env.Dir)
	// Merge it, then blank merged_at like a pre-migration row: straight into
	// the database, since no event can produce a NULL merged_at.
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	db, err := sql.Open("sqlite3", filepath.Join(env.Dir, ".nxd", "nxd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE stories SET merged_at = NULL WHERE id = ?`, "s-1"); err != nil {
		t.Fatal(err)
	}

	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "(story: s-1, merged:") {
		t.Errorf("a story without a merge time must not be listed for cleanup, got: %s", out)
	}
	if !strings.Contains(out, "skipped nxd/s-1 (story s-1): no recorded merge time") {
		t.Errorf("the skip must be visible, got: %s", out)
	}
	if !strings.Contains(out, "would check 0 branches") {
		t.Errorf("the skipped story must not be counted, got: %s", out)
	}
}

// TestRunGC_RetainsRecentlyMergedOldStory: retention is measured from the
// merge time against a pinned clock — an old story merged yesterday keeps its
// branch. Reading CreatedAt (2026-01-01 here) would delete it.
func TestRunGC_RetainsRecentlyMergedOldStory(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	pinGCNow(t, now)
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", now.AddDate(0, 0, -1)) // created 2026-01-01, merged yesterday
	br := exec.Command("git", "branch", "nxd/s-1")
	br.Dir = repo
	if out, err := br.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No branches eligible for cleanup") {
		t.Fatalf("a branch merged one day ago must survive a 7-day retention, got: %s", out)
	}
	if !nxdgit.BranchExists(repo, "nxd/s-1") {
		t.Fatal("the branch was deleted despite a recent merge time")
	}
}

// TestRunGC_DoesNotDesyncProjection: a real gc run appends BRANCH_DELETED /
// GC_COMPLETED to the log; it must project them (no-op) so the watermark
// advances, or the next loadStores rebuilds the projection and replays an
// archive's REQ_COMPLETED as plain "completed".
func TestRunGC_DoesNotDesyncProjection(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")

	// An archived requirement that a faithful-but-lossy replay would revert.
	seedTestReq(t, env, "r-old", "Old", repo)
	if _, err := execCmd(t, newArchiveCmd(), env.Config, "r-old"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// A merged story with a real branch, merged long before the 7-day retention.
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	br := exec.Command("git", "branch", "nxd/s-1")
	br.Dir = repo
	if out, err := br.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Cleaned up 1 branches") {
		t.Fatalf("expected one deletion, got: %s", out)
	}

	applied, err := env.Proj.AppliedEventCount()
	if err != nil {
		t.Fatal(err)
	}
	logged, err := env.Events.Count(state.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != logged {
		t.Fatalf("projection watermark %d != log %d after gc — the next command would rebuild and undo archives", applied, logged)
	}

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores: %v", err)
	}
	defer s.Close()
	req, err := s.Proj.GetRequirement("r-old")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != "archived" {
		t.Fatalf("archived requirement reverted to %q after gc + reload", req.Status)
	}
}

// TestRunGC_RemovesLeftoverWorktreeBeforeBranch: git refuses `branch -D` for
// a branch that is checked out somewhere, so a worktree the monitor failed
// to remove must be force-removed first — both while it still exists on disk
// and when only its admin entry is left (prunable).
func TestRunGC_RemovesLeftoverWorktreeBeforeBranch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prunable bool
	}{
		{"live worktree", false},
		{"prunable worktree (directory already deleted)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestEnv(t)
			repo := initTestRepoAt(t, env.Dir, "repo")
			seedTestReq(t, env, "r-1", "Req", repo)
			seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
			wt := addTestWorktree(t, repo, "nxd/s-1")
			if tc.prunable {
				if err := os.RemoveAll(wt); err != nil {
					t.Fatal(err)
				}
			}

			out, err := execCmd(t, newGCCmd(), env.Config)
			if err != nil {
				t.Fatalf("gc: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Cleaned up 1 branches") {
				t.Fatalf("expected the branch to be deleted despite the leftover worktree, got: %s", out)
			}
			if b := gitOutput(t, repo, "branch", "--list", "nxd/s-1"); strings.TrimSpace(b) != "" {
				t.Errorf("branch nxd/s-1 should be gone, got %q", b)
			}
			if l := gitOutput(t, repo, "worktree", "list", "--porcelain"); strings.Contains(l, wt) {
				t.Errorf("worktree %s should be gone from the worktree list:\n%s", wt, l)
			}
			if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
				t.Errorf("worktree directory should be gone (stat err=%v)", statErr)
			}
			// worktree remove --force discards uncommitted edits there;
			// "Cleaned up 1 branches." on its own does not say so.
			// The path git reports is the resolved one (macOS resolves
			// /var to /private/var), so match the report, not the
			// TempDir spelling.
			if !strings.Contains(out, "Removed leftover worktree ") || !strings.Contains(out, "checked out on nxd/s-1") {
				t.Errorf("gc must report the leftover worktree it force-removed, got:\n%s", out)
			}
		})
	}
}

// TestRunGC_ContinuesAcrossRepos: a requirement whose repo no longer exists is
// an error for that repo, and the other repos are still cleaned.
func TestRunGC_ContinuesAcrossRepos(t *testing.T) {
	env := setupTestEnv(t)
	gone := filepath.Join(env.Dir, "gone")
	seedTestReq(t, env, "r-a", "Moved", gone)
	seedStartedStory(t, env, "r-a", "s-a", "nxd/s-a", longAgo)
	repoB := initTestRepoAt(t, env.Dir, "repo-b")
	seedTestReq(t, env, "r-b", "Live", repoB)
	seedStartedStory(t, env, "r-b", "s-b", "nxd/s-b", longAgo)
	br := exec.Command("git", "branch", "nxd/s-b")
	br.Dir = repoB
	if out, err := br.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err == nil || !strings.Contains(err.Error(), gone) {
		t.Fatalf("want an error naming the missing repo %s, got %v", gone, err)
	}
	if !strings.Contains(out, "Cleaned up 1 branches") {
		t.Fatalf("repo B must still be cleaned, got: %s", out)
	}
	if b := gitOutput(t, repoB, "branch", "--list", "nxd/s-b"); strings.TrimSpace(b) != "" {
		t.Errorf("branch nxd/s-b in repo B should be gone, got %q", b)
	}
}

// TestRunGC_EmptyRepoPathFallsBackToCwdWithNote: a requirement submitted
// without a repo path is checked in the current directory, and gc says so.
func TestRunGC_EmptyRepoPathFallsBackToCwdWithNote(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "No repo", "")
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "repo: "+cwd) || !strings.Contains(out, "requirement r-1 has no repo path; using the current directory "+cwd) {
		t.Fatalf("expected the cwd fallback to be listed and explained, got: %s", out)
	}
}

// TestRunGC_SkipsStoryWithUnreadableRequirement: no requirement row means no
// repo to run git in — the story is skipped and reported, never deleted from
// the current directory.
func TestRunGC_SkipsStoryWithUnreadableRequirement(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Req", env.Dir)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	// Point the story at a requirement that does not exist.
	db, err := sql.Open("sqlite3", filepath.Join(env.Dir, ".nxd", "nxd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE stories SET req_id = 'r-missing' WHERE id = 's-1'`); err != nil {
		t.Fatal(err)
	}
	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "skipped nxd/s-1") || strings.Contains(out, "would check 1") {
		t.Fatalf("expected the story to be skipped with a reason, got: %s", out)
	}
}

// TestResolveRepoDir: the recorded repo wins; an empty one is the absolute cwd.
func TestResolveRepoDir(t *testing.T) {
	if got, err := resolveRepoDir(state.Requirement{ID: "r", RepoPath: "/srv/repo"}); err != nil || got != "/srv/repo" {
		t.Fatalf("recorded repo: got %q, %v", got, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := resolveRepoDir(state.Requirement{ID: "r"}); err != nil || got != cwd || !filepath.IsAbs(got) {
		t.Fatalf("empty repo path: want the absolute cwd %q, got %q, %v", cwd, got, err)
	}
}

// TestRunGC_AllDeletionsFailed_DoesNotSayNoneEligible: when every eligible
// deletion fails (the branch is checked out in the main worktree, which git
// refuses to delete), gc returns the failure and must not print "No branches
// eligible for cleanup." — nothing was ineligible, it was refused.
func TestRunGC_AllDeletionsFailed_DoesNotSayNoneEligible(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	co := exec.Command("git", "checkout", "-q", "-b", "nxd/s-1")
	co.Dir = repo
	if out, err := co.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, out)
	}

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err == nil || !strings.Contains(err.Error(), "nxd/s-1") {
		t.Fatalf("want the refused deletion returned, got %v", err)
	}
	if strings.Contains(out, "No branches eligible") {
		t.Fatalf("a failed deletion must not read as nothing eligible, got: %s", out)
	}
}

// TestRunGC_DryRun_ReportsMissingRepo: the preview says when a requirement's
// repo cannot be checked, instead of listing its branches as if it existed.
func TestRunGC_DryRun_ReportsMissingRepo(t *testing.T) {
	env := setupTestEnv(t)
	gone := filepath.Join(env.Dir, "gone")
	seedTestReq(t, env, "r-a", "Moved", gone)
	seedStartedStory(t, env, "r-a", "s-a", "nxd/s-a", longAgo)

	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "repo for requirement r-a cannot be checked") || !strings.Contains(out, gone) {
		t.Fatalf("dry run must report the missing repo, got: %s", out)
	}
}

// TestRunGC_EmptyProjectedBranchUsesCanonicalName: a merged row whose branch
// never reached the projection is still cleaned, under the dispatcher's
// canonical "nxd/<id>" (state.StoryBranch).
func TestRunGC_EmptyProjectedBranchUsesCanonicalName(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "", longAgo) // STORY_STARTED without a branch
	br := exec.Command("git", "branch", "nxd/s-1")
	br.Dir = repo
	if out, err := br.CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v\n%s", err, out)
	}

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err != nil {
		t.Fatalf("gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Cleaned up 1 branches") {
		t.Fatalf("the canonical branch must be cleaned, got: %s", out)
	}
	if b := gitOutput(t, repo, "branch", "--list", "nxd/s-1"); strings.TrimSpace(b) != "" {
		t.Errorf("branch nxd/s-1 should be gone, got %q", b)
	}
}

// TestRunGC_LockedWorktree_ErrorNamesTheLock: a worktree git refuses to
// remove (locked) is why branch -D fails; the error must say so instead of
// only "checked out at …".
func TestRunGC_LockedWorktree_ErrorNamesTheLock(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	wt := addTestWorktree(t, repo, "nxd/s-1")
	gitOutput(t, repo, "worktree", "lock", "--reason", "operator is in here", wt)

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("the error must carry the worktree failure (locked), got %v\n%s", err, out)
	}
	if strings.Contains(out, "No branches eligible") {
		t.Fatalf("a refused deletion must not read as nothing eligible: %s", out)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("a locked worktree must survive: %v", statErr)
	}
}

// TestRunGC_NonRepoDirIsAnError: a directory that exists but is not a git
// repository must not read as "nothing eligible" (every BranchExists would
// be false); it is reported like a missing repo.
func TestRunGC_NonRepoDirIsAnError(t *testing.T) {
	env := setupTestEnv(t)
	plain := filepath.Join(env.Dir, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	seedTestReq(t, env, "r-1", "Plain", plain)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("want a not-a-repository error, got %v\n%s", err, out)
	}
	if strings.Contains(out, "No branches eligible") {
		t.Fatalf("a non-repo directory must not read as nothing eligible: %s", out)
	}
}

// TestRunGC_RefusesWhileLocked: gc removes worktrees and branches, so like
// archive --force it refuses while a pipeline holds the lock; a dry run
// still lists.
func TestRunGC_RefusesWhileLocked(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)
	gitOutput(t, repo, "branch", "nxd/s-1")

	lock, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	_, err = execCmd(t, newGCCmd(), env.Config)
	if err == nil || !strings.Contains(err.Error(), "pipeline lock") {
		t.Fatalf("gc must refuse while the pipeline lock is held, got %v", err)
	}
	if !strings.Contains(gitOutput(t, repo, "branch", "--list", "nxd/s-1"), "nxd/s-1") {
		t.Fatal("a refused gc must leave the branch in place")
	}
	cmd := newGCCmd()
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatal(err)
	}
	if out, err := execCmd(t, cmd, env.Config); err != nil || !strings.Contains(out, "would check 1") {
		t.Fatalf("a dry run does not need the lock, got err=%v out=%s", err, out)
	}
}

// fakeRequirementGetter drives the repo lookup without SQLite.
type fakeRequirementGetter struct {
	reqs  map[string]state.Requirement
	calls int
}

func (f *fakeRequirementGetter) GetRequirement(id string) (state.Requirement, error) {
	f.calls++
	req, ok := f.reqs[id]
	if !ok {
		return state.Requirement{}, fmt.Errorf("requirement %s not found", id)
	}
	return req, nil
}

// TestRepoDirCache_AnswersOncePerRequirement: every story of a requirement
// shares its repo, so the lookup — and its notes — happen once. A note that
// repeats per story buries the ones that matter.
func TestRepoDirCache_AnswersOncePerRequirement(t *testing.T) {
	getter := &fakeRequirementGetter{reqs: map[string]state.Requirement{
		"r-1": {ID: "r-1", RepoPath: "/repo/one"},
	}}
	cache := newRepoDirCache(getter)

	dir, notes, err := cache.dirFor("r-1")
	if err != nil || dir != "/repo/one" || len(notes) != 0 {
		t.Fatalf("first lookup: dir=%q notes=%v err=%v", dir, notes, err)
	}
	dir, notes, err = cache.dirFor("r-1")
	if err != nil || dir != "/repo/one" || len(notes) != 0 {
		t.Fatalf("second lookup: dir=%q notes=%v err=%v", dir, notes, err)
	}
	if getter.calls != 1 {
		t.Fatalf("the requirement is read once, got %d reads", getter.calls)
	}

	if _, _, err := cache.dirFor("r-missing"); err == nil {
		t.Fatal("an unreadable requirement is an error")
	}
	if _, _, err := cache.dirFor("r-missing"); err == nil {
		t.Fatal("and stays one")
	}
	if getter.calls != 2 {
		t.Fatalf("a failed lookup is remembered too, got %d reads", getter.calls)
	}
}

// TestRunGC_LockRefusedNamesHolder: gc refuses while the pipeline lock is
// held, and the operator's first question is which process holds it — so the
// refusal carries the holder's pid, not just "could not acquire".
func TestRunGC_LockRefusedNamesHolder(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", longAgo)

	// seedStartedStory projects the branch; gc acts on git, so create it too
	// or "nothing was deleted" would be true whatever gc did.
	gitOutput(t, repo, "branch", "nxd/s-1")

	lock, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatalf("take the lock the way nxd resume does: %v", err)
	}
	defer func() { _ = lock.Release() }()

	out, err := execCmd(t, newGCCmd(), env.Config)
	if err == nil {
		t.Fatalf("gc must refuse while the pipeline lock is held, got:\n%s", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "is nxd resume running?") {
		t.Errorf("the refusal must name the likely holder: %v", err)
	}
	if !strings.Contains(msg, fmt.Sprintf("pid %d", os.Getpid())) {
		t.Errorf("the refusal must carry the holding pid (%d): %v", os.Getpid(), err)
	}
	if b := gitOutput(t, repo, "branch", "--list", "nxd/s-1"); strings.TrimSpace(b) == "" {
		t.Error("a refused gc must not have deleted anything")
	}
}
