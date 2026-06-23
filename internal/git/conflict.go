package git

import (
	"fmt"
	"os"
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
		_ = abortRebase(worktreePath)
		return fmt.Errorf("git rebase %s: %w (%s)", upstream, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ConflictedFiles returns the list of files with unresolved merge conflicts
// in the given worktree. It uses `git status --porcelain` which reliably
// detects all unmerged states (UU, AA, DD, AU, UA, DU, UD), unlike
// `git diff --diff-filter=U` which can miss some conflict types.
func ConflictedFiles(worktreePath string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Unmerged status codes: UU, AA, DD, AU, UA, DU, UD
		xy := line[:2]
		if xy == "UU" || xy == "AA" || xy == "DD" ||
			xy == "AU" || xy == "UA" || xy == "DU" || xy == "UD" {
			files = append(files, strings.TrimSpace(line[3:]))
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
	_, _ = cmd.CombinedOutput()
	return nil
}

// IsBinaryConflict returns true if the given file in the worktree is binary,
// as determined by `git diff --numstat HEAD -- <file>`. Binary files are
// reported as "-\t-\t<path>" by numstat.
//
// On any error (e.g. the file is newly added and not in HEAD) the function
// returns true as a fail-safe so that callers skip LLM resolution — attempting
// to feed raw binary content to an LLM causes "prompt too long" errors.
func IsBinaryConflict(worktreePath string, file string) (bool, error) {
	cmd := exec.Command("git", "diff", "--numstat", "HEAD", "--", file)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fail safe: treat as binary so no LLM call is made.
		return true, nil
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		// numstat returned nothing (file may be newly staged unmerged).
		// Fall back to content sniffing.
		return SniffBinary(fmt.Sprintf("%s/%s", worktreePath, file))
	}
	return strings.HasPrefix(line, "-\t-\t"), nil
}

// SniffBinary returns true if the first 8 KB of the file contains a null byte,
// which is a reliable indicator that the file is binary. It is used as a
// fallback when `git diff --numstat` returns empty output (e.g. a newly-added
// unmerged file not yet recorded in HEAD).
func SniffBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true, nil
		}
	}
	return false, nil
}
