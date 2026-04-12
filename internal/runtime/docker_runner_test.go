package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRunner_Implements_Runner(t *testing.T) {
	var _ Runner = (*DockerRunner)(nil)
}

func TestNewDockerRunner_Defaults(t *testing.T) {
	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	if r.image != "test:latest" {
		t.Errorf("image = %q, want test:latest", r.image)
	}
	if r.network != "host" {
		t.Errorf("network = %q, want host (default)", r.network)
	}
}

func TestNewDockerRunner_CustomNetwork(t *testing.T) {
	r := NewDockerRunner(DockerConfig{Image: "test:latest", Network: "bridge"})
	if r.network != "bridge" {
		t.Errorf("network = %q, want bridge", r.network)
	}
}

func TestNewDockerRunner_ExtraFlags(t *testing.T) {
	flags := []string{"--cpus=2", "--memory=512m"}
	r := NewDockerRunner(DockerConfig{
		Image:      "test:latest",
		ExtraFlags: flags,
	})
	if len(r.extraFlags) != 2 {
		t.Errorf("extraFlags length = %d, want 2", len(r.extraFlags))
	}
	if r.extraFlags[0] != "--cpus=2" {
		t.Errorf("extraFlags[0] = %q, want --cpus=2", r.extraFlags[0])
	}
}

func TestDockerRunner_SendInput_Unsupported(t *testing.T) {
	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	err := r.SendInput("test-session", "hello")
	if err == nil {
		t.Error("SendInput should return error for Docker runner")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, should mention 'not supported'", err.Error())
	}
}

func TestDockerRunner_Run_BuildsCorrectArgs(t *testing.T) {
	// Replace execCommand with a mock to capture the args
	var capturedName string
	var capturedArgs []string
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = args
		// Return a command that succeeds
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "nxd-agent:latest", Network: "bridge"})
	pe := PreparedExecution{
		Command:     "claude -p 'do something'",
		WorkDir:     t.TempDir(),
		Env:         map[string]string{"API_KEY": "secret"},
		SessionName: "test-docker-run",
		SetupFiles:  map[string]string{},
	}

	err := r.Run(pe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedName != "docker" {
		t.Errorf("command = %q, want docker", capturedName)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "run -d") {
		t.Error("should use 'run -d' for detached mode")
	}
	if !strings.Contains(joined, "--name test-docker-run") {
		t.Error("should set container name to session name")
	}
	if !strings.Contains(joined, "--network bridge") {
		t.Error("should set network to bridge")
	}
	if !strings.Contains(joined, "-w /workspace") {
		t.Error("should set working directory to /workspace")
	}
	if !strings.Contains(joined, "API_KEY=secret") {
		t.Error("should pass environment variables")
	}
	if !strings.Contains(joined, "nxd-agent:latest") {
		t.Error("should use the configured image")
	}
}

func TestDockerRunner_Run_WritesSetupFiles(t *testing.T) {
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	dir := t.TempDir()
	setupFile := filepath.Join(dir, "sub", "CLAUDE.md")

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	pe := PreparedExecution{
		Command:     "echo test",
		WorkDir:     dir,
		SessionName: "test-setup",
		SetupFiles:  map[string]string{setupFile: "# Agent Directive"},
	}

	err := r.Run(pe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(setupFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "# Agent Directive" {
		t.Errorf("setup file content = %q, want %q", string(data), "# Agent Directive")
	}
}

func TestDockerRunner_Run_MountsLogDir(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	logFile := filepath.Join(logDir, "agent.log")

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	pe := PreparedExecution{
		Command:     "echo test",
		WorkDir:     dir,
		SessionName: "test-logmount",
		LogFile:     logFile,
		SetupFiles:  map[string]string{},
	}

	err := r.Run(pe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, logDir+":"+logDir) {
		t.Error("should mount log directory into container")
	}
}

func TestDockerRunner_Terminate_StopsAndRemoves(t *testing.T) {
	var commands []string
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	err := r.Terminate("my-session")
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	if len(commands) < 2 {
		t.Fatalf("expected at least 2 docker commands, got %d", len(commands))
	}
	if !strings.Contains(commands[0], "docker stop my-session") {
		t.Errorf("first command = %q, want docker stop", commands[0])
	}
	if !strings.Contains(commands[1], "docker rm -f my-session") {
		t.Errorf("second command = %q, want docker rm -f", commands[1])
	}
}

func TestDockerRunner_ReadOutput_UsesDockerLogs(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.Command("echo", "line1\nline2\nline3")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	out, err := r.ReadOutput("my-session", 50)
	if err != nil {
		t.Fatalf("ReadOutput: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "logs --tail 50") {
		t.Errorf("args = %q, should contain 'logs --tail 50'", joined)
	}
	if out == "" {
		t.Error("ReadOutput should return non-empty output")
	}
}

func TestDockerRunner_IsAlive_RunningContainer(t *testing.T) {
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "true")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	if !r.IsAlive("running-session") {
		t.Error("IsAlive should return true for running container")
	}
}

func TestDockerRunner_IsAlive_StoppedContainer(t *testing.T) {
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "false")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	if r.IsAlive("stopped-session") {
		t.Error("IsAlive should return false for stopped container")
	}
}

func TestDockerRunner_IsAlive_NoContainer(t *testing.T) {
	original := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		// Simulate a command that fails (container not found)
		return exec.Command("false")
	}
	defer func() { execCommand = original }()

	r := NewDockerRunner(DockerConfig{Image: "test:latest"})
	if r.IsAlive("nonexistent") {
		t.Error("IsAlive should return false when docker inspect fails")
	}
}
