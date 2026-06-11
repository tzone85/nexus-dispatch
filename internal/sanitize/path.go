package sanitize

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SafeJoin joins root and rel and returns the cleaned path only if the result
// stays within root. It rejects any path that escapes via "..", absolute
// rel components, or symlink-like trickery prior to filesystem resolution.
//
// SafeJoin does NOT resolve symlinks; callers that need symlink protection
// should follow up with filepath.EvalSymlinks and re-check the result.
func SafeJoin(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("empty root")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("rel %q must be relative, not absolute", rel)
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanRoot, rel))
	if joined == cleanRoot {
		return joined, nil
	}
	if !strings.HasPrefix(joined, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes root %q", rel, root)
	}
	return joined, nil
}

// ValidIdentifier returns true if s contains only [a-zA-Z0-9_.-]. Useful for
// validating story IDs, agent IDs, and session names before using them in
// filesystem paths or shell arguments.
func ValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
			continue
		default:
			return false
		}
	}
	return true
}

// ValidTmuxTarget returns true if s is safe to pass to tmux as a session
// target via -t. Stricter than ValidIdentifier: the `.` and `:` characters
// have window/pane semantics in tmux target specifiers ("session.0",
// "session:1") so we exclude both even though tmux session names CAN
// technically contain them. A session name like "mysession.0" would
// otherwise let an attacker (or a corrupted projection) target a specific
// pane in a different session via the kill command.
func ValidTmuxTarget(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			continue
		default:
			return false
		}
	}
	return true
}
