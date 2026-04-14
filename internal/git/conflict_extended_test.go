package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartRebase_NoConflict(t *testing.T) {
	dir := helperInitRepo(t)
	mainBranch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	// Create a non-conflicting branch.
	helperRun(t, dir, "git", "checkout", "-b", "topic-clean")
	if err := os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("new content\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic commit")

	// Add a non-conflicting change on main.
	helperRun(t, dir, "git", "checkout", mainBranch)
	if err := os.WriteFile(filepath.Join(dir, "another.txt"), []byte("another\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "main commit")

	helperRun(t, dir, "git", "checkout", "topic-clean")

	err := StartRebase(dir, mainBranch)
	if err != nil {
		t.Fatalf("StartRebase should succeed with no conflicts, got: %v", err)
	}
}

func TestStartRebase_WithConflict(t *testing.T) {
	dir, mainBranch, _ := setupConflict(t)

	err := StartRebase(dir, mainBranch)
	if err == nil {
		t.Fatal("StartRebase should return an error on conflict")
	}

	if !IsConflict(err) {
		t.Fatalf("expected ConflictError, got: %v", err)
	}

	ce := err.(*ConflictError)
	if ce.Output == "" {
		t.Error("ConflictError.Output should not be empty")
	}
}

func TestStartRebase_InvalidDir(t *testing.T) {
	err := StartRebase("/nonexistent/path", "main")
	if err == nil {
		t.Fatal("StartRebase should fail with invalid directory")
	}
	// Should not be a conflict error.
	if IsConflict(err) {
		t.Error("error for invalid dir should not be a ConflictError")
	}
}

func TestConflictedFiles_WithConflict(t *testing.T) {
	dir, mainBranch, _ := setupConflict(t)

	// Start a rebase that will conflict (leave it in progress).
	err := StartRebase(dir, mainBranch)
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got: %v", err)
	}

	files, err := ConflictedFiles(dir)
	if err != nil {
		t.Fatalf("ConflictedFiles: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("expected at least one conflicted file")
	}

	found := false
	for _, f := range files {
		if f == "file.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'file.txt' in conflicted files, got: %v", files)
	}

	// Clean up rebase state.
	RebaseAbort(dir)
}

func TestConflictedFiles_NoConflict(t *testing.T) {
	dir := helperInitRepo(t)

	files, err := ConflictedFiles(dir)
	if err != nil {
		t.Fatalf("ConflictedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no conflicted files in clean repo, got: %v", files)
	}
}

func TestConflictedFiles_InvalidDir(t *testing.T) {
	_, err := ConflictedFiles("/nonexistent/path")
	if err == nil {
		t.Fatal("ConflictedFiles should fail with invalid directory")
	}
}

func TestStageFiles(t *testing.T) {
	dir := helperInitRepo(t)

	// Create an unstaged file.
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("content\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := StageFiles(dir, []string{"staged.txt"})
	if err != nil {
		t.Fatalf("StageFiles: %v", err)
	}

	// Verify it is staged.
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "staged.txt") {
		t.Errorf("expected staged.txt in staged files, got: %s", out)
	}
}

func TestStageFiles_InvalidFile(t *testing.T) {
	dir := helperInitRepo(t)
	err := StageFiles(dir, []string{"nonexistent-file-xyz.txt"})
	if err == nil {
		t.Fatal("StageFiles should fail for nonexistent file")
	}
}

func TestRebaseContinue_NoRebaseInProgress(t *testing.T) {
	dir := helperInitRepo(t)
	err := RebaseContinue(dir)
	if err == nil {
		t.Fatal("RebaseContinue should fail when no rebase is in progress")
	}
	// Should not be a conflict error.
	if IsConflict(err) {
		t.Error("error should not be a ConflictError")
	}
}

func TestRebaseContinue_AfterResolvingConflict(t *testing.T) {
	dir, mainBranch, _ := setupConflict(t)

	// Start rebase -- will conflict.
	err := StartRebase(dir, mainBranch)
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got: %v", err)
	}

	// Resolve the conflict by choosing one side.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("resolved\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", "file.txt")

	// Continue the rebase.
	err = RebaseContinue(dir)
	if err != nil {
		t.Fatalf("RebaseContinue should succeed after resolving conflict, got: %v", err)
	}
}

func TestRebaseAbort_NoRebase(t *testing.T) {
	dir := helperInitRepo(t)
	err := RebaseAbort(dir)
	if err != nil {
		t.Fatalf("RebaseAbort should succeed even with no rebase in progress, got: %v", err)
	}
}

func TestRebaseAbort_DuringConflict(t *testing.T) {
	dir, mainBranch, _ := setupConflict(t)

	// Start rebase to get into conflict state.
	err := StartRebase(dir, mainBranch)
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got: %v", err)
	}

	// Abort should succeed and leave the worktree clean.
	err = RebaseAbort(dir)
	if err != nil {
		t.Fatalf("RebaseAbort: %v", err)
	}

	// Verify the repo is clean (no rebase in progress).
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "UU") {
		t.Error("expected no conflict markers after abort")
	}
}

func TestAbortRebase(t *testing.T) {
	dir := helperInitRepo(t)
	err := abortRebase(dir)
	if err != nil {
		t.Fatalf("abortRebase should return nil, got: %v", err)
	}
}

func TestRebaseContinue_MultipleConflicts(t *testing.T) {
	dir := helperInitRepo(t)
	mainBranch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	// Create topic branch with two conflicting commits.
	helperRun(t, dir, "git", "checkout", "-b", "multi-conflict")

	// First commit on topic.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("topic v1\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic commit 1")

	// Second commit on topic.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("topic v2\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic commit 2")

	// Conflicting change on main.
	helperRun(t, dir, "git", "checkout", mainBranch)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("main v2\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "main conflicting")

	helperRun(t, dir, "git", "checkout", "multi-conflict")

	// Start rebase -- first commit will conflict.
	err := StartRebase(dir, mainBranch)
	if !IsConflict(err) {
		t.Fatalf("expected conflict on first commit, got: %v", err)
	}

	// Resolve first conflict.
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("resolved v1\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", "file.txt")

	// Continue -- second commit may also conflict.
	err = RebaseContinue(dir)
	if err != nil {
		if IsConflict(err) {
			// Second commit conflicted too -- resolve and continue.
			if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("resolved v2\n"), 0644); err != nil {
				t.Fatalf("write: %v", err)
			}
			helperRun(t, dir, "git", "add", "file.txt")
			err = RebaseContinue(dir)
			if err != nil {
				t.Fatalf("RebaseContinue should succeed after resolving second conflict: %v", err)
			}
		} else {
			t.Fatalf("unexpected error during continue: %v", err)
		}
	}
}
