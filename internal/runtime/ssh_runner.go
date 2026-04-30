package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// SSHRunner executes agent sessions on remote machines via SSH.
type SSHRunner struct {
	host       string   // user@host
	keyFile    string   // path to SSH key (optional)
	remoteDir  string   // remote working directory base
	extraFlags []string // additional SSH flags
}

// SSHConfig holds configuration for the SSH runner.
type SSHConfig struct {
	Host       string   `yaml:"host"`       // user@host
	KeyFile    string   `yaml:"key_file"`   // path to private key
	RemoteDir  string   `yaml:"remote_dir"` // remote base directory
	ExtraFlags []string `yaml:"extra_flags"`
}

// NewSSHRunner creates an SSHRunner with the given config.
func NewSSHRunner(cfg SSHConfig) *SSHRunner {
	remoteDir := cfg.RemoteDir
	if remoteDir == "" {
		remoteDir = "/tmp/nxd-agent"
	}
	return &SSHRunner{
		host:       cfg.Host,
		keyFile:    cfg.KeyFile,
		remoteDir:  remoteDir,
		extraFlags: cfg.ExtraFlags,
	}
}

// Run uploads setup files and starts the execution on the remote machine.
func (r *SSHRunner) Run(pe PreparedExecution) error {
	// H13: validate SessionName before using it in remote paths to prevent
	// path traversal on the SSH target (e.g. SessionName="../../etc").
	if !sanitize.ValidIdentifier(pe.SessionName) {
		return fmt.Errorf("invalid session name %q", pe.SessionName)
	}
	remoteWorkDir := filepath.Join(r.remoteDir, pe.SessionName)

	// Create remote directory.
	if err := r.sshExec("mkdir", "-p", remoteWorkDir); err != nil {
		return fmt.Errorf("create remote dir: %w", err)
	}

	// Upload setup files via scp.
	for localPath, content := range pe.SetupFiles {
		// H11: write setup files mode 0o600 — they may carry env-var values
		// (API keys, tokens). Mode 0o644 leaves them world-readable on
		// shared dev hosts for the duration of the SCP.
		tmpFile := filepath.Join(os.TempDir(), filepath.Base(localPath))
		if err := os.WriteFile(tmpFile, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write temp file: %w", err)
		}
		defer os.Remove(tmpFile)

		remotePath := filepath.Join(remoteWorkDir, filepath.Base(localPath))
		if err := r.scpTo(tmpFile, remotePath); err != nil {
			return fmt.Errorf("scp setup file %s: %w", localPath, err)
		}
	}

	// Build env exports.
	var envExports string
	for key, val := range pe.Env {
		envExports += fmt.Sprintf("export %s=%q; ", key, val)
	}

	// Execute command remotely in background (nohup + disown).
	remoteCmd := fmt.Sprintf("cd %s && %s nohup sh -c %q > /dev/null 2>&1 &",
		remoteWorkDir, envExports, pe.Command)

	if err := r.sshExec("sh", "-c", remoteCmd); err != nil {
		return fmt.Errorf("ssh exec: %w", err)
	}

	return nil
}

// Terminate kills the remote process by session ID pattern.
func (r *SSHRunner) Terminate(sessionID string) error {
	cmd := fmt.Sprintf("pkill -f %q 2>/dev/null || true", sessionID)
	return r.sshExec("sh", "-c", cmd)
}

// SendInput is not supported for SSH runner.
func (r *SSHRunner) SendInput(sessionID string, input string) error {
	return fmt.Errorf("SendInput not supported for SSH runner")
}

// ReadOutput reads the last N lines from the remote log file.
func (r *SSHRunner) ReadOutput(sessionID string, lines int) (string, error) {
	logPath := filepath.Join(r.remoteDir, sessionID, "agent.log")
	cmd := r.buildSSHCmd("tail", fmt.Sprintf("-%d", lines), logPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh tail: %w", err)
	}
	return string(out), nil
}

// IsAlive checks if the remote process is still running.
func (r *SSHRunner) IsAlive(sessionID string) bool {
	cmd := r.buildSSHCmd("pgrep", "-f", sessionID)
	return cmd.Run() == nil
}

// sshExec runs a command on the remote host and returns any error.
func (r *SSHRunner) sshExec(args ...string) error {
	cmd := r.buildSSHCmd(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// buildSSHCmd constructs an SSH command with the runner's config.
func (r *SSHRunner) buildSSHCmd(remoteArgs ...string) *exec.Cmd {
	sshArgs := []string{}
	if r.keyFile != "" {
		sshArgs = append(sshArgs, "-i", r.keyFile)
	}
	sshArgs = append(sshArgs, r.extraFlags...)
	sshArgs = append(sshArgs, r.host)
	sshArgs = append(sshArgs, remoteArgs...)
	return sshExecCommand("ssh", sshArgs...)
}

// scpTo uploads a local file to the remote host.
func (r *SSHRunner) scpTo(localPath, remotePath string) error {
	scpArgs := []string{}
	if r.keyFile != "" {
		scpArgs = append(scpArgs, "-i", r.keyFile)
	}
	scpArgs = append(scpArgs, localPath, r.host+":"+remotePath)
	cmd := sshExecCommand("scp", scpArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// sshExecCommand wraps exec.Command for testability (allows mocking in tests).
var sshExecCommand = exec.Command
