package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasCommits_WithCommits(t *testing.T) {
	dir := helperInitRepo(t)

	if !HasCommits(dir) {
		t.Error("HasCommits should return true for a repo with commits")
	}
}

func TestHasCommits_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	helperRun(t, dir, "git", "init")

	if HasCommits(dir) {
		t.Error("HasCommits should return false for a repo with no commits")
	}
}

func TestHasCommits_NotARepo(t *testing.T) {
	dir := t.TempDir()

	if HasCommits(dir) {
		t.Error("HasCommits should return false for a non-git directory")
	}
}

func TestHasCommits_NonexistentDir(t *testing.T) {
	if HasCommits("/nonexistent/dir/12345") {
		t.Error("HasCommits should return false for nonexistent directory")
	}
}

func TestHasCommits_MultipleCommits(t *testing.T) {
	dir := helperInitRepo(t)

	// Add another commit.
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("second"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "second commit")

	if !HasCommits(dir) {
		t.Error("HasCommits should return true for a repo with multiple commits")
	}
}
