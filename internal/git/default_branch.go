package git

import (
	"os/exec"
	"strings"
)

// DetectDefaultBranch returns the repository's real default branch instead of
// assuming "main". Repositories created before ~2020 (and many still today)
// use "master"; a hardcoded "main" makes every merge fail with
// "couldn't find remote ref main" on those repos, which silently breaks the
// whole pipeline on older codebases.
//
// Resolution order:
//  1. origin/HEAD symbolic ref — the remote's declared default branch.
//  2. A local branch named main, then master.
//  3. The currently checked-out branch.
//  4. "main" as a last resort so callers always get a non-empty value.
func DetectDefaultBranch(repoDir string) string {
	run := func(args ...string) (string, bool) {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}

	// 1. What does origin say its default branch is?
	if ref, ok := run("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok && ref != "" {
		// ref looks like "origin/master" — strip the remote prefix.
		if _, after, found := strings.Cut(ref, "/"); found {
			return after
		}
		return ref
	}

	// 2. Local branch by convention.
	for _, name := range []string{"main", "master"} {
		if _, ok := run("show-ref", "--verify", "--quiet", "refs/heads/"+name); ok {
			return name
		}
	}

	// 3. Whatever is checked out.
	if cur, ok := run("rev-parse", "--abbrev-ref", "HEAD"); ok && cur != "" && cur != "HEAD" {
		return cur
	}

	// 4. Last resort.
	return "main"
}
