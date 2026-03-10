package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// Available reports whether the tmux binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// CreateSession starts a new detached tmux session with the given name and
// working directory. An optional initial command may be provided.
func CreateSession(name, workDir, command string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", workDir}
	if command != "" {
		args = append(args, command)
	}
	return run(args...)
}

// KillSession destroys the named tmux session.
func KillSession(name string) error {
	return run("kill-session", "-t", name)
}

// SessionExists returns true when a tmux session with the given name is alive.
func SessionExists(name string) bool {
	err := run("has-session", "-t", name)
	return err == nil
}

// ListSessions returns the names of all running tmux sessions.
func ListSessions() ([]string, error) {
	out, err := output("list-sessions", "-F", "#{session_name}")
	if err != nil {
		// No sessions is not an error.
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(err.Error(), "no sessions") {
			return nil, nil
		}
		return nil, err
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

// run executes a tmux subcommand and returns any error.
func run(args ...string) error {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// output executes a tmux subcommand and returns its combined stdout/stderr.
func output(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
