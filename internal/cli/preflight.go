package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// gitInsideWorkTreeFn lets tests stub out the real `git rev-parse` call.
// Production: realGitInsideWorkTree shells out to git.
var gitInsideWorkTreeFn = realGitInsideWorkTree

// tmuxAvailableFn lets tests stub out the PATH lookup that backs
// tmux.Available(). Production: tmux.Available.
var tmuxAvailableFn = tmux.Available

// errNotAGitRepo and errTmuxMissing are sentinels callers can errors.Is-match
// in tests. The wrapped error returned from PreflightForRun is what the user
// sees; the sentinel exists for test assertions.
var (
	errNotAGitRepo  = errors.New("not a git repository")
	errTmuxMissing  = errors.New("tmux not available on PATH")
)

// PreflightForRun runs cheap checks before the LLM pipeline incurs cost:
//
//  1. cwd is inside a git work tree (worktrees + branch ops need git);
//  2. tmux is on PATH IF the active config has any non-native CLI runtime
//     (aider, claude-code, codex). Configs that only use the native gemma
//     runtime skip the tmux check.
//
// Errors are wrapped with platform-aware recovery hints — Windows users get
// pointed at WSL2, macOS/Linux at the relevant package manager.
func PreflightForRun(cwd string, cfg config.Config) error {
	if err := checkGitRepo(cwd); err != nil {
		return err
	}
	if err := checkTmuxIfNeeded(cfg); err != nil {
		return err
	}
	return nil
}

func checkGitRepo(dir string) error {
	if gitInsideWorkTreeFn(dir) {
		return nil
	}
	return fmt.Errorf("%w: %s is not inside a git repository.\n"+
		"NXD spawns worktrees and branches per story; cd into a git repo first.\n"+
		"To start from scratch: git init && git commit --allow-empty -m init",
		errNotAGitRepo, dir)
}

func checkTmuxIfNeeded(cfg config.Config) error {
	if !cfgRequiresTmux(cfg) {
		return nil
	}
	if tmuxAvailableFn() {
		return nil
	}
	hint := "install tmux (`brew install tmux` on macOS, `apt install tmux` on Debian/Ubuntu)"
	if runtime.GOOS == "windows" {
		hint = "tmux has no native Windows port; run NXD inside WSL2 (Ubuntu). " +
			"See the Windows install section of README.md. " +
			"Read-only commands (status, dashboard, doctor) still work on native Windows."
	}
	return fmt.Errorf("%w: %s", errTmuxMissing, hint)
}

// cfgRequiresTmux returns true when any configured runtime is a CLI runtime
// (Native==false). The native gemma runtime runs in-process and needs no tmux.
func cfgRequiresTmux(cfg config.Config) bool {
	for _, rt := range cfg.Runtimes {
		if !rt.Native {
			return true
		}
	}
	return false
}

func realGitInsideWorkTree(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}
