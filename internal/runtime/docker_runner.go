package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// allowedDockerExtraFlags is the ALLOWLIST of `docker run` flags an operator
// may add via runtimes.<name>.docker.extra_flags. Everything else is rejected
// — in particular anything that escalates privileges or reaches the host
// (--privileged, --cap-add, --device, --pid/--ipc/--userns/--uts/--cgroupns
// =host, --security-opt, --volume/-v, --mount, --network). Flags are matched
// by name; a value may follow as the next element or after "=".
var allowedDockerExtraFlags = map[string]bool{
	"--cpus": true, "--memory": true, "-m": true, "--memory-swap": true,
	"--cpu-shares": true, "--pids-limit": true, "--shm-size": true,
	"--read-only": true, "--tmpfs": true, "--ulimit": true,
	"--label": true, "-l": true, "--hostname": true, "-h": true,
	"--user": true, "-u": true, "--workdir": true, "--platform": true,
	"--pull": true, "--dns": true, "--stop-timeout": true, "--init": true,
	"--cap-drop": true, "--no-healthcheck": true, "--env": true, "-e": true,
}

// validDockerNetworks are the networks a runner may use. "host" is never
// allowed — it removes the network namespace entirely.
var validDockerNetworks = map[string]bool{"none": true, "bridge": true}

// validateDockerExtraFlags enforces the allowlist and rejects shell
// metacharacters. It walks flag/value pairs so `--memory 512m` and
// `--memory=512m` both validate.
func validateDockerExtraFlags(flags []string) error {
	expectValue := false
	for _, f := range flags {
		if strings.ContainsAny(f, ";&|`$<>\n\r") {
			return fmt.Errorf("docker flag contains shell metacharacters: %q", f)
		}
		if expectValue {
			expectValue = false
			continue
		}
		if !strings.HasPrefix(f, "-") {
			return fmt.Errorf("docker extra_flags entry %q is not a flag", f)
		}
		name, _, hasEq := strings.Cut(f, "=")
		if !allowedDockerExtraFlags[name] {
			return fmt.Errorf("docker flag not in allowlist: %q (allowed: resource limits, labels, user, workdir, tmpfs, read-only)", f)
		}
		expectValue = !hasEq && name != "--read-only" && name != "--init" && name != "--no-healthcheck"
	}
	if expectValue {
		return fmt.Errorf("docker extra_flags ends with a flag missing its value")
	}
	return nil
}

// DockerRunner executes agent sessions inside Docker containers.
type DockerRunner struct {
	image      string   // Docker image to use (e.g., "nxd-agent:latest")
	network    string   // Docker network: none (default) or bridge
	extraFlags []string // Additional flags passed to docker run (allowlisted)
}

// DockerConfig holds configuration for the Docker runner.
type DockerConfig struct {
	Image      string   `yaml:"image"`
	Network    string   `yaml:"network"`
	ExtraFlags []string `yaml:"extra_flags"`
}

// NewDockerRunner creates a DockerRunner with the given config. The network
// defaults to "none"; use "bridge" when the agent needs outbound access.
func NewDockerRunner(cfg DockerConfig) *DockerRunner {
	network := cfg.Network
	if network == "" {
		network = "none"
	}
	return &DockerRunner{
		image:      cfg.Image,
		network:    network,
		extraFlags: cfg.ExtraFlags,
	}
}

// dockerEnvFileRel is where the runner stages the --env-file (0600) inside
// the worktree. Removed after `docker run` returns.
const dockerEnvFileRel = ".nxd-prompts/docker.env"

// Run starts a Docker container with the prepared execution:
//
//	docker run -d --name <session> --network <net> -w /workspace
//	  -v <worktree>:/workspace --cap-drop ALL --security-opt no-new-privileges
//	  [--env-file <worktree>/.nxd-prompts/docker.env] [-v logdir:logdir]
//	  <extra flags> <image> sh -c <command>
//
// Secrets never appear in argv: the environment is passed via --env-file.
func (r *DockerRunner) Run(pe PreparedExecution) error {
	if !sanitize.ValidIdentifier(pe.SessionName) {
		return fmt.Errorf("invalid session name %q", pe.SessionName)
	}
	if !validDockerNetworks[r.network] {
		return fmt.Errorf("docker network %q not allowed (use none or bridge)", r.network)
	}
	if err := validateDockerExtraFlags(r.extraFlags); err != nil {
		return err
	}
	if err := pe.WriteSetupFiles(); err != nil {
		return err
	}

	args := []string{
		"run", "-d",
		"--name", pe.SessionName,
		"--network", r.network,
		"-w", "/workspace",
		"-v", pe.WorkDir + ":/workspace",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}

	if len(pe.Env) > 0 {
		content, err := RenderDockerEnvFile(pe.Env)
		if err != nil {
			return err
		}
		envFile := filepath.Join(pe.WorkDir, dockerEnvFileRel)
		if err := writeSecretFile(envFile, content); err != nil {
			return fmt.Errorf("write docker env file: %w", err)
		}
		defer func() { _ = os.Remove(envFile) }()
		args = append(args, "--env-file", envFile)
	}

	// Mount log directory if a log file is specified.
	if pe.LogFile != "" {
		logDir := filepath.Dir(pe.LogFile)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create log dir %s: %w", logDir, err)
		}
		args = append(args, "-v", logDir+":"+logDir)
	}

	args = append(args, r.extraFlags...)
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
	_, _ = stop.CombinedOutput()

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
