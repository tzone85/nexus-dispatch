package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// masterRepo initialises a repo whose only branch is master (an older repo).
func masterRepo(t *testing.T, dir string) string {
	t.Helper()
	repo := initTestRepoAt(t, dir, "repo")
	mv := exec.Command("git", "branch", "-M", "master")
	mv.Dir = repo
	if out, err := mv.CombinedOutput(); err != nil {
		t.Fatalf("git branch -M master: %v\n%s", err, out)
	}
	return repo
}

// dropBaseBranch rewrites the test config so merge.base_branch is empty — the
// default config since it stopped defaulting to main.
func dropBaseBranch(t *testing.T, cfgPath string) {
	t.Helper()
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), "  base_branch: main\n", "", 1)
	if cfg == string(b) {
		t.Fatal("test config did not contain base_branch: main")
	}
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveMergeBase: an empty base is detected from the repo (master
// here); a configured base is left alone; the input config is not mutated.
func TestResolveMergeBase(t *testing.T) {
	repo := masterRepo(t, t.TempDir())
	in := config.MergeConfig{Mode: "local"}
	got := resolveMergeBase(in, repo)
	if got.BaseBranch != "master" {
		t.Fatalf("empty base must be detected as master, got %q", got.BaseBranch)
	}
	if in.BaseBranch != "" {
		t.Fatal("resolveMergeBase must return a copy, not mutate its input")
	}
	if got := resolveMergeBase(config.MergeConfig{Mode: "github", BaseBranch: "develop"}, repo); got.BaseBranch != "develop" {
		t.Fatalf("a configured base must win, got %q", got.BaseBranch)
	}
}

// TestStoryRepo_UsesRequirementRepoAndDetectsBase: merge and review run in
// the requirement's repo (not the cwd) with the detected base, in local and
// github mode alike, without touching s.Config.
func TestStoryRepo_UsesRequirementRepoAndDetectsBase(t *testing.T) {
	env := setupTestEnv(t)
	dropBaseBranch(t, env.Config)
	repo := masterRepo(t, env.Dir)
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "", time.Time{})

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, mode := range []string{"local", "github"} {
		s.Config.Merge.Mode = mode
		story, err := s.Proj.GetStory("s-1")
		if err != nil {
			t.Fatal(err)
		}
		repoDir, mergeCfg, _, err := storyRepo(s, story)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if repoDir != repo || mergeCfg.BaseBranch != "master" || mergeCfg.Mode != mode {
			t.Fatalf("%s: want %s/master, got %s/%s", mode, repo, repoDir, mergeCfg.BaseBranch)
		}
		if s.Config.Merge.BaseBranch != "" {
			t.Fatal("storyRepo must not mutate s.Config")
		}
		if state.StoryBranch(story) != "nxd/s-1" {
			t.Fatalf("an empty projected branch resolves to the canonical name, got %q", state.StoryBranch(story))
		}
	}
	// A story whose requirement is missing is an error, not the cwd.
	if _, _, _, err := storyRepo(s, state.Story{ID: "s-x", ReqID: "r-missing"}); err == nil || !strings.Contains(err.Error(), "r-missing") {
		t.Fatalf("missing requirement must be an error naming it, got %v", err)
	}
}

// TestReviewStory_DiffsAgainstResolvedBase: review runs in the requirement
// repo and diffs against the detected base (master), so the story's change
// shows up in "Changes".
func TestReviewStory_DiffsAgainstResolvedBase(t *testing.T) {
	env := setupTestEnv(t)
	dropBaseBranch(t, env.Config)
	repo := masterRepo(t, env.Dir)
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})
	wt := addTestWorktree(t, repo, "nxd/s-1")
	if err := os.WriteFile(filepath.Join(wt, "feature.go"), []byte("package m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "feature.go"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "feature"}} {
		c := exec.Command("git", args...)
		c.Dir = wt
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "Changes:") || !strings.Contains(out, "feature.go") {
		t.Fatalf("expected the story's change in the diff stat, got:\n%s", out)
	}
	if strings.Contains(out, "unavailable") {
		t.Fatalf("diff must succeed against the detected base, got:\n%s", out)
	}
}

// TestReviewStory_GitFailureIsPrinted: a branch git cannot diff is reported
// as unavailable with git's message, never silently omitted.
func TestReviewStory_GitFailureIsPrinted(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})
	// The branch exists, but the base does not: git fails, and review says so.
	gitOutput(t, repo, "branch", "nxd/s-1")
	b, err := os.ReadFile(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.Config, []byte(strings.Replace(string(b), "base_branch: main", "base_branch: no-such-base", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "Changes: unavailable (") || !strings.Contains(out, "Diff: unavailable (") {
		t.Fatalf("git failures must be printed, got:\n%s", out)
	}
}

// TestReviewStory_UnstartedStory_NoGitNoise: a draft story has no branch
// yet; review says so instead of printing a git failure for a branch that
// was never created.
func TestReviewStory_UnstartedStory_NoGitNoise(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedTestStory(t, env, "s-1", "r-1", "Draft", 1)

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "Changes: none yet (branch nxd/s-1 does not exist)") || strings.Contains(out, "unavailable") || strings.Contains(out, "fatal:") {
		t.Fatalf("want a plain 'none yet' line and no git noise, got:\n%s", out)
	}
	if !strings.Contains(out, "Branch: nxd/s-1") {
		t.Fatalf("the header shows the resolved branch, got:\n%s", out)
	}
}

// TestRunMergeStory_MergesInRequirementRepoWithDetectedBase is the end-to-end
// guard for the merge commit: the config has no base branch, the repo's only
// branch is master, the cwd is not the repo, and `nxd merge` still merges the
// story's real commit into master and records the story as merged. Before the
// fix this reached `git checkout ""` in the cwd.
func TestRunMergeStory_MergesInRequirementRepoWithDetectedBase(t *testing.T) {
	env := setupTestEnv(t)
	dropBaseBranch(t, env.Config)
	repo := masterRepo(t, env.Dir)
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})
	ready := state.NewEvent(state.EventStoryMergeReady, "qa", "s-1", nil)
	if err := env.Events.Append(ready); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(ready); err != nil {
		t.Fatal(err)
	}

	// A real commit on the story branch.
	gitOutput(t, repo, "checkout", "-q", "-b", "nxd/s-1")
	if err := os.WriteFile(filepath.Join(repo, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, repo, "add", "feature.go")
	gitOutput(t, repo, "commit", "-q", "-m", "feature")
	gitOutput(t, repo, "checkout", "-q", "master")
	storySHA := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "nxd/s-1"))

	// The cwd is deliberately somewhere else.
	t.Chdir(t.TempDir()) // restored by the framework, and it refuses t.Parallel

	out, err := execCmd(t, newMergeStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Merged story s-1") {
		t.Fatalf("want the merged line, got:\n%s", out)
	}
	if !strings.Contains(gitOutput(t, repo, "branch", "--merged", "master"), "nxd/s-1") {
		t.Fatalf("nxd/s-1 (%s) is not merged into master:\n%s", storySHA, gitOutput(t, repo, "log", "--oneline", "master"))
	}
	story, err := env.Proj.GetStory("s-1")
	if err != nil {
		t.Fatal(err)
	}
	if story.Status != "merged" {
		t.Fatalf("story status after merge: %q, want merged", story.Status)
	}
}

// TestReviewStory_MergedStory_BranchRemoved: the monitor deletes a merged
// story's branch right after the merge, so review must not read that as
// "none yet".
func TestReviewStory_MergedStory_BranchRemoved(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	seedTestReq(t, env, "r-1", "Req", repo)
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "Changes: branch nxd/s-1 no longer exists (removed after merge or cleanup)") || strings.Contains(out, "none yet") {
		t.Fatalf("a merged story whose branch is gone must not read as 'none yet', got:\n%s", out)
	}
}

// TestReviewStory_MissingRepo_IsUnavailable: a requirement repo that is gone
// prints "unavailable", never "none yet".
func TestReviewStory_MissingRepo_IsUnavailable(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Moved", filepath.Join(env.Dir, "gone"))
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "Changes: unavailable (") || strings.Contains(out, "none yet") {
		t.Fatalf("a missing repo must be reported as unavailable, got:\n%s", out)
	}
}

// TestReviewStory_EmptyRepoPath_PrintsCwdNote: like gc and archive, review
// says when it falls back to the current directory.
func TestReviewStory_EmptyRepoPath_PrintsCwdNote(t *testing.T) {
	env := setupTestEnv(t)
	repo := initTestRepoAt(t, env.Dir, "repo")
	t.Chdir(repo)          // restored by the framework, and it refuses t.Parallel
	cwd, err := os.Getwd() // as resolveRepoDir sees it (macOS resolves /var to /private/var)
	if err != nil {
		t.Fatal(err)
	}
	seedTestReq(t, env, "r-1", "No repo", "")
	seedTestStory(t, env, "s-1", "r-1", "Draft", 1)

	out, err := execCmd(t, newReviewStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(out, "requirement r-1 has no repo path; using the current directory "+cwd) {
		t.Fatalf("review must print the cwd fallback note, got:\n%s", out)
	}
}

// TestRunMergeStory_EmptyRepoPath_PrintsCwdNote: a merge into the current
// directory is a write, so the fallback is never silent.
func TestRunMergeStory_EmptyRepoPath_PrintsCwdNote(t *testing.T) {
	env := setupTestEnv(t)
	dropBaseBranch(t, env.Config)
	repo := masterRepo(t, env.Dir)
	t.Chdir(repo)          // restored by the framework, and it refuses t.Parallel
	cwd, err := os.Getwd() // as resolveRepoDir sees it (macOS resolves /var to /private/var)
	if err != nil {
		t.Fatal(err)
	}
	seedTestReq(t, env, "r-1", "No repo", "")
	seedStartedStory(t, env, "r-1", "s-1", "nxd/s-1", time.Time{})
	ready := state.NewEvent(state.EventStoryMergeReady, "qa", "s-1", nil)
	if err := env.Events.Append(ready); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(ready); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, repo, "checkout", "-q", "-b", "nxd/s-1")
	if err := os.WriteFile(filepath.Join(repo, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, repo, "add", "feature.go")
	gitOutput(t, repo, "commit", "-q", "-m", "feature")
	gitOutput(t, repo, "checkout", "-q", "master")

	out, err := execCmd(t, newMergeStoryCmd(), env.Config, "s-1")
	if err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if !strings.Contains(out, "requirement r-1 has no repo path; using the current directory "+cwd) || !strings.Contains(out, "Merged story s-1") {
		t.Fatalf("merge must print the cwd fallback note and merge, got:\n%s", out)
	}
}

// TestRunDiff_DetectsMasterBase: nxd diff shares the empty-base rule — a
// master-only worktree with the default (empty) merge.base_branch diffs
// against master instead of a missing main.
func TestRunDiff_DetectsMasterBase(t *testing.T) {
	env := setupTestEnv(t)
	dropBaseBranch(t, env.Config)
	wt := masterRepo(t, filepath.Join(env.Dir, ".nxd", "worktrees"))
	// masterRepo names the directory "repo"; diff looks for <stateDir>/worktrees/<story>.
	storyDir := filepath.Join(env.Dir, ".nxd", "worktrees", "s-diff-master")
	if err := os.Rename(wt, storyDir); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, storyDir, "checkout", "-q", "-b", "nxd/s-diff-master")
	if err := os.WriteFile(filepath.Join(storyDir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, storyDir, "add", "f.txt")
	gitOutput(t, storyDir, "commit", "-q", "-m", "f")

	cmd := newDiffCmd()
	if err := cmd.Flags().Set("stat", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(t, cmd, env.Config, "s-diff-master")
	if err != nil {
		t.Fatalf("diff on a master-only worktree with no configured base: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Base: master") || !strings.Contains(out, "f.txt") {
		t.Fatalf("want the detected base and the change, got:\n%s", out)
	}
}
