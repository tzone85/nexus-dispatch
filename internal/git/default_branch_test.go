package git_test

import (
	"os/exec"
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

func gitInit(t *testing.T, dir string, defaultBranch string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", defaultBranch)
	run("commit", "-q", "--allow-empty", "-m", "init")
}

func TestDetectDefaultBranch_Master(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, "master")
	if got := nxdgit.DetectDefaultBranch(dir); got != "master" {
		t.Fatalf("want master, got %q", got)
	}
}

func TestDetectDefaultBranch_Main(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, "main")
	if got := nxdgit.DetectDefaultBranch(dir); got != "main" {
		t.Fatalf("want main, got %q", got)
	}
}

func TestDetectDefaultBranch_CustomTrunk(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, "trunk")
	// No main/master exist; the checked-out branch is the answer.
	if got := nxdgit.DetectDefaultBranch(dir); got != "trunk" {
		t.Fatalf("want trunk, got %q", got)
	}
}
