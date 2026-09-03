package engine

import (
	"fmt"
	"os/exec"
	"strings"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

// baseBranch returns the branch stories are diffed against and merged into:
// merge.base_branch when configured (resume.go resolves it to the repo's real
// default branch at startup), otherwise the repo's detected default. Never
// assumes "main" — repos on master/develop broke every diff and stat.
func (m *Monitor) baseBranch(repoDir string) string {
	if m.config.Merge.BaseBranch != "" {
		return m.config.Merge.BaseBranch
	}
	return nxdgit.DetectDefaultBranch(repoDir)
}

// mergeBaseCandidates lists the refs gitDiff probes for a merge-base, most
// specific first: the resolved base branch, then the conventional main /
// master fallbacks (for worktrees of repos whose base is not yet known),
// each as origin/<ref> first when a remote exists. Duplicates are removed.
func mergeBaseCandidates(baseBranch string, hasOrigin bool) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, n := range []string{baseBranch, "main", "master"} {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if !hasOrigin {
		return names
	}
	out := make([]string, 0, 2*len(names))
	for _, n := range names {
		out = append(out, "origin/"+n)
	}
	return append(out, names...)
}

// gitDiff returns the git diff for committed changes in a worktree against
// baseBranch. It tries multiple merge-base candidates (see
// mergeBaseCandidates) so it works with local-only repos that have no
// "origin/<base>", and falls back to the root commit when none match.
//
// Performance note (B1.5): probe `git remote` once to skip the origin/*
// candidates entirely on local-only repos (common after LB7). Saves
// 2 fork+exec'd git processes per pipeline pass; ScanRepo runs hundreds
// of times during a multi-story run.
func gitDiff(worktreePath, baseBranch string) (string, error) {
	// Probe whether `origin` exists before trying origin/* refs.
	hasOrigin := false
	if remoteCmd := exec.Command("git", "remote"); remoteCmd != nil {
		remoteCmd.Dir = worktreePath
		if out, err := remoteCmd.Output(); err == nil {
			hasOrigin = strings.Contains(string(out), "origin")
		}
	}

	candidates := mergeBaseCandidates(baseBranch, hasOrigin)
	var mbOut []byte
	var mbErr error
	for _, ref := range candidates {
		mbCmd := exec.Command("git", "merge-base", "HEAD", ref)
		mbCmd.Dir = worktreePath
		mbOut, mbErr = mbCmd.Output()
		if mbErr == nil {
			break
		}
	}
	if mbErr != nil {
		// No merge-base found -- fall back to the root commit of the
		// current branch so we diff all changes since the initial commit.
		rootCmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
		rootCmd.Dir = worktreePath
		rootOut, rootErr := rootCmd.Output()
		if rootErr != nil {
			return "", fmt.Errorf("git diff: cannot find merge-base or root commit: %w", rootErr)
		}
		mbOut = rootOut
	}

	mergeBase := strings.TrimSpace(string(mbOut))
	cmd := exec.Command("git", "diff", mergeBase, "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}

	// Filter out diffs that only touch .gitignore (written by
	// ensureGitignorePatterns before this check). A diff limited to
	// .gitignore means the agent produced no real code changes.
	if isGitignoreOnlyDiff(worktreePath, mergeBase) {
		return "", nil
	}

	return string(out), nil
}

// nxdArtifactPatterns are files created by NXD infrastructure, not by the
// agent's actual work.
var nxdArtifactPatterns = []string{
	".gitignore",
	"CLAUDE.md",
	".nxd-prompts/",
	".serena/",
}

func isArtifactFile(path string) bool {
	for _, pattern := range nxdArtifactPatterns {
		if path == pattern || strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

// isGitignoreOnlyDiff returns true when the only files changed between
// mergeBase and HEAD are NXD infrastructure artifacts (not real code).
func isGitignoreOnlyDiff(worktreePath, mergeBase string) bool {
	cmd := exec.Command("git", "diff", "--name-only", mergeBase, "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	files := strings.TrimSpace(string(out))
	if files == "" {
		return false
	}
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !isArtifactFile(f) {
			return false
		}
	}
	return true
}

// captureStoryDiff returns a compact --stat summary of changes between the
// base branch and the given branch. Returns an empty string on any error so
// callers can skip mining without disrupting the pipeline.
func captureStoryDiff(repoDir, baseBranch, branch string) string {
	cmd := exec.Command("git", "diff", baseBranch+"..."+branch, "--stat")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
