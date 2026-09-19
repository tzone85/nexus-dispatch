package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// WorktreeForBranch asks git which worktree of repoDir has branch checked out
// and returns its path, or "" when none does. A git failure (repoDir missing
// or not a repository) is an error, never "no worktree": callers that delete
// on that answer must not mistake a moved repo for an already-clean one. The
// executor keeps worktrees under <state_dir>/worktrees/<story-id>, not under
// the repo, so callers must not assume a path.
func WorktreeForBranch(repoDir, branch string) (string, error) {
	// -z: without it git c-quotes a path with a space, a quote or a non-ASCII
	// byte, and the quoted string would not match the worktree on disk (the
	// same fix ConflictedFiles already carries for status --porcelain). It
	// needs git >= 2.36; on an older git the switch is unknown, and failing
	// closed there would break gc, archive and review on, say, Ubuntu 20.04.
	// The newline form is the fallback: c-quoting is a limitation of that
	// form, not a reason to refuse to run.
	out, err := worktreeList(repoDir, "-z")
	if err == nil {
		return ParseWorktreeForBranch(out, branch, true), nil
	}
	if !rejectsZ(err) {
		// A missing directory, no repository, no permission: a real failure.
		// Retrying it costs a second git call and would report the second
		// error, which says the same thing about a different command line.
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	out, err = worktreeList(repoDir)
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	return ParseWorktreeForBranch(out, branch, false), nil
}

// rejectsZ reports the one failure the newline fallback exists for: a git
// older than 2.36 does not know `worktree list --porcelain -z` and answers
// "error: unknown switch `z'" (or "unknown option" on some builds) with exit
// 129. Every other failure is a real one.
func rejectsZ(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown switch") || strings.Contains(msg, "unknown option")
}

// worktreeList runs `git worktree list --porcelain` with extra args and
// returns its output, or an error carrying git's message.
func worktreeList(repoDir string, extra ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"worktree", "list", "--porcelain"}, extra...)...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ParseWorktreeForBranch parses `git worktree list --porcelain` output and
// returns the path of the worktree that has branch checked out, or "". The
// two forms have the same shape — one attribute per record, an empty one
// between records — and differ only in the terminator, so nulSeparated says
// which one this is. Splitting on both would undo the point of -z: a
// worktree path containing a newline is exactly what -z is there to carry,
// and treating \n as a separator would cut it in half again.
func ParseWorktreeForBranch(porcelain, branch string, nulSeparated bool) string {
	sep := "\n"
	if nulSeparated {
		sep = "\x00"
	}
	want := "branch refs/heads/" + branch
	current := ""
	for _, line := range strings.Split(porcelain, sep) {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = strings.TrimPrefix(line, "worktree ")
		case line == want && current != "":
			return current
		}
	}
	return ""
}
