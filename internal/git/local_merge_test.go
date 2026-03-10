package git_test

import (
	"os"
	"path/filepath"
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

// setupMergeRepo creates a temporary git repo with an initial commit on the
// main branch. It returns the repo directory path.
func setupMergeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCmd(t, dir, "git", "init", "--initial-branch=main")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("initial"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "init")
	return dir
}

// addFeatureBranch creates a feature branch from main with a new file committed.
func addFeatureBranch(t *testing.T, repoDir, branchName, fileName, content string) {
	t.Helper()
	runCmd(t, repoDir, "git", "checkout", "-b", branchName)
	if err := os.WriteFile(filepath.Join(repoDir, fileName), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", fileName, err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "add "+fileName)
	runCmd(t, repoDir, "git", "checkout", "main")
}

// addConflictingBranches creates a feature branch and a commit on main that
// both modify the same file on the same line, guaranteeing a merge conflict.
func addConflictingBranches(t *testing.T, repoDir, branchName string) {
	t.Helper()

	// Create feature branch with a change to README
	runCmd(t, repoDir, "git", "checkout", "-b", branchName)
	if err := os.WriteFile(filepath.Join(repoDir, "README"), []byte("feature change"), 0644); err != nil {
		t.Fatalf("write README on feature: %v", err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "feature change to README")

	// Switch back to main and make a conflicting change
	runCmd(t, repoDir, "git", "checkout", "main")
	if err := os.WriteFile(filepath.Join(repoDir, "README"), []byte("main change"), 0644); err != nil {
		t.Fatalf("write README on main: %v", err)
	}
	runCmd(t, repoDir, "git", "add", ".")
	runCmd(t, repoDir, "git", "commit", "-m", "main change to README")
}

func TestLocalMerger_Merge_Success(t *testing.T) {
	repo := setupMergeRepo(t)
	addFeatureBranch(t, repo, "feature/clean", "feature.txt", "hello world")

	merger := nxdgit.NewLocalMerger(repo)
	result, err := merger.Merge("feature/clean", "main")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if result.Branch != "feature/clean" {
		t.Fatalf("expected branch 'feature/clean', got %q", result.Branch)
	}
	if result.BaseBranch != "main" {
		t.Fatalf("expected base branch 'main', got %q", result.BaseBranch)
	}
	if result.MergedSHA == "" {
		t.Fatal("expected non-empty merged SHA")
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", result.Conflicts)
	}

	// Verify the feature file exists on main after merge
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); os.IsNotExist(err) {
		t.Fatal("feature.txt should exist on main after merge")
	}
}

func TestLocalMerger_Merge_WithConflicts(t *testing.T) {
	repo := setupMergeRepo(t)
	addConflictingBranches(t, repo, "feature/conflict")

	merger := nxdgit.NewLocalMerger(repo)
	result, err := merger.Merge("feature/conflict", "main")
	if err == nil {
		t.Fatal("expected error from conflicting merge")
	}

	if len(result.Conflicts) == 0 {
		t.Fatal("expected conflict file names in result")
	}

	// Verify README is in the conflict list
	found := false
	for _, f := range result.Conflicts {
		if f == "README" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'README' in conflicts, got %v", result.Conflicts)
	}

	// Verify merge was aborted — we should still be on main cleanly
	branch, err := nxdgit.CurrentBranch(repo)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected to be on 'main' after abort, got %q", branch)
	}
}

func TestLocalMerger_CanMerge_Clean(t *testing.T) {
	repo := setupMergeRepo(t)
	addFeatureBranch(t, repo, "feature/check-clean", "clean.txt", "clean content")

	merger := nxdgit.NewLocalMerger(repo)
	canMerge, conflicts, err := merger.CanMerge("feature/check-clean", "main")
	if err != nil {
		t.Fatalf("can merge: %v", err)
	}
	if !canMerge {
		t.Fatal("expected clean merge to be possible")
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
	}

	// Verify working directory is clean after dry run
	branch, err := nxdgit.CurrentBranch(repo)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("expected to be on 'main' after can-merge check, got %q", branch)
	}
}

func TestLocalMerger_CanMerge_WithConflicts(t *testing.T) {
	repo := setupMergeRepo(t)
	addConflictingBranches(t, repo, "feature/check-conflict")

	merger := nxdgit.NewLocalMerger(repo)
	canMerge, conflicts, err := merger.CanMerge("feature/check-conflict", "main")
	if err != nil {
		t.Fatalf("can merge: %v", err)
	}
	if canMerge {
		t.Fatal("expected merge to be blocked by conflicts")
	}
	if len(conflicts) == 0 {
		t.Fatal("expected conflict file names")
	}

	found := false
	for _, f := range conflicts {
		if f == "README" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'README' in conflicts, got %v", conflicts)
	}
}

func TestLocalMerger_MergedSHA_AfterMerge(t *testing.T) {
	repo := setupMergeRepo(t)
	addFeatureBranch(t, repo, "feature/sha-test", "sha.txt", "sha content")

	merger := nxdgit.NewLocalMerger(repo)
	result, err := merger.Merge("feature/sha-test", "main")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	sha, err := merger.MergedSHA()
	if err != nil {
		t.Fatalf("merged SHA: %v", err)
	}

	if sha == "" {
		t.Fatal("expected non-empty SHA")
	}
	if len(sha) != 40 {
		t.Fatalf("expected 40-char SHA, got %d chars: %s", len(sha), sha)
	}
	if sha != result.MergedSHA {
		t.Fatalf("SHA mismatch: MergedSHA()=%s, result.MergedSHA=%s", sha, result.MergedSHA)
	}
}
