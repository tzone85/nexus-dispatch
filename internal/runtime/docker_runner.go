package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// dangerousDockerFlags are flags that can break out of the agent sandbox
// (privilege escalation, host filesystem access, capability grants). H12:
// reject these from extra_flags unless the operator explicitly opts in via
// a future allow_dangerous config field.
var dangerousDockerFlags = []string{
	"--privileged",
	"--cap-add",
	"--security-opt=label=disable",
	"--device",
	"--pid=host",
	"--pid=container",
	"--ipc=host",
	"--userns=host",
	"--uts=host",
	"--cgroupns=host",
}

// validateDockerExtraFlags rejects flags that escalate the container's
// privileges. Returns an error describing the first dangerous flag found.
func validateDockerExtraFlags(flags []string) error {
	for _, f := range flags {
		// Match exact and prefix-with-= forms (e.g. --cap-add=SYS_ADMIN).
		for _, danger := range dangerousDockerFlags {
			if f == danger || strings.HasPrefix(f, danger+"=") {
				return fmt.Errorf("dangerous docker flag rejected: %q (set runtimes.<name>.docker.allow_dangerous: true to override)", f)
			}
		}
		// Also reject anything containing shell metacharacters.
		if strings.ContainsAny(f, ";&|`$<>") {
			return fmt.Errorf("docker flag contains shell metacharacters: %q", f)
		}
	}
	return nil
}

// DockerRunner executes agent sessions inside Docker containers.
type DockerRunner struct {
	image      string   // Docker image to use (e.g., "nxd-agent:latest")
	network    string   // Docker network (default: "host")
	extraFlags []string // Additional flags passed to docker run
}

// DockerConfig holds configuration for the Docker runner.
type DockerConfig struct {
	Image      string   `yaml:"image"`
	Network    string   `yaml:"network"`
	ExtraFlags []string `yaml:"extra_flags"`
}

// NewDockerRunner creates a DockerRunner with the given config.
func NewDockerRunner(cfg DockerConfig) *DockerRunner {
	network := cfg.Network
	if network == "" {
		network = "host"
	}
	return &DockerRunner{
		image:      cfg.Image,
		network:    network,
		extraFlags: cfg.ExtraFlags,
	}
}

// Run starts a Docker container with the prepared execution.
func (r *DockerRunner) Run(pe PreparedExecution) error {
	// H13: validate session name before using it as a container name.
	if !sanitize.ValidIdentifier(pe.SessionName) {
		return fmt.Errorf("invalid session name %q", pe.SessionName)
	}
	// H12: reject dangerous extra flags before assembling the command.
	if err := validateDockerExtraFlags(r.extraFlags); err != nil {
		return err
	}
	// Write setup files to the work directory before mounting.
	for path, content := range pe.SetupFiles {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir for setup file %s: %w", path, err)
		}
		// H11-equiv: setup files often carry env vars / API keys; mode 0o600.
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write setup file %s: %w", path, err)
		}
	}

	args := []string{
		"run", "-d",
		"--name", pe.SessionName,
		"--network", r.network,
		"-w", "/workspace",
		"-v", pe.WorkDir + ":/workspace",
	}

	// Pass environment variables.
	for key, val := range pe.Env {
		args = append(args, "-e", key+"="+val)
	}

	// Mount log directory if a log file is specified.
	if pe.LogFile != "" {
		logDir := filepath.Dir(pe.LogFile)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir %s: %w", logDir, err)
		}
		args = append(args, "-v", logDir+":"+logDir)
	}

	// Append any extra flags from config.
	args = append(args, r.extraFlags...)

	// Image and command.
	args = append(args, r.image, "sh", "-c", pe.Command)

	cmd := execCommand("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Terminate stops and removes the Docker container.
func (r *DockerRunner) Terminate(sessionID string) error {
	// Stop the container (ignore error — it may already be stopped).
	stop := execCommand("docker", "stop", sessionID)
	stop.CombinedOutput()

	// Remove the container.
	rm := execCommand("docker", "rm", "-f", sessionID)
	out, err := rm.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SendInput is not supported for Docker containers.
// Agents in Docker run non-interactively.
func (r *DockerRunner) SendInput(sessionID string, input string) error {
	return fmt.Errorf("SendInput not supported for Docker runner — agents should run non-interactively")
}

// ReadOutput captures recent logs from the Docker container.
func (r *DockerRunner) ReadOutput(sessionID string, lines int) (string, error) {
	cmd := execCommand("docker", "logs", "--tail", fmt.Sprintf("%d", lines), sessionID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker logs: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// IsAlive checks if the Docker container is running.
func (r *DockerRunner) IsAlive(sessionID string) bool {
	cmd := execCommand("docker", "inspect", "-f", "{{.State.Running}}", sessionID)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// execCommand wraps exec.Command for testability (allows mocking in tests).
var execCommand = exec.Command
