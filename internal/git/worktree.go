package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// CreateWorktree creates a new git worktree at worktreePath on a new branch.
func CreateWorktree(repoDir, worktreePath, branch string) error {
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
