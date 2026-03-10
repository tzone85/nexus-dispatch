package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CreateWorktree creates a new git worktree at worktreePath on a new branch.
// It is idempotent: if a valid worktree already exists at the path, it is
// reused. If a broken worktree or stale branch exists from a previous failed
// run, they are cleaned up before creating a fresh worktree.
func CreateWorktree(repoDir, worktreePath, branch string) error {
	// If the directory exists, check if it's a usable worktree
	if fi, err := os.Stat(worktreePath); err == nil && fi.IsDir() {
		check := exec.Command("git", "rev-parse", "--is-inside-work-tree")
		check.Dir = worktreePath
		if out, err := check.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
			return nil // valid worktree — reuse it
		}
		// Broken or empty worktree — remove it
		os.RemoveAll(worktreePath)
	}

	// Prune stale worktree references from .git/worktrees
	prune := exec.Command("git", "worktree", "prune")
	prune.Dir = repoDir
	prune.Run()

	// Delete the branch if it lingers from a previous failed attempt
	delBranch := exec.Command("git", "branch", "-D", branch)
	delBranch.Dir = repoDir
	delBranch.Run()

	// Create fresh worktree with new branch
	cmd := exec.Command("git", "worktree", "add", "-b", branch, worktreePath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteWorktree forcefully removes a worktree at the given path.
func DeleteWorktree(repoDir, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListWorktrees returns the absolute paths of all worktrees in the repo.
func ListWorktrees(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}
