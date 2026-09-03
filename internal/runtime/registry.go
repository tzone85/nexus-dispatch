package runtime

import (
	"fmt"
	"regexp"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// Detection holds compiled regex patterns for detecting runtime states
// from captured terminal output.
type Detection struct {
	IdlePattern       *regexp.Regexp
	PermissionPattern *regexp.Regexp
	PlanModePattern   *regexp.Regexp
}

// CLIRuntime is a concrete Runtime backed by a CLI tool running inside a
// tmux session.
type CLIRuntime struct {
	name      string
	command   string
	args      []string
	models    []string
	detection Detection
	runner    Runner
}

// newRunnerFromConfig is swappable in tests.
var newRunnerFromConfig = NewRunnerFromConfig

// Registry maps runtime names to their CLIRuntime instances, loaded from
// configuration at startup. Native runtimes (e.g. Gemma) are stored
// separately since they don't use CLI/tmux sessions.
type Registry struct {
	runtimes      map[string]*CLIRuntime
	nativeConfigs map[string]config.RuntimeConfig
}

// NewRegistry builds a Registry from the provided runtime configuration map.
// It compiles all detection regex patterns and returns an error if any are
// invalid.
func NewRegistry(cfg map[string]config.RuntimeConfig) (*Registry, error) {
	reg := &Registry{
		runtimes:      make(map[string]*CLIRuntime),
		nativeConfigs: make(map[string]config.RuntimeConfig),
	}

	for name, rc := range cfg {
		// Native runtimes (e.g. Gemma) bypass CLIRuntime creation entirely.
		if rc.Native {
			reg.nativeConfigs[name] = rc
			continue
		}

		detection := Detection{}

		if rc.Detection.IdlePattern != "" {
			p, err := regexp.Compile(rc.Detection.IdlePattern)
			if err != nil {
				return nil, fmt.Errorf("runtime %s idle pattern: %w", name, err)
			}
			detection.IdlePattern = p
		}
		if rc.Detection.PermissionPattern != "" {
			p, err := regexp.Compile(rc.Detection.PermissionPattern)
			if err != nil {
				return nil, fmt.Errorf("runtime %s permission pattern: %w", name, err)
			}
			detection.PermissionPattern = p
		}
		if rc.Detection.PlanModePattern != "" {
			p, err := regexp.Compile(rc.Detection.PlanModePattern)
			if err != nil {
				return nil, fmt.Errorf("runtime %s plan mode pattern: %w", name, err)
			}
			detection.PlanModePattern = p
		}

		// Select the execution backend from runtimes.<name>.runner
		// (tmux default, docker, ssh). Previously the factory was never
		// called and every runtime silently ran in tmux.
		runner, err := newRunnerFromConfig(rc)
		if err != nil {
			return nil, fmt.Errorf("runtime %s: %w", name, err)
		}
		reg.runtimes[name] = &CLIRuntime{
			name:    name,
			command: rc.Command,
			// EffectiveArgs appends the CLI's unattended-mode flag only when
			// the runner is docker/ssh; on the host the agent keeps prompting.
			args:      rc.EffectiveArgs(),
			models:    rc.Models,
			detection: detection,
			runner:    runner,
		}
	}

	return reg, nil
}

// Get returns the Runtime registered under the given name, or an error if
// no such runtime exists.
func (r *Registry) Get(name string) (Runtime, error) {
	rt, ok := r.runtimes[name]
	if !ok {
		return nil, fmt.Errorf("runtime not found: %s", name)
	}
	return rt, nil
}

// List returns the names of all registered runtimes, including native ones.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.runtimes)+len(r.nativeConfigs))
	for name := range r.runtimes {
		names = append(names, name)
	}
	for name := range r.nativeConfigs {
		names = append(names, name)
	}
	return names
}

// IsNative reports whether the named runtime is a native runtime (not CLI-based).
func (r *Registry) IsNative(name string) bool {
	_, ok := r.nativeConfigs[name]
	return ok
}

// NativeConfig returns the configuration for a native runtime, or false if it
// is not found.
func (r *Registry) NativeConfig(name string) (config.RuntimeConfig, bool) {
	cfg, ok := r.nativeConfigs[name]
	return cfg, ok
}

// Name returns the runtime's registered name.
func (c *CLIRuntime) Name() string { return c.name }

// SupportedModels returns the list of models this runtime can use.
func (c *CLIRuntime) SupportedModels() []string { return c.models }

// nxdMDContent is written to each worktree on every spawn so that
// Claude Code's superpowers/brainstorming plugins don't override the
// -p prompt instructions. Re-written unconditionally because a reused
// worktree may have a stale or missing CLAUDE.md.
const nxdMDContent = `# NXD Agent Directive

You are an automated coding agent dispatched by NXD (nexus-dispatch).
Follow these rules strictly:

1. **Do NOT brainstorm or plan.** Execute the task described in the prompt immediately.
2. **Do NOT ask questions.** Make reasonable decisions and proceed.
3. **Do NOT enter plan mode.** Write code directly.
4. **Do NOT use interactive features.** No confirmations, no menus.
5. **Commit your changes** when the task is complete.
6. **Stay focused on the assigned story only.** Do not refactor unrelated code.
`

// BuildCommand constructs the full shell command string for the CLI runtime
// and writes the prompt and env files (0600) into cfg.WorkDir. The returned
// string never contains a secret value — see envfile.go.
func (c *CLIRuntime) BuildCommand(cfg SessionConfig) (string, error) {
	pe, err := c.prepare(cfg)
	if err != nil {
		return "", err
	}
	if err := pe.WriteSetupFiles(); err != nil {
		return "", err
	}
	return pe.Command, nil
}

// prepare builds the PreparedExecution for cfg without I/O.
func (c *CLIRuntime) prepare(cfg SessionConfig) (PreparedExecution, error) {
	return prepareCLIExecution(c.command, c.args, cfg, false)
}

// Spawn prepares the session and delegates to the runtime's Runner (tmux by
// default; docker/ssh when runtimes.<name>.runner says so). The runner writes
// the setup files (CLAUDE.md, prompt, env) — unconditionally on every spawn,
// because a reused worktree may have stale content — and starts the session.
func (c *CLIRuntime) Spawn(cfg SessionConfig) error {
	pe, err := c.prepare(cfg)
	if err != nil {
		return err
	}
	return c.runner.Run(pe)
}

// Terminate stops the session via the runner.
func (c *CLIRuntime) Terminate(sessionID string) error {
	return c.runner.Terminate(sessionID)
}

// SendInput sends a line of text to the session via the runner.
func (c *CLIRuntime) SendInput(sessionID string, input string) error {
	return c.runner.SendInput(sessionID, input)
}

// ReadOutput captures the last N lines of output via the runner.
func (c *CLIRuntime) ReadOutput(sessionID string, lines int) (string, error) {
	return c.runner.ReadOutput(sessionID, lines)
}

// Runner returns the execution backend this runtime delegates to.
func (c *CLIRuntime) Runner() Runner { return c.runner }

// WithRunner replaces the execution backend (tests inject fakes).
func (c *CLIRuntime) WithRunner(r Runner) *CLIRuntime {
	c.runner = r
	return c
}

// DetectStatus reads recent output from the session and matches it against
// the configured detection patterns to determine the agent's current state.
func (c *CLIRuntime) DetectStatus(sessionID string) (AgentStatus, error) {
	output, err := c.ReadOutput(sessionID, 20)
	if err != nil {
		if !c.runner.IsAlive(sessionID) {
			return StatusTerminated, nil
		}
		return StatusWorking, err
	}

	if c.detection.PermissionPattern != nil && c.detection.PermissionPattern.MatchString(output) {
		return StatusPermissionPrompt, nil
	}
	if c.detection.PlanModePattern != nil && c.detection.PlanModePattern.MatchString(output) {
		return StatusPlanMode, nil
	}
	if c.detection.IdlePattern != nil && c.detection.IdlePattern.MatchString(output) {
		return StatusDone, nil
	}

	return StatusWorking, nil
}
