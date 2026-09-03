package runtime

import (
	"fmt"
	"path/filepath"
)

// CLIAdapter implements Adapter for CLI-based agent runtimes.
// It translates a SessionConfig into a PreparedExecution without performing
// any I/O — all file writes and process spawning are deferred to the Runner.
type CLIAdapter struct {
	name    string
	command string
	args    []string
	models  []string
}

// NewCLIAdapter creates an adapter for a CLI-based agent runtime.
func NewCLIAdapter(name, command string, args, models []string) *CLIAdapter {
	return &CLIAdapter{
		name:    name,
		command: command,
		args:    args,
		models:  models,
	}
}

// Name returns the adapter's identifier.
func (a *CLIAdapter) Name() string { return a.name }

// SupportedModels returns models this adapter can handle.
func (a *CLIAdapter) SupportedModels() []string { return a.models }

// Prepare builds the full command string and environment without executing.
// Secrets are never placed in the command: the env file (0600, sourced then
// deleted by the command) and the prompt file are returned as SetupFiles.
func (a *CLIAdapter) Prepare(cfg SessionConfig) (PreparedExecution, error) {
	return prepareCLIExecution(a.command, a.args, cfg, true)
}

// prepareCLIExecution is the single builder behind CLIAdapter.Prepare and
// CLIRuntime.BuildCommand. promptFlag adds "-p" before the prompt argument
// (Claude Code's non-interactive flag; BuildCommand historically passed the
// prompt positionally).
//
// Resulting command shape:
//
//	. ./.nxd-prompts/env.sh && rm -f ./.nxd-prompts/env.sh; unset CLAUDECODE; \
//	  <command> <args...> --model <m> [-p] "$(cat .nxd-prompts/prompt.txt)" 2>&1 | tee <log>
//
// Every interpolated value goes through QuoteShellArg; the env file and the
// prompt file are referenced by worktree-relative path so the same string is
// valid under tmux (-c workdir), docker (-w /workspace) and ssh (cd dir).
func prepareCLIExecution(command string, args []string, cfg SessionConfig, promptFlag bool) (PreparedExecution, error) {
	cmdStr := command
	for _, arg := range args {
		if err := ValidateShellArg(arg); err != nil {
			return PreparedExecution{}, fmt.Errorf("invalid runtime arg: %w", err)
		}
		cmdStr += " " + QuoteShellArg(arg)
	}
	if cfg.Model != "" {
		if err := ValidateModelName(cfg.Model); err != nil {
			return PreparedExecution{}, fmt.Errorf("invalid model name: %w", err)
		}
		cmdStr += " --model " + QuoteShellArg(cfg.Model)
	}

	prompt := cfg.Goal
	if cfg.SystemPrompt != "" {
		prompt = cfg.SystemPrompt + "\n\n---\n\n" + cfg.Goal
	}

	setupFiles := make(map[string]string)

	// Prompt goes into a file referenced via shell substitution — piping via
	// stdin does not work reliably inside tmux detached sessions.
	if prompt != "" && cfg.WorkDir != "" {
		setupFiles[filepath.Join(cfg.WorkDir, PromptFileRel)] = prompt
		if promptFlag {
			cmdStr += " -p"
		}
		cmdStr += ` "$(cat ` + PromptFileRel + `)"`
	}

	// Tee output to a log file for post-mortem diagnosis.
	if cfg.LogFile != "" {
		cmdStr += " 2>&1 | tee " + QuoteShellArg(cfg.LogFile)
	}

	env, err := sessionEnv(cfg.EnvVars)
	if err != nil {
		return PreparedExecution{}, err
	}
	hasEnvFile := len(env) > 0 && cfg.WorkDir != ""
	if hasEnvFile {
		content, err := RenderEnvFile(env)
		if err != nil {
			return PreparedExecution{}, err
		}
		setupFiles[filepath.Join(cfg.WorkDir, EnvFileRel)] = content
	}
	cmdStr = envSourcePrefix(hasEnvFile) + cmdStr

	// CLAUDE.md stops Claude Code plugins from brainstorming/planning.
	if cfg.WorkDir != "" {
		setupFiles[filepath.Join(cfg.WorkDir, "CLAUDE.md")] = nxdMDContent
	}

	return PreparedExecution{
		Command:     cmdStr,
		WorkDir:     cfg.WorkDir,
		Env:         env,
		SessionName: cfg.SessionName,
		LogFile:     cfg.LogFile,
		SetupFiles:  setupFiles,
	}, nil
}

// WriteSetupFiles writes every SetupFiles entry with mode 0600 (they may
// carry API keys), creating parent directories. Shared by all runners.
func (pe PreparedExecution) WriteSetupFiles() error {
	for path, content := range pe.SetupFiles {
		if err := writeSecretFile(path, content); err != nil {
			return fmt.Errorf("write setup file %s: %w", path, err)
		}
	}
	return nil
}
