package cli

import (
	"errors"
	"fmt"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

// errAlreadyClean reports that a story's worktree and branch were already
// gone: nothing to remove, which is the normal case for a merged story (the
// monitor cleans up right after the merge) and not a failure.
var errAlreadyClean = errors.New("worktree and branch already removed")

// removeBranchAndWorktree deletes a branch, removing the worktree that has it
// checked out first: git refuses `branch -D` for a branch checked out
// anywhere, and the monitor can crash before it removes a story's worktree.
// It is the one rule for that pair — `nxd archive` and the gc reaper both
// take this path — and it returns the worktree it removed, because
// `worktree remove --force` discards uncommitted edits there and a caller
// should be able to say so. Nothing to remove is errAlreadyClean, not a
// failure; a git lookup that fails is an error, never "no worktree", because
// callers delete on that answer.
func removeBranchAndWorktree(repoDir, branch string) (removed string, err error) {
	worktreePath, err := nxdgit.WorktreeForBranch(repoDir, branch)
	if err != nil {
		return "", fmt.Errorf("locate worktree for %s in %s: %w", branch, repoDir, err)
	}
	if worktreePath == "" && !nxdgit.BranchExists(repoDir, branch) {
		return "", errAlreadyClean
	}
	var errs []error
	if worktreePath != "" {
		if derr := nxdgit.DeleteWorktree(repoDir, worktreePath); derr != nil {
			errs = append(errs, fmt.Errorf("remove worktree %s: %w", worktreePath, derr))
		} else {
			removed = worktreePath
		}
	}
	if derr := nxdgit.DeleteBranch(repoDir, branch); derr != nil {
		errs = append(errs, fmt.Errorf("delete branch %s: %w", branch, derr))
	}
	return removed, errors.Join(errs...)
}
