package criteria

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestUntrackedFiles_ListsNewFiles covers the happy path: a git repo
// with an unstaged-and-untracked file produces a map with that file.
// Without this test, untrackedFiles stayed at 50% and silent
// regressions in the porcelain parsing would let the criteria
// evaluator miss files an agent forgot to add.
func TestUntrackedFiles_ListsNewFiles(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-qm", "init")

	// Plant two files: one untracked, one ignored.
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("text"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := untrackedFiles(dir)
	if _, ok := got["new.go"]; !ok {
		t.Errorf("expected new.go in untracked; got keys %v", keys(got))
	}
	if _, ok := got["other.txt"]; !ok {
		t.Errorf("expected other.txt in untracked; got keys %v", keys(got))
	}
}

// TestUntrackedFiles_NonRepoReturnsNil covers the error path —
// git status fails outside a repo. The function must return nil
// rather than crash so the criteria evaluator can fall through.
func TestUntrackedFiles_NonRepoReturnsNil(t *testing.T) {
	got := untrackedFiles(t.TempDir())
	if got != nil {
		t.Errorf("expected nil for non-git dir; got %v", got)
	}
}

// TestUntrackedFiles_EmptyRepoNoUntracked covers the clean-repo path:
// a fresh git repo with no unstaged files produces an empty map (not
// nil).
func TestUntrackedFiles_EmptyRepoNoUntracked(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		_ = c.Run()
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-qm", "init")

	got := untrackedFiles(dir)
	if got == nil {
		t.Fatal("clean repo should produce empty map, not nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for clean repo; got %v", got)
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
