package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestConflictedFiles_NonASCIIPath verifies that a conflict on a filename with a
// space and a non-ASCII character is returned as its real path — not git's
// default core.quotepath-escaped form (e.g. `"r\303\251sum\303\251 a.txt"`).
// Without the fix, the escaped string flows into SniffBinary/StageFiles and the
// Tech-Lead conflict resolver silently fails.
func TestConflictedFiles_NonASCIIPath(t *testing.T) {
	dir := t.TempDir()
	helperRun(t, dir, "git", "init")
	helperRun(t, dir, "git", "config", "user.email", "test@test.com")
	helperRun(t, dir, "git", "config", "user.name", "Test")
	// Intentionally leave core.quotepath at its default (true).

	const name = "résumé draft.txt"
	write := func(content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	write("initial\n")
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "init")
	mainBranch := helperRun(t, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")

	helperRun(t, dir, "git", "checkout", "-b", "topic")
	write("topic change\n")
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "topic")

	helperRun(t, dir, "git", "checkout", mainBranch)
	write("main change\n")
	helperRun(t, dir, "git", "add", ".")
	helperRun(t, dir, "git", "commit", "-m", "main")

	helperRun(t, dir, "git", "checkout", "topic")
	// Trigger the conflict (rebase exits non-zero — that's expected).
	rebase := exec.Command("git", "rebase", mainBranch)
	rebase.Dir = dir
	_, _ = rebase.CombinedOutput()

	files, err := ConflictedFiles(dir)
	if err != nil {
		t.Fatalf("ConflictedFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 conflicted file, got %d: %#v", len(files), files)
	}
	if files[0] != name {
		t.Errorf("ConflictedFiles returned %q, want the real path %q", files[0], name)
	}
	// The returned path must resolve to a real file on disk.
	if _, statErr := os.Stat(filepath.Join(dir, files[0])); statErr != nil {
		t.Errorf("returned path does not resolve to a file: %v", statErr)
	}
}
