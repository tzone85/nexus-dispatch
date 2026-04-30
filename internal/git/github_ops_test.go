package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergePR_FakeGH tests MergePR using a fake gh script.
func TestMergePR_FakeGH(t *testing.T) {
	fakeDir := t.TempDir()
	fakeScript := "#!/bin/sh\nexit 0\n"
	scriptPath := filepath.Join(fakeDir, "gh")
	if err := os.WriteFile(scriptPath, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	err := MergePR(t.TempDir(), 42)
	if err != nil {
		t.Fatalf("MergePR with fake gh should succeed, got: %v", err)
	}
}

// TestMergePR_Error tests MergePR when gh fails.
func TestMergePR_Error(t *testing.T) {
	fakeDir := t.TempDir()
	fakeScript := "#!/bin/sh\necho 'merge failed: not mergeable' >&2\nexit 1\n"
	scriptPath := filepath.Join(fakeDir, "gh")
	if err := os.WriteFile(scriptPath, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	err := MergePR(t.TempDir(), 42)
	if err == nil {
		t.Fatal("MergePR should fail when gh fails")
	}
	if !strings.Contains(err.Error(), "gh pr merge") {
		t.Errorf("error should mention 'gh pr merge', got: %v", err)
	}
}

// TestGetPRStatus_FakeGH tests GetPRStatus with a fake gh that returns JSON.
func TestGetPRStatus_FakeGH(t *testing.T) {
	fakeDir := t.TempDir()
	fakeScript := `#!/bin/sh
printf '{"number":99,"url":"https://github.com/o/r/pull/99","state":"OPEN","title":"test pr"}\n'
`
	scriptPath := filepath.Join(fakeDir, "gh")
	if err := os.WriteFile(scriptPath, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	info, err := GetPRStatus(t.TempDir(), 99)
	if err != nil {
		t.Fatalf("GetPRStatus: %v", err)
	}
	if info.Number != 99 {
		t.Errorf("Number = %d; want 99", info.Number)
	}
	if info.State != "OPEN" {
		t.Errorf("State = %q; want OPEN", info.State)
	}
	if info.Title != "test pr" {
		t.Errorf("Title = %q; want 'test pr'", info.Title)
	}
	if info.URL != "https://github.com/o/r/pull/99" {
		t.Errorf("URL = %q; want correct URL", info.URL)
	}
}

// TestGetPRStatus_Error tests GetPRStatus when gh fails.
func TestGetPRStatus_Error(t *testing.T) {
	fakeDir := t.TempDir()
	fakeScript := "#!/bin/sh\necho 'not found' >&2\nexit 1\n"
	scriptPath := filepath.Join(fakeDir, "gh")
	if err := os.WriteFile(scriptPath, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	_, err := GetPRStatus(t.TempDir(), 999)
	if err == nil {
		t.Fatal("GetPRStatus should fail when gh fails")
	}
}

// TestGetPRStatus_InvalidJSON tests GetPRStatus with malformed JSON.
func TestGetPRStatus_InvalidJSON(t *testing.T) {
	fakeDir := t.TempDir()
	fakeScript := "#!/bin/sh\necho 'not json'\n"
	scriptPath := filepath.Join(fakeDir, "gh")
	if err := os.WriteFile(scriptPath, []byte(fakeScript), 0755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	_, err := GetPRStatus(t.TempDir(), 1)
	if err == nil {
		t.Fatal("GetPRStatus should fail on invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse pr info") {
		t.Errorf("error should mention 'parse pr info', got: %v", err)
	}
}

// TestPushBranch_NoRemote verifies PushBranch is a no-op for local-only
// repos (LB10 fix — companion to LB7). Previously failed with "fatal:
// 'origin' does not appear to be a git repository".
func TestPushBranch_NoRemote(t *testing.T) {
	dir := helperInitRepo(t)
	if err := PushBranch(dir, "main"); err != nil {
		t.Fatalf("PushBranch should be a no-op for local-only repos, got: %v", err)
	}
}

// TestPushBranch_WithRemote tests PushBranch with a local bare remote.
func TestPushBranch_WithRemote(t *testing.T) {
	// Create a bare "remote".
	remote := t.TempDir()
	helperRun(t, remote, "git", "init", "--bare")

	// Create a repo that tracks the remote.
	dir := t.TempDir()
	helperRun(t, dir, "git", "clone", remote, ".")
	helperRun(t, dir, "git", "config", "user.email", "test@test.com")
	helperRun(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "init")

	branch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	err := PushBranch(dir, branch)
	if err != nil {
		t.Fatalf("PushBranch should succeed with remote, got: %v", err)
	}
}

// TestDeleteRemoteBranch_NoRemote verifies DeleteRemoteBranch is a no-op
// for local-only repos (LB10 fix — companion to PushBranch + FetchBranch).
func TestDeleteRemoteBranch_NoRemote(t *testing.T) {
	dir := helperInitRepo(t)
	if err := DeleteRemoteBranch(dir, "some-branch"); err != nil {
		t.Fatalf("DeleteRemoteBranch should be a no-op for local-only repos, got: %v", err)
	}
}

// TestDeleteRemoteBranch_WithRemote tests DeleteRemoteBranch with a local bare remote.
func TestDeleteRemoteBranch_WithRemote(t *testing.T) {
	remote := t.TempDir()
	helperRun(t, remote, "git", "init", "--bare")

	dir := t.TempDir()
	helperRun(t, dir, "git", "clone", remote, ".")
	helperRun(t, dir, "git", "config", "user.email", "test@test.com")
	helperRun(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "init")
	helperRun(t, dir, "git", "push", "origin", "HEAD")

	// Create and push a branch.
	helperRun(t, dir, "git", "checkout", "-b", "feature/delete-me")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "feature commit")
	helperRun(t, dir, "git", "push", "origin", "feature/delete-me")

	// Switch to detached HEAD so we're not on the branch we want to delete.
	helperRun(t, dir, "git", "checkout", "--detach")

	err := DeleteRemoteBranch(dir, "feature/delete-me")
	if err != nil {
		t.Fatalf("DeleteRemoteBranch should succeed, got: %v", err)
	}

	// Verify branch no longer exists on remote.
	cmd := exec.Command("git", "ls-remote", "--heads", "origin", "feature/delete-me")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "feature/delete-me") {
		t.Error("remote branch should be deleted")
	}
}

// TestRemoveWorktree tests the full worktree removal flow.
func TestRemoveWorktree_Success(t *testing.T) {
	dir := helperInitRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt-remove")

	// Create a worktree.
	helperRun(t, dir, "git", "worktree", "add", "-b", "wt-branch", wtPath)

	err := RemoveWorktree(dir, wtPath, "wt-branch")
	if err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	// Verify worktree directory is gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed")
	}

	// Verify branch is deleted.
	cmd := exec.Command("git", "branch", "--list", "wt-branch")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "wt-branch") {
		t.Error("branch should be deleted after RemoveWorktree")
	}
}

// TestRemoveWorktree_InvalidPath tests RemoveWorktree with a non-existent worktree.
func TestRemoveWorktree_InvalidPath(t *testing.T) {
	dir := helperInitRepo(t)

	err := RemoveWorktree(dir, "/nonexistent/worktree", "fake-branch")
	if err == nil {
		t.Fatal("RemoveWorktree should fail for non-existent worktree")
	}
}

// TestFetchBranch_NoRemote verifies FetchBranch is a no-op for local-only
// repos (LB7 fix). Previously this test asserted an error was returned;
// after the live-test fix, FetchBranch returns nil so callers (rebaseAndMerge)
// can fall back to the local base branch instead of failing.
func TestFetchBranch_NoRemote(t *testing.T) {
	dir := helperInitRepo(t)

	if err := FetchBranch(dir, "main"); err != nil {
		t.Fatalf("FetchBranch should be a no-op for local-only repos, got: %v", err)
	}
}

// TestHasRemote covers the helper added alongside FetchBranch (LB7).
func TestHasRemote(t *testing.T) {
	dir := helperInitRepo(t)
	if HasRemote(dir, "origin") {
		t.Error("expected no origin in fresh local repo")
	}
	helperRun(t, dir, "git", "remote", "add", "origin", "/tmp/fake-remote")
	if !HasRemote(dir, "origin") {
		t.Error("expected origin to be detected after add")
	}
}

// TestFetchBranch_WithRemote tests FetchBranch with a local bare remote.
func TestFetchBranch_WithRemote(t *testing.T) {
	remote := t.TempDir()
	helperRun(t, remote, "git", "init", "--bare")

	dir := t.TempDir()
	helperRun(t, dir, "git", "clone", remote, ".")
	helperRun(t, dir, "git", "config", "user.email", "test@test.com")
	helperRun(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "init")
	helperRun(t, dir, "git", "push", "origin", "HEAD")

	branch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	err := FetchBranch(dir, branch)
	if err != nil {
		t.Fatalf("FetchBranch should succeed with valid remote, got: %v", err)
	}
}

// TestRebaseOnto_NoConflict tests RebaseOnto with a clean rebase.
func TestRebaseOnto_NoConflict(t *testing.T) {
	dir := helperInitRepo(t)
	mainBranch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	// Create topic with non-conflicting change.
	helperRun(t, dir, "git", "checkout", "-b", "topic-rebase")
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("extra\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic")

	// Add non-conflicting change on main.
	helperRun(t, dir, "git", "checkout", mainBranch)
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "main advance")

	helperRun(t, dir, "git", "checkout", "topic-rebase")

	err := RebaseOnto(dir, mainBranch)
	if err != nil {
		t.Fatalf("RebaseOnto should succeed for clean rebase, got: %v", err)
	}
}

// TestRebaseOnto_WithConflict tests RebaseOnto with conflicting changes.
func TestRebaseOnto_WithConflict(t *testing.T) {
	dir, mainBranch, _ := setupConflict(t)

	err := RebaseOnto(dir, mainBranch)
	if err == nil {
		t.Fatal("RebaseOnto should fail with conflicts")
	}
	if !strings.Contains(err.Error(), "git rebase") {
		t.Errorf("error should mention 'git rebase', got: %v", err)
	}

	// Verify the rebase was aborted (worktree should be clean).
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "UU") {
		t.Error("rebase should be aborted after RebaseOnto failure")
	}
}

// TestRebaseOnto_InvalidDir tests RebaseOnto with invalid directory.
func TestRebaseOnto_InvalidDir(t *testing.T) {
	err := RebaseOnto("/nonexistent/path", "main")
	if err == nil {
		t.Fatal("RebaseOnto should fail with invalid directory")
	}
}
