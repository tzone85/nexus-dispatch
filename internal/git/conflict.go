package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// StartRebase begins a rebase onto the given upstream ref. If the rebase
// encounters conflicts, it returns ErrConflict (the rebase is left in
// progress so the caller can resolve and continue). On success it returns nil.
func StartRebase(worktreePath, upstream string) error {
	cmd := exec.Command("git", "rebase", upstream)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isConflict(string(out)) {
			return &ConflictError{Output: strings.TrimSpace(string(out))}
		}
		// Non-conflict failure — abort and return generic error.
		abortRebase(worktreePath)
		return fmt.Errorf("git rebase %s: %w (%s)", upstream, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ConflictedFiles returns the list of files with unresolved merge conflicts
// in the given worktree.
func ConflictedFiles(worktreePath string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only --diff-filter=U: %w", err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	var files []string
	for _, f := range strings.Split(raw, "\n") {
		f = strings.TrimSpace(f)
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// StageFiles stages the specified files in the worktree (git add).
func StageFiles(worktreePath string, files []string) error {
	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RebaseContinue continues a rebase in progress. Returns nil on success,
// a *ConflictError if the next commit also has conflicts, or a generic
// error on unexpected failure.
func RebaseContinue(worktreePath string) error {
	cmd := exec.Command("git", "-c", "core.editor=true", "rebase", "--continue")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		if isConflict(string(out)) {
			return &ConflictError{Output: strings.TrimSpace(string(out))}
		}
		return fmt.Errorf("git rebase --continue: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RebaseAbort aborts a rebase in progress, returning the worktree to a
// clean state.
func RebaseAbort(worktreePath string) error {
	return abortRebase(worktreePath)
}

// ConflictError indicates a rebase stopped due to merge conflicts.
type ConflictError struct {
	Output string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("merge conflict: %s", e.Output)
}

// IsConflict reports whether err is a *ConflictError.
func IsConflict(err error) bool {
	_, ok := err.(*ConflictError)
	return ok
}

// isConflict checks git output for conflict indicators.
func isConflict(output string) bool {
	return strings.Contains(output, "CONFLICT") ||
		strings.Contains(output, "could not apply") ||
		strings.Contains(output, "Resolve all conflicts")
}

func abortRebase(worktreePath string) error {
	cmd := exec.Command("git", "rebase", "--abort")
	cmd.Dir = worktreePath
	cmd.CombinedOutput()
	return nil
}
