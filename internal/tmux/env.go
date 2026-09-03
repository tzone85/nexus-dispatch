package tmux

// criticalEnvVars lists environment variables that agents must receive
// fresh per session. Historically they were pushed into the tmux GLOBAL
// environment via `tmux set-environment -g KEY VALUE`, which put the secret
// in the tmux client's argv (visible in `ps`) and shared it with every
// session on the server. Values now travel in a per-session 0600 env file
// (see internal/runtime/envfile.go); this package only REMOVES stale global
// values so they cannot shadow the per-session file.
var criticalEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OLLAMA_HOST",
}

// ClearStaleEnv unsets the listed variables from the tmux global environment
// (`tmux set-environment -g -u KEY`). No value is ever passed to tmux. Errors
// are ignored: the variable may never have been set, or no server may be
// running yet.
func ClearStaleEnv(vars []string) {
	for _, key := range vars {
		_ = run("set-environment", "-g", "-u", key)
	}
}

// ClearStaleCriticalEnv clears the critical variables (API keys, host
// overrides) from the tmux global environment.
func ClearStaleCriticalEnv() {
	ClearStaleEnv(criticalEnvVars)
}
