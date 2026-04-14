package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helperInitRepo creates a temporary git repo with one commit and returns its path.
func helperInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	helperRun(t, dir, "git", "init")
	helperRun(t, dir, "git", "config", "user.email", "test@test.com")
	helperRun(t, dir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("initial\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "init")
	return dir
}

func helperRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v (%s)", name, strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// setupConflict creates a repo with two branches that have conflicting changes,
// checks out the topic branch, and returns (repoDir, mainBranch, topicBranch).
func setupConflict(t *testing.T) (string, string, string) {
	t.Helper()
	dir := helperInitRepo(t)

	// Determine the default branch name (main or master).
	mainBranch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	// Create a conflicting change on a topic branch.
	helperRun(t, dir, "git", "checkout", "-b", "topic")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("topic change\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic commit")

	// Create a conflicting change on main.
	helperRun(t, dir, "git", "checkout", mainBranch)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("main change\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "main commit")

	// Switch back to topic so StartRebase will rebase topic onto main.
	helperRun(t, dir, "git", "checkout", "topic")

	return dir, mainBranch, "topic"
}
