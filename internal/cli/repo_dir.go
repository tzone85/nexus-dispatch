package cli

import (
	"fmt"
	"os"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// resolveRepoDir is the one rule for which repo gc, archive, merge and review
// run git in: the requirement's recorded repo, or — for a requirement submitted without
// one — the current directory, resolved to an absolute path so the choice is
// visible in every message that prints it.
func resolveRepoDir(req state.Requirement) (string, error) {
	if req.RepoPath != "" {
		return req.RepoPath, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("requirement %s has no repo path and the working directory is unknown: %w", req.ID, err)
	}
	return cwd, nil
}

// cwdFallbackNotes is the one line gc, archive, merge and review print when a requirement
// has no repo path and dir is the current directory, so the fallback
// resolveRepoDir made is visible in the output.
func cwdFallbackNotes(req state.Requirement, dir string) []string {
	if req.RepoPath != "" {
		return nil
	}
	return []string{fmt.Sprintf("  note: requirement %s has no repo path; using the current directory %s", req.ID, dir)}
}
