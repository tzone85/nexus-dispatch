package repolearn

import (
	"os/exec"
	"strings"
	"testing"
)

// initBranchTestRepo creates a tempdir git repo with the requested
// branches so detectBranchPattern has real data to scan. Returns the
// repo path.
func initBranchTestRepo(t *testing.T, branches []string) string {
	t.Helper()
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
	for _, b := range branches {
		run("branch", b)
	}
	return dir
}

// TestDetectBranchPattern_FeaturePrefix covers the recognized-prefix
// path: when multiple branches share a feature/* or fix/* prefix,
// detectBranchPattern returns those prefixes joined. Without the
// test, scanning logic for git output stays at 33% coverage.
func TestDetectBranchPattern_FeaturePrefix(t *testing.T) {
	dir := initBranchTestRepo(t, []string{
		"feature/a", "feature/b", "fix/c", "fix/d",
	})
	got := detectBranchPattern(dir)
	if !strings.Contains(got, "feature/*") {
		t.Errorf("expected feature/* in pattern; got %q", got)
	}
	if !strings.Contains(got, "fix/*") {
		t.Errorf("expected fix/* in pattern; got %q", got)
	}
}

// TestDetectBranchPattern_FreeformWhenNoPrefixes covers the fallback:
// many branches but no recognizable prefix groups → "freeform".
func TestDetectBranchPattern_FreeformWhenNoPrefixes(t *testing.T) {
	dir := initBranchTestRepo(t, []string{"foo", "bar", "baz", "qux"})
	got := detectBranchPattern(dir)
	// With no `/`-separated prefixes, prefixCounts stays empty;
	// detectBranchPattern returns "freeform".
	if got != "freeform" {
		t.Errorf("expected 'freeform' for prefix-less branches; got %q", got)
	}
}

// TestDetectBranchPattern_MainOnly covers the single-branch case
// (no other branches exist) → "main-only".
func TestDetectBranchPattern_MainOnly(t *testing.T) {
	dir := initBranchTestRepo(t, nil) // just main
	got := detectBranchPattern(dir)
	if got != "main-only" {
		t.Errorf("expected 'main-only' for single-branch repo; got %q", got)
	}
}

// TestDetectBranchPattern_NonGitDirReturnsEmpty covers the error
// fallback (both -r and local branch listing fail).
func TestDetectBranchPattern_NonGitDirReturnsEmpty(t *testing.T) {
	got := detectBranchPattern(t.TempDir())
	if got != "" {
		t.Errorf("expected empty string for non-git dir; got %q", got)
	}
}
