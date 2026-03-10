// Package runtime provides the Runtime interface, agent session lifecycle
// management, and a config-driven registry for pluggable CLI runtimes.
package runtime

// AgentStatus represents the detected state of an agent session.
type AgentStatus int

const (
	StatusWorking AgentStatus = iota
	StatusStuck
	StatusDone
	StatusPermissionPrompt
	StatusPlanMode
	StatusTerminated
)

// String returns the human-readable name of the status.
func (s AgentStatus) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusStuck:
		return "stuck"
	case StatusDone:
		return "done"
	case StatusPermissionPrompt:
		return "permission_prompt"
	case StatusPlanMode:
		return "plan_mode"
	case StatusTerminated:
		return "terminated"
	}
	return "unknown"
}

// SessionConfig holds the parameters needed to spawn a new agent session.
type SessionConfig struct {
	WorkDir      string
	Model        string
	SystemPrompt string
	Goal         string
	EnvVars      map[string]string
	SessionName  string
}

// Runtime is the interface that CLI runtimes must implement to participate
// in the NXD agent orchestration loop.
type Runtime interface {
	Spawn(cfg SessionConfig) error
	Terminate(sessionID string) error
	SendInput(sessionID string, input string) error
	ReadOutput(sessionID string, lines int) (string, error)
	DetectStatus(sessionID string) (AgentStatus, error)
	Name() string
	SupportedModels() []string
}
