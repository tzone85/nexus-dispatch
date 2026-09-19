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

// BranchExists reports whether a local branch (refs/heads/<name>) exists.
func BranchExists(repoDir, name string) bool {
	// refs/heads only: rev-parse --verify would also match a tag or a remote
	// ref of the same name, and the canonical fallback feeds branch -D.
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}
