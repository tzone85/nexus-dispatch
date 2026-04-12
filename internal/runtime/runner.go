package runtime

// Runner executes a PreparedExecution in some environment.
// Different runners handle different execution targets (tmux, Docker, SSH).
type Runner interface {
	// Run starts the prepared execution and returns a session handle.
	Run(exec PreparedExecution) error

	// Terminate stops the running session.
	Terminate(sessionID string) error

	// SendInput sends text to the running session.
	SendInput(sessionID string, input string) error

	// ReadOutput captures recent output from the session.
	ReadOutput(sessionID string, lines int) (string, error)

	// IsAlive returns true if the session is still running.
	IsAlive(sessionID string) bool
}
