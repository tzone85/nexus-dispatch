package runtime

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSSHRunner_Implements_Runner(t *testing.T) {
	var _ Runner = (*SSHRunner)(nil)
}

func TestNewSSHRunner_Defaults(t *testing.T) {
	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	if r.host != "user@host" {
		t.Errorf("host = %q, want user@host", r.host)
	}
	if r.remoteDir != "/tmp/nxd-agent" {
		t.Errorf("remoteDir = %q, want /tmp/nxd-agent", r.remoteDir)
	}
	if r.keyFile != "" {
		t.Errorf("keyFile = %q, want empty", r.keyFile)
	}
}

func TestNewSSHRunner_CustomConfig(t *testing.T) {
	r := NewSSHRunner(SSHConfig{
		Host:      "deploy@prod.example.com",
		KeyFile:   "/home/user/.ssh/id_ed25519",
		RemoteDir: "/opt/nxd",
	})
	if r.host != "deploy@prod.example.com" {
		t.Errorf("host = %q", r.host)
	}
	if r.keyFile != "/home/user/.ssh/id_ed25519" {
		t.Errorf("keyFile = %q", r.keyFile)
	}
	if r.remoteDir != "/opt/nxd" {
		t.Errorf("remoteDir = %q", r.remoteDir)
	}
}

func TestNewSSHRunner_ExtraFlags(t *testing.T) {
	flags := []string{"-o", "StrictHostKeyChecking=no"}
	r := NewSSHRunner(SSHConfig{
		Host:       "user@host",
		ExtraFlags: flags,
	})
	if len(r.extraFlags) != 2 {
		t.Errorf("extraFlags length = %d, want 2", len(r.extraFlags))
	}
}

func TestSSHRunner_SendInput_Unsupported(t *testing.T) {
	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	err := r.SendInput("test-session", "hello")
	if err == nil {
		t.Error("SendInput should return error for SSH runner")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, should mention 'not supported'", err.Error())
	}
}

func TestSSHRunner_BuildSSHCmd_WithKey(t *testing.T) {
	r := NewSSHRunner(SSHConfig{
		Host:    "user@host",
		KeyFile: "/path/to/key",
	})

	cmd := r.buildSSHCmd("ls", "-la")
	args := cmd.Args

	// Should be: ssh -i /path/to/key user@host ls -la
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-i /path/to/key") {
		t.Errorf("args = %q, should contain key file flag", joined)
	}
	if !strings.Contains(joined, "user@host") {
		t.Errorf("args = %q, should contain host", joined)
	}
	if !strings.Contains(joined, "ls -la") {
		t.Errorf("args = %q, should contain remote command", joined)
	}
}

func TestSSHRunner_BuildSSHCmd_WithoutKey(t *testing.T) {
	r := NewSSHRunner(SSHConfig{Host: "user@host"})

	cmd := r.buildSSHCmd("whoami")
	args := cmd.Args

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-i") {
		t.Errorf("args = %q, should not contain -i when no key file", joined)
	}
	if !strings.Contains(joined, "user@host whoami") {
		t.Errorf("args = %q, should contain host and command", joined)
	}
}

func TestSSHRunner_BuildSSHCmd_WithExtraFlags(t *testing.T) {
	r := NewSSHRunner(SSHConfig{
		Host:       "user@host",
		ExtraFlags: []string{"-o", "StrictHostKeyChecking=no"},
	})

	cmd := r.buildSSHCmd("echo", "hello")
	joined := strings.Join(cmd.Args, " ")

	if !strings.Contains(joined, "-o StrictHostKeyChecking=no") {
		t.Errorf("args = %q, should contain extra flags", joined)
	}
}

func TestSSHRunner_Run_CreatesRemoteDir(t *testing.T) {
	var commands []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host", RemoteDir: "/opt/nxd"})
	pe := PreparedExecution{
		Command:     "claude -p 'test'",
		WorkDir:     t.TempDir(),
		SessionName: "test-ssh-run",
		SetupFiles:  map[string]string{},
		Env:         map[string]string{},
	}

	err := r.Run(pe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(commands) < 1 {
		t.Fatal("expected at least 1 command for mkdir")
	}

	// First command should create the remote directory
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "mkdir") && strings.Contains(cmd, "/opt/nxd/test-ssh-run") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("commands = %v, should contain mkdir for remote dir", commands)
	}
}

func TestSSHRunner_Run_EnvExports(t *testing.T) {
	var commands []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	pe := PreparedExecution{
		Command:     "claude -p 'test'",
		WorkDir:     t.TempDir(),
		SessionName: "test-ssh-env",
		SetupFiles:  map[string]string{},
		Env:         map[string]string{"MY_KEY": "my_value"},
	}

	err := r.Run(pe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The last command should contain the env export
	lastCmd := commands[len(commands)-1]
	if !strings.Contains(lastCmd, "MY_KEY") {
		t.Errorf("last command = %q, should contain env var export", lastCmd)
	}
}

func TestSSHRunner_Terminate_KillsBySession(t *testing.T) {
	var capturedArgs []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	err := r.Terminate("my-session")
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "pkill") || !strings.Contains(joined, "my-session") {
		t.Errorf("args = %q, should contain pkill with session ID", joined)
	}
}

func TestSSHRunner_ReadOutput_UsesTail(t *testing.T) {
	var capturedArgs []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.Command("echo", "output line")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host", RemoteDir: "/opt/nxd"})
	out, err := r.ReadOutput("my-session", 25)
	if err != nil {
		t.Fatalf("ReadOutput: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "tail") {
		t.Errorf("args = %q, should contain tail", joined)
	}
	if !strings.Contains(joined, "-25") {
		t.Errorf("args = %q, should contain line count", joined)
	}
	if !strings.Contains(joined, "/opt/nxd/my-session/agent.log") {
		t.Errorf("args = %q, should contain log path", joined)
	}
	if out == "" {
		t.Error("ReadOutput should return non-empty output")
	}
}

func TestSSHRunner_IsAlive_Running(t *testing.T) {
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	if !r.IsAlive("running-session") {
		t.Error("IsAlive should return true when pgrep succeeds")
	}
}

func TestSSHRunner_IsAlive_NotRunning(t *testing.T) {
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host"})
	if r.IsAlive("dead-session") {
		t.Error("IsAlive should return false when pgrep fails")
	}
}

func TestSSHRunner_ScpTo_WithKey(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = args
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{
		Host:    "user@host",
		KeyFile: "/path/to/key",
	})

	err := r.scpTo("/tmp/local.txt", "/remote/file.txt")
	if err != nil {
		t.Fatalf("scpTo: %v", err)
	}

	if capturedName != "scp" {
		t.Errorf("command = %q, want scp", capturedName)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "-i /path/to/key") {
		t.Errorf("args = %q, should contain key file", joined)
	}
	if !strings.Contains(joined, "/tmp/local.txt") {
		t.Errorf("args = %q, should contain local path", joined)
	}
	if !strings.Contains(joined, "user@host:/remote/file.txt") {
		t.Errorf("args = %q, should contain remote destination", joined)
	}
}

func TestSSHRunner_ScpTo_WithoutKey(t *testing.T) {
	var capturedArgs []string
	original := sshExecCommand
	sshExecCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return exec.Command("true")
	}
	defer func() { sshExecCommand = original }()

	r := NewSSHRunner(SSHConfig{Host: "user@host"})

	err := r.scpTo("/tmp/local.txt", "/remote/file.txt")
	if err != nil {
		t.Fatalf("scpTo: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if strings.Contains(joined, "-i") {
		t.Errorf("args = %q, should not contain -i when no key file", joined)
	}
}
