package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// IsRepo reports whether dir is inside a git repository, as the error it is
// not. The callers act on the answer (gc reports the repo and cleans the
// others; archive says it could not check), so the message carries git's.
func IsRepo(dir string) error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s is not a git repository: %s", dir, strings.TrimSpace(string(out)))
	}
	return nil
}
