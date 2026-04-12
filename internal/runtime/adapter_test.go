package runtime

import (
	"strings"
	"testing"
)

func TestCLIAdapter_Prepare_BasicCommand(t *testing.T) {
	adapter := NewCLIAdapter("claude-code", "claude", []string{"--dangerously-skip-permissions"}, []string{"opus-4"})
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		SessionName: "test-session",
		WorkDir:     dir,
		Model:       "claude-sonnet-4-5-20250514",
		Goal:        "implement login",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if exec.SessionName != "test-session" {
		t.Errorf("SessionName = %q, want test-session", exec.SessionName)
	}
	if exec.WorkDir != dir {
		t.Errorf("WorkDir = %q, want %q", exec.WorkDir, dir)
	}
	if !strings.Contains(exec.Command, "--model") {
		t.Error("command should contain --model")
	}
	if !strings.Contains(exec.Command, "claude-sonnet-4-5-20250514") {
		t.Error("command should contain model name")
	}
	if _, ok := exec.SetupFiles[dir+"/CLAUDE.md"]; !ok {
		t.Error("setup files should include CLAUDE.md")
	}
}

func TestCLIAdapter_Prepare_RejectsUnsafeModel(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	_, err := adapter.Prepare(SessionConfig{
		Model:   "model; evil",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("should reject unsafe model name")
	}
}

func TestCLIAdapter_Prepare_RejectsUnsafeArg(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", []string{"--flag;evil"}, nil)
	_, err := adapter.Prepare(SessionConfig{
		WorkDir: t.TempDir(),
		Goal:    "test",
	})
	if err == nil {
		t.Fatal("should reject unsafe runtime arg")
	}
}

func TestCLIAdapter_Prepare_PromptFile(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", []string{"-p", "-"}, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir:      dir,
		Goal:         "do something",
		SystemPrompt: "you are an agent",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	promptPath := dir + "/.nxd-prompts/prompt.txt"
	content, ok := exec.SetupFiles[promptPath]
	if !ok {
		t.Fatal("prompt file not in setup files")
	}
	if !strings.Contains(content, "you are an agent") {
		t.Error("prompt file should contain system prompt")
	}
	if !strings.Contains(content, "do something") {
		t.Error("prompt file should contain goal")
	}
}

func TestCLIAdapter_Prepare_NoPromptWithoutGoal(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if strings.Contains(exec.Command, " -p ") {
		t.Error("command should not contain -p flag when no goal is set")
	}
}

func TestCLIAdapter_Prepare_LogFile(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir: dir,
		Goal:    "test",
		LogFile: "/tmp/test.log",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !strings.Contains(exec.Command, "tee") {
		t.Error("command should pipe to tee when LogFile is set")
	}
	if exec.LogFile != "/tmp/test.log" {
		t.Errorf("LogFile = %q, want /tmp/test.log", exec.LogFile)
	}
}

func TestCLIAdapter_Prepare_EnvVars(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir: dir,
		Goal:    "test",
		EnvVars: map[string]string{"CUSTOM_VAR": "custom_value"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if exec.Env["CUSTOM_VAR"] != "custom_value" {
		t.Error("env should contain custom var")
	}
	if !strings.Contains(exec.Command, "CUSTOM_VAR") {
		t.Error("command should export custom env var")
	}
}

func TestCLIAdapter_Prepare_UnsetsClaudeCode(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !strings.Contains(exec.Command, "unset CLAUDECODE") {
		t.Error("command should unset CLAUDECODE")
	}
}

func TestCLIAdapter_Name(t *testing.T) {
	adapter := NewCLIAdapter("claude-code", "claude", nil, []string{"opus-4"})
	if adapter.Name() != "claude-code" {
		t.Errorf("Name() = %q, want claude-code", adapter.Name())
	}
	if len(adapter.SupportedModels()) != 1 || adapter.SupportedModels()[0] != "opus-4" {
		t.Errorf("SupportedModels() = %v, want [opus-4]", adapter.SupportedModels())
	}
}

func TestCLIAdapter_Prepare_CLAUDEMDContent(t *testing.T) {
	adapter := NewCLIAdapter("test", "claude", nil, nil)
	dir := t.TempDir()

	exec, err := adapter.Prepare(SessionConfig{
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	claudePath := dir + "/CLAUDE.md"
	content, ok := exec.SetupFiles[claudePath]
	if !ok {
		t.Fatal("CLAUDE.md not in setup files")
	}
	if !strings.Contains(content, "NXD Agent Directive") {
		t.Error("CLAUDE.md content should contain NXD Agent Directive")
	}
}
