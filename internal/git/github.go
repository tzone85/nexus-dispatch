package git

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PRInfo holds metadata about a GitHub pull request.
type PRInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Title  string `json:"title"`
}

// CreatePR opens a new pull request from the current branch to baseBranch
// using the gh CLI.
func CreatePR(repoDir, title, body, baseBranch, headBranch string) (PRInfo, error) {
	cmd := exec.Command("gh", "pr", "create",
		"--title", title,
		"--body", body,
		"--base", baseBranch,
		"--head", headBranch,
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr create: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	url := strings.TrimSpace(string(out))
	return PRInfo{URL: url}, nil
}

// MergePR squash-merges the given PR number and deletes the source branch.
func MergePR(repoDir string, prNumber int) error {
	cmd := exec.Command("gh", "pr", "merge",
		fmt.Sprintf("%d", prNumber),
		"--squash",
		"--delete-branch",
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr merge: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GetPRStatus retrieves metadata for the given PR number.
func GetPRStatus(repoDir string, prNumber int) (PRInfo, error) {
	cmd := exec.Command("gh", "pr", "view",
		fmt.Sprintf("%d", prNumber),
		"--json", "number,url,state,title",
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var info PRInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return PRInfo{}, fmt.Errorf("parse pr info: %w", err)
	}
	return info, nil
}

// PushBranch pushes the named branch to origin and sets up tracking.
func PushBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "push", "-u", "origin", branch)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GHAvailable reports whether the gh CLI binary is on PATH.
func GHAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}
