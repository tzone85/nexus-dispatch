package runtime

import (
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// TmuxRunner executes agent sessions inside tmux sessions.
type TmuxRunner struct{}

// NewTmuxRunner creates a TmuxRunner.
func NewTmuxRunner() *TmuxRunner {
	return &TmuxRunner{}
}

// Run writes the setup files (CLAUDE.md, prompt, 0600 env file) and starts a
// detached tmux session running the prepared command. Secrets reach the
// agent by the command sourcing the env file — never via tmux argv or the
// tmux global environment; stale global values are cleared first so they
// cannot shadow the per-session file.
func (r *TmuxRunner) Run(exec PreparedExecution) error {
	if err := exec.WriteSetupFiles(); err != nil {
		return err
	}
	tmux.ClearStaleCriticalEnv()
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
