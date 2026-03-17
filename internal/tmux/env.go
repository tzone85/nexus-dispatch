package tmux

import (
	"log"
	"os"
)

// criticalEnvVars lists environment variables that must be propagated into the
// tmux global environment before spawning agent sessions. Without this, the
// tmux server may hold stale values from the time it was first started,
// causing agents to authenticate with expired or wrong API keys.
var criticalEnvVars = []string{
	"OLLAMA_HOST",
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
}

// PropagateEnv reads the listed environment variables from the current process
// and sets them in the tmux global environment via `tmux set-environment -g`.
// Variables that are unset in the current process are removed from the tmux
// global environment so agents don't inherit stale values.
//
// Errors are logged but not returned; a failure to propagate one variable
// should not prevent session creation.
func PropagateEnv(vars []string) {
	for _, key := range vars {
		val, ok := os.LookupEnv(key)
		if ok {
			if err := run("set-environment", "-g", key, val); err != nil {
				log.Printf("tmux: warning: failed to set-environment %s: %v", key, err)
			}
		} else {
			// Remove stale value from tmux global env; ignore errors
			// (e.g. variable was never set in tmux).
			_ = run("set-environment", "-g", "-u", key)
		}
	}
}

// PropagateCriticalEnv is a convenience wrapper that propagates all
// critical environment variables (API keys, host overrides) into the
// tmux global environment.
func PropagateCriticalEnv() {
	PropagateEnv(criticalEnvVars)
}
