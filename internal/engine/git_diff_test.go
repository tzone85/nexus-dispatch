package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// initDevelopRepo builds a repo whose default branch is `develop` (no main,
// no master): two commits on develop, then a `feature` branch off it that
// adds feature.txt. It returns the repo dir.
func initDevelopRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q", "--initial-branch=develop")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	write("base.txt", "base\n")
	runGit("add", ".")
	runGit("commit", "-qm", "base")
	write("develop-only.txt", "on develop\n")
	runGit("add", ".")
	runGit("commit", "-qm", "second develop commit")
	runGit("checkout", "-qb", "feature")
	write("feature.txt", "feature\n")
	runGit("add", ".")
	runGit("commit", "-qm", "feature work")
	return dir
}

// TestGitDiff_UsesResolvedBaseBranch: with a `develop` default branch the old
// main/master probing found no merge-base and diffed from the ROOT commit,
// so develop's own history leaked into the story diff the reviewer saw.
func TestGitDiff_UsesResolvedBaseBranch(t *testing.T) {
	dir := initDevelopRepo(t)

	diff, err := gitDiff(dir, "develop")
	if err != nil {
		t.Fatalf("gitDiff: %v", err)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Fatalf("diff should contain the story's file, got:\n%s", diff)
	}
	if strings.Contains(diff, "develop-only.txt") {
		t.Fatalf("diff against the base branch must not include base-branch history, got:\n%s", diff)
	}
}

func TestGitDiff_EmptyBaseFallsBackToRoot(t *testing.T) {
	dir := initDevelopRepo(t)
	// Legacy behaviour when no base is known and neither main nor master
	// exists: diff from the root commit (everything since the first commit).
	diff, err := gitDiff(dir, "")
	if err != nil {
		t.Fatalf("gitDiff: %v", err)
	}
	if !strings.Contains(diff, "develop-only.txt") || !strings.Contains(diff, "feature.txt") {
		t.Fatalf("root fallback should include all history since the root commit, got:\n%s", diff)
	}
}

func TestMergeBaseCandidates(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		hasOrigin bool
		want      []string
	}{
		{"develop local", "develop", false, []string{"develop", "main", "master"}},
		{"develop with origin", "develop", true, []string{"origin/develop", "origin/main", "origin/master", "develop", "main", "master"}},
		{"main dedupes", "main", false, []string{"main", "master"}},
		{"empty base", "", true, []string{"origin/main", "origin/master", "main", "master"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeBaseCandidates(tc.base, tc.hasOrigin)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("mergeBaseCandidates(%q,%v) = %v, want %v", tc.base, tc.hasOrigin, got, tc.want)
			}
		})
	}
}

func TestCaptureStoryDiff_UsesBaseBranch(t *testing.T) {
	dir := initDevelopRepo(t)

	got := captureStoryDiff(dir, "develop", "feature")
	if !strings.Contains(got, "feature.txt") {
		t.Fatalf("stat diff against develop should mention feature.txt, got %q", got)
	}
	if strings.Contains(got, "develop-only.txt") {
		t.Fatalf("stat diff must not include base-branch history, got %q", got)
	}
	// The old hard-coded main does not exist in this repo → empty.
	if got := captureStoryDiff(dir, "main", "feature"); got != "" {
		t.Fatalf("expected empty stat for a missing base branch, got %q", got)
	}
}

func TestMonitor_BaseBranch(t *testing.T) {
	dir := initDevelopRepo(t)
	cfg := config.DefaultConfig()
	cfg.Merge.BaseBranch = "release"
	m := &Monitor{config: cfg}
	if got := m.baseBranch(dir); got != "release" {
		t.Fatalf("configured base must win, got %q", got)
	}
	m.config.Merge.BaseBranch = ""
	// DetectDefaultBranch falls back to the checked-out branch when neither
	// main nor master exists; a repo sitting on develop reports develop.
	co := exec.Command("git", "checkout", "-q", "develop")
	co.Dir = dir
	if out, err := co.CombinedOutput(); err != nil {
		t.Fatalf("checkout develop: %v\n%s", err, out)
	}
	if got := m.baseBranch(dir); got != "develop" {
		t.Fatalf("empty config must detect the repo default (develop), got %q", got)
	}
}
