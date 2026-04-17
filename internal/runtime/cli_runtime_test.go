package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestCLIRuntime creates a CLIRuntime directly for internal testing.
func newTestCLIRuntime(name, command string, args, models []string) *CLIRuntime {
	return &CLIRuntime{
		name:    name,
		command: command,
		args:    args,
		models:  models,
	}
}

// ── CLIRuntime.Name / SupportedModels ────────────────────────────────

func TestCLIRuntime_Name(t *testing.T) {
	rt := newTestCLIRuntime("claude-code", "claude", nil, nil)
	if rt.Name() != "claude-code" {
		t.Errorf("Name() = %q, want claude-code", rt.Name())
	}
}

func TestCLIRuntime_SupportedModels(t *testing.T) {
	rt := newTestCLIRuntime("test", "test", nil, []string{"opus-4", "sonnet-4"})
	models := rt.SupportedModels()
	if len(models) != 2 {
		t.Fatalf("SupportedModels() = %v, want 2 models", models)
	}
	if models[0] != "opus-4" {
		t.Errorf("models[0] = %q, want opus-4", models[0])
	}
}

// ── CLIRuntime.BuildCommand ───────────────────────────────────────────

func TestBuildCommand_BasicNoPrompt(t *testing.T) {
	rt := newTestCLIRuntime("claude-code", "claude", []string{"--dangerously-skip-permissions"}, nil)
	dir := t.TempDir()

	cmd, err := rt.BuildCommand(SessionConfig{
		WorkDir:     dir,
		SessionName: "test-session",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if !strings.Contains(cmd, "claude") {
		t.Error("command should contain base command 'claude'")
	}
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Error("command should contain the arg")
	}
	if !strings.Contains(cmd, "unset CLAUDECODE") {
		t.Error("command should unset CLAUDECODE")
	}
}

func TestBuildCommand_WithModel(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	dir := t.TempDir()

	cmd, err := rt.BuildCommand(SessionConfig{
		WorkDir: dir,
		Model:   "claude-sonnet-4-5-20250514",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if !strings.Contains(cmd, "--model") {
		t.Error("command should contain --model flag")
	}
	if !strings.Contains(cmd, "claude-sonnet-4-5-20250514") {
		t.Error("command should contain model name")
	}
}

func TestBuildCommand_WithInvalidModel(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	_, err := rt.BuildCommand(SessionConfig{
		WorkDir: t.TempDir(),
		Model:   "model; evil",
	})
	if err == nil {
		t.Fatal("expected error for invalid model name")
	}
	if !strings.Contains(err.Error(), "invalid model name") {
		t.Errorf("error = %v, expected 'invalid model name'", err)
	}
}

func TestBuildCommand_WithInvalidArg(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", []string{"--arg;evil"}, nil)
	_, err := rt.BuildCommand(SessionConfig{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for invalid runtime arg")
	}
	if !strings.Contains(err.Error(), "invalid runtime arg") {
		t.Errorf("error = %v, expected 'invalid runtime arg'", err)
	}
}

func TestBuildCommand_WithPrompt(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	dir := t.TempDir()

	cmd, err := rt.BuildCommand(SessionConfig{
		WorkDir: dir,
		Goal:    "implement feature X",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	// Should reference the prompt file.
	if !strings.Contains(cmd, ".nxd-prompts") {
		t.Error("command should reference .nxd-prompts dir")
	}
	// Prompt file should exist.
	promptPath := filepath.Join(dir, ".nxd-prompts", "prompt.txt")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("prompt file not written: %v", err)
	}
	if !strings.Contains(string(data), "implement feature X") {
		t.Errorf("prompt file content = %q, expected goal text", string(data))
	}
}

func TestBuildCommand_WithSystemPromptAndGoal(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	dir := t.TempDir()

	_, err := rt.BuildCommand(SessionConfig{
		WorkDir:      dir,
		SystemPrompt: "you are a coding agent",
		Goal:         "write tests",
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	promptPath := filepath.Join(dir, ".nxd-prompts", "prompt.txt")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("prompt file not written: %v", err)
	}
	if !strings.Contains(string(data), "you are a coding agent") {
		t.Error("prompt file should contain system prompt")
	}
	if !strings.Contains(string(data), "write tests") {
		t.Error("prompt file should contain goal")
	}
}

func TestBuildCommand_WithLogFile(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	dir := t.TempDir()
	logFile := filepath.Join(dir, "agent.log")

	cmd, err := rt.BuildCommand(SessionConfig{
		WorkDir: dir,
		LogFile: logFile,
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if !strings.Contains(cmd, "tee") {
		t.Error("command should pipe to tee when LogFile is set")
	}
}

func TestBuildCommand_WithEnvVars(t *testing.T) {
	rt := newTestCLIRuntime("test", "claude", nil, nil)
	dir := t.TempDir()

	cmd, err := rt.BuildCommand(SessionConfig{
		WorkDir: dir,
		EnvVars: map[string]string{"MY_TOKEN": "tok123"},
	})
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if !strings.Contains(cmd, "MY_TOKEN") {
		t.Error("command should export custom env vars")
	}
}
