package runtime

// PreparedExecution holds everything needed to run an agent session,
// decoupled from how it will be executed.
type PreparedExecution struct {
	Command     string            // The full shell command string
	WorkDir     string            // Working directory
	Env         map[string]string // Environment variables
	SessionName string            // Identifier for the session
	LogFile     string            // Path for output logging
	SetupFiles  map[string]string // Files to write before execution (path -> content)
}

// Adapter translates a SessionConfig into a PreparedExecution.
// Different adapters handle different agent CLIs (Claude, Codex, Gemini).
type Adapter interface {
	// Prepare builds the command and environment for an agent session
	// without executing anything. Pure function, no side effects.
	Prepare(cfg SessionConfig) (PreparedExecution, error)

	// Name returns the adapter's identifier.
	Name() string

	// SupportedModels returns models this adapter can handle.
	SupportedModels() []string
}
