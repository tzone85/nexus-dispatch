package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MergeResult holds the outcome of a local git merge.
type MergeResult struct {
	Branch     string
	BaseBranch string
	MergedSHA  string
	Conflicts  []string
}

// LocalMerger performs git merge operations locally without network access.
type LocalMerger struct {
	repoDir string
}

// NewLocalMerger creates a LocalMerger for the given repository directory.
func NewLocalMerger(repoDir string) *LocalMerger {
	return &LocalMerger{repoDir: repoDir}
}

// Merge merges a feature branch into the base branch locally.
// It checks out the base branch, merges the feature branch (--no-ff for clean
// history), and returns the result. If conflicts occur, it aborts the merge and
// returns them in the result.
func (m *LocalMerger) Merge(featureBranch, baseBranch string) (MergeResult, error) {
	result := MergeResult{
		Branch:     featureBranch,
		BaseBranch: baseBranch,
	}
	m.cleanupUntrackedGoBuildArtifacts()
	defer m.cleanupUntrackedGoBuildArtifacts()

	// Checkout base branch
	if err := m.runGit("checkout", baseBranch); err != nil {
		return result, fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	// Attempt merge with --no-ff for explicit merge commit
	mergeMsg := fmt.Sprintf("Merge %s into %s", featureBranch, baseBranch)
	err := m.runGit("merge", "--no-ff", featureBranch, "-m", mergeMsg)
	if err != nil {
		// Merge failed — check for conflicts
		conflicts, conflictErr := m.listConflicts()
		if conflictErr != nil {
			// Cannot determine conflicts; abort and report original error
			_ = m.runGit("merge", "--abort")
			return result, fmt.Errorf("merge %s into %s: %w", featureBranch, baseBranch, err)
		}

		if len(conflicts) > 0 {
			result.Conflicts = conflicts
			_ = m.runGit("merge", "--abort")
			return result, fmt.Errorf("merge conflicts in %d file(s): %s", len(conflicts), strings.Join(conflicts, ", "))
		}

		// No conflicts detected but merge still failed
		_ = m.runGit("merge", "--abort")
		return result, fmt.Errorf("merge %s into %s: %w", featureBranch, baseBranch, err)
	}

	sha, err := m.MergedSHA()
	if err != nil {
		return result, fmt.Errorf("read merged SHA: %w", err)
	}
	result.MergedSHA = sha

	return result, nil
}

// CanMerge checks if a branch can be merged cleanly (dry run).
// It uses git merge --no-commit --no-ff, then aborts regardless of outcome.
func (m *LocalMerger) CanMerge(featureBranch, baseBranch string) (bool, []string, error) {
	m.cleanupUntrackedGoBuildArtifacts()
	defer m.cleanupUntrackedGoBuildArtifacts()

	// Checkout base branch
	if err := m.runGit("checkout", baseBranch); err != nil {
		return false, nil, fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	// Attempt a dry-run merge (no commit)
	err := m.runGit("merge", "--no-commit", "--no-ff", featureBranch)
	if err != nil {
		// Merge had problems — check for conflicts
		conflicts, conflictErr := m.listConflicts()
		if conflictErr != nil {
			_ = m.runGit("merge", "--abort")
			return false, nil, fmt.Errorf("check merge feasibility: %w", err)
		}

		_ = m.runGit("merge", "--abort")
		return false, conflicts, nil
	}

	// Clean merge — abort to restore original state
	_ = m.runGit("merge", "--abort")
	return true, nil, nil
}

// MergedSHA returns the current HEAD SHA.
func (m *LocalMerger) MergedSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = m.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// listConflicts returns the names of files with unresolved merge conflicts.
func (m *LocalMerger) listConflicts() ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = m.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --diff-filter=U: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	var files []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

// runGit runs a git command in the repository directory and returns any error.
func (m *LocalMerger) runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *LocalMerger) cleanupUntrackedGoBuildArtifacts() {
	for _, path := range m.knownGoBuildArtifactPaths() {
		if !m.isUntracked(path) {
			continue
		}
		_ = os.Remove(filepath.Join(m.repoDir, path))
	}
}

func (m *LocalMerger) knownGoBuildArtifactPaths() []string {
	paths := []string{filepath.Base(m.repoDir)}
	if data, err := os.ReadFile(filepath.Join(m.repoDir, "go.mod")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "module" {
				paths = append(paths, filepath.Base(fields[1]))
				break
			}
		}
	}
	return paths
}

func (m *LocalMerger) isUntracked(path string) bool {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", path)
	cmd.Dir = m.repoDir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "?? ") && strings.TrimSpace(strings.TrimPrefix(line, "?? ")) == path {
			return true
		}
	}
	return false
}
