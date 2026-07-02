package engine

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pullBaseAfterMerge fast-forwards the local checkout to the composed base
// branch after all stories merge, so subsequent tools (the completion gate,
// the doc generator, other agents) verify the true merged tree rather than a
// stale checkout. Best-effort throughout — a failed pull is logged, never
// fatal.
func pullBaseAfterMerge(repoDir, baseBranch string) {
	if repoDir == "" {
		return
	}

	// Pre-clean NXD-only working-tree leftovers that would block ff-pull.
	// These files may be untracked (written by NXD, never committed) or
	// tracked+modified (e.g. from a prior partial run). Handle both cases:
	//   git clean -f <file>  — removes untracked files
	//   git checkout -- <f>  — discards tracked modifications (restores HEAD)
	// Both commands are best-effort; errors are intentionally ignored.
	for _, artifact := range []string{
		"WAVE_CONTEXT.md",
		"REQUIREMENT.md",
		".nxd-fix-gaps.md",
	} {
		checkoutCmd := exec.Command("git", "-C", repoDir, "checkout", "--", artifact)
		_ = checkoutCmd.Run()
		cleanCmd := exec.Command("git", "-C", repoDir, "clean", "-f", artifact)
		_ = cleanCmd.Run()
		p := filepath.Join(repoDir, artifact)
		if _, err := os.Stat(p); err == nil {
			_ = os.Remove(p) // best-effort
		}
	}

	// Ensure gitignore covers NXD artifacts for the main repo (not just worktrees).
	ensureGitignorePatterns(repoDir)

	branches := []string{baseBranch}
	if baseBranch == "" {
		branches = []string{"main", "master"}
	}
	for _, branch := range branches {
		if branch == "" {
			continue
		}
		cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+branch)
		cmd.Dir = repoDir
		if err := cmd.Run(); err == nil {
			gitPullWithStash(repoDir, branch)
			return
		}
	}
	log.Printf("[auto-resume] could not detect base branch for pull")
}

// gitPullWithStash performs a fast-forward pull of the given branch.
// If the working tree is dirty it stashes first, pulls, then pops.
// If the stash itself fails it skips the pull cleanly. Local-only repos
// (no origin remote — common offline) log the failed pull as non-fatal.
func gitPullWithStash(repoDir, branch string) {
	// The dirty check MUST run in repoDir, not the daemon's CWD.
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoDir
	statusOut, err := statusCmd.Output()
	statusText := ""
	if err == nil {
		statusText = strings.TrimSpace(string(statusOut))
	}
	dirty := statusText != ""

	if dirty {
		stash := exec.Command("git", "stash", "push", "-u", "-m", "nxd-pull-stash")
		stash.Dir = repoDir
		if stashErr := stash.Run(); stashErr != nil {
			dirtyCount := len(strings.Split(statusText, "\n"))
			log.Printf("[auto-resume] working tree dirty (%d files) — skipping %s pull; manual: cd %s && git pull --ff-only origin %s",
				dirtyCount, branch, repoDir, branch)
			return
		}
		log.Printf("[auto-resume] stashed dirty working tree before pulling %s", branch)
	}

	pull := exec.Command("git", "pull", "--ff-only", "origin", branch)
	pull.Dir = repoDir
	if out, pullErr := pull.CombinedOutput(); pullErr != nil {
		log.Printf("[auto-resume] pull %s non-fatal: %v — %s", branch, pullErr, strings.TrimSpace(string(out)))
	} else {
		log.Printf("[auto-resume] pulled latest %s into local checkout", branch)
	}

	if dirty {
		pop := exec.Command("git", "stash", "pop")
		pop.Dir = repoDir
		if popErr := pop.Run(); popErr != nil {
			log.Printf("[auto-resume] stash pop after pull: %v (manual: cd %s && git stash pop)", popErr, repoDir)
		}
	}
}
