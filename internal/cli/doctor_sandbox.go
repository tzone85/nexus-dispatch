package cli

import (
	"fmt"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// checkSandbox reports how agent commands will be confined. Two independent
// surfaces are covered:
//
//   - native tool commands (gemma run_command, success criteria, investigator)
//     follow sandbox.mode — docker when the daemon answers, otherwise host;
//   - CLI agents (claude-code, codex, aider) run wherever their
//     runtimes.<name>.runner says — tmux on this host by default.
//
// dockerUp is injected so the check is unit-testable without a daemon.
func checkSandbox(cfg config.Config, dockerUp func() bool) checkResult {
	const name = "Sandbox"
	if cfg.Runtimes == nil && cfg.Sandbox.Mode == "" {
		return checkResult{name, "warn", "No config loaded — cannot determine sandbox mode"}
	}
	var notes []string
	status := "ok"

	switch cfg.Sandbox.Mode {
	case "host":
		status = "warn"
		notes = append(notes, "sandbox.mode: host — native tool commands run unsandboxed on this host")
	case "docker":
		if !dockerUp() {
			return checkResult{name, "fail", "sandbox.mode: docker but `docker info` failed — start docker or set sandbox.mode: host"}
		}
		notes = append(notes, fmt.Sprintf("native tool commands run in docker (%s, network %s)", cfg.Sandbox.Image, cfg.Sandbox.Network))
	default: // auto
		if dockerUp() {
			notes = append(notes, fmt.Sprintf("native tool commands run in docker (%s, network %s)", cfg.Sandbox.Image, cfg.Sandbox.Network))
		} else {
			status = "warn"
			notes = append(notes, "sandbox.mode: auto but docker is unavailable — native tool commands run unsandboxed on this host (set sandbox.mode: host to acknowledge)")
		}
	}

	if hostRuntimes := cfg.UnsandboxedRuntimes(); len(hostRuntimes) > 0 {
		status = "warn"
		notes = append(notes, fmt.Sprintf("agents run unsandboxed on this host: %s (set runtimes.<name>.runner: docker|ssh to confine them; permission prompts are NOT auto-approved on the host)", strings.Join(hostRuntimes, ", ")))
	}
	return checkResult{name, status, strings.Join(notes, "; ")}
}
