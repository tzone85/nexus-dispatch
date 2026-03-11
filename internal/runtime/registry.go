package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
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
}

// Registry maps runtime names to their CLIRuntime instances, loaded from
// configuration at startup.
type Registry struct {
	runtimes map[string]*CLIRuntime
}

// NewRegistry builds a Registry from the provided runtime configuration map.
// It compiles all detection regex patterns and returns an error if any are
// invalid.
func NewRegistry(cfg map[string]config.RuntimeConfig) (*Registry, error) {
	reg := &Registry{runtimes: make(map[string]*CLIRuntime)}

	for name, rc := range cfg {
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

		reg.runtimes[name] = &CLIRuntime{
			name:      name,
			command:   rc.Command,
			args:      rc.Args,
			models:    rc.Models,
			detection: detection,
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

// List returns the names of all registered runtimes.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.runtimes))
	for name := range r.runtimes {
		names = append(names, name)
	}
	return names
}

// Name returns the runtime's registered name.
func (c *CLIRuntime) Name() string { return c.name }

// SupportedModels returns the list of models this runtime can use.
func (c *CLIRuntime) SupportedModels() []string { return c.models }

// Spawn creates a new tmux session running the CLI tool with the given
// configuration. Output is tee'd to a log file for post-mortem diagnosis.
func (c *CLIRuntime) Spawn(cfg SessionConfig) error {
	cmdStr := c.command
	for _, arg := range c.args {
		cmdStr += " " + arg
	}
	if cfg.Model != "" {
		cmdStr += " --model " + cfg.Model
	}

	// Write the combined prompt (system context + goal) to a file and pipe
	// it via stdin. This avoids all shell escaping issues with quotes, $,
	// backticks, and newlines that would break when passed as arguments.
	prompt := cfg.Goal
	if cfg.SystemPrompt != "" {
		prompt = cfg.SystemPrompt + "\n\n---\n\n" + cfg.Goal
	}
	if prompt != "" {
		promptDir := filepath.Join(cfg.WorkDir, ".nxd-prompts")
		os.MkdirAll(promptDir, 0o755)
		promptFile := filepath.Join(promptDir, "prompt.txt")
		if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
			return fmt.Errorf("write prompt file: %w", err)
		}
		cmdStr = fmt.Sprintf("cat %q | %s", promptFile, cmdStr)
	}

	// Tee output to a log file so we can inspect it after the session exits.
	if cfg.LogFile != "" {
		cmdStr += fmt.Sprintf(" 2>&1 | tee %q", cfg.LogFile)
	}

	// Unset CLAUDECODE to prevent "nested session" errors when NXD itself
	// is running inside a Claude Code session.
	cmdStr = "unset CLAUDECODE; " + cmdStr

	return tmux.CreateSession(cfg.SessionName, cfg.WorkDir, cmdStr)
}

// Terminate destroys the tmux session identified by sessionID.
func (c *CLIRuntime) Terminate(sessionID string) error {
	return tmux.KillSession(sessionID)
}

// SendInput sends a line of text to the tmux session identified by sessionID.
func (c *CLIRuntime) SendInput(sessionID string, input string) error {
	return tmux.SendKeys(sessionID, input)
}

// ReadOutput captures the last N lines of terminal output from the session.
func (c *CLIRuntime) ReadOutput(sessionID string, lines int) (string, error) {
	return tmux.CapturePaneOutput(sessionID, lines)
}

// DetectStatus reads recent output from the session and matches it against
// the configured detection patterns to determine the agent's current state.
func (c *CLIRuntime) DetectStatus(sessionID string) (AgentStatus, error) {
	output, err := c.ReadOutput(sessionID, 20)
	if err != nil {
		if !tmux.SessionExists(sessionID) {
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
