package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// CreateBranch creates a new branch at the current HEAD without switching to it.
func CreateBranch(repoDir, name string) error {
	cmd := exec.Command("git", "branch", name)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteBranch force-deletes the named branch.
func DeleteBranch(repoDir, name string) error {
	cmd := exec.Command("git", "branch", "-D", name)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch -D: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CurrentBranch returns the name of the currently checked-out branch.
func CurrentBranch(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// BranchExists returns true when the named branch (or ref) exists.
func BranchExists(repoDir, name string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", name)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}
