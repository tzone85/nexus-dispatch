package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// TmuxRunner executes agent sessions inside tmux sessions.
type TmuxRunner struct{}

// NewTmuxRunner creates a TmuxRunner.
func NewTmuxRunner() *TmuxRunner {
	return &TmuxRunner{}
}

// Run starts a tmux session with the prepared execution.
// It writes setup files (e.g., CLAUDE.md, prompt files) before spawning
// the session, then propagates critical environment variables into the
// tmux global environment.
func (r *TmuxRunner) Run(exec PreparedExecution) error {
	// Write setup files before spawning so the agent finds them on start.
	for path, content := range exec.SetupFiles {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create dir %s: %v\n", dir, err)
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write %s: %v\n", path, err)
		}
	}

	// Propagate critical env vars so tmux sessions pick up fresh values.
	tmux.PropagateCriticalEnv()

	return tmux.CreateSession(exec.SessionName, exec.WorkDir, exec.Command)
}

// Terminate kills the tmux session.
func (r *TmuxRunner) Terminate(sessionID string) error {
	return tmux.KillSession(sessionID)
}

// SendInput sends keys to the tmux session.
func (r *TmuxRunner) SendInput(sessionID string, input string) error {
	return tmux.SendKeys(sessionID, input)
}

// ReadOutput captures output from the tmux pane.
func (r *TmuxRunner) ReadOutput(sessionID string, lines int) (string, error) {
	return tmux.CapturePaneOutput(sessionID, lines)
}

// IsAlive checks if the tmux session exists.
func (r *TmuxRunner) IsAlive(sessionID string) bool {
	return tmux.SessionExists(sessionID)
}
