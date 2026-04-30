package runtime

import (
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

// NewRunnerFromConfig builds the appropriate Runner based on RuntimeConfig.
// Defaults to TmuxRunner when Runner is "" or "tmux". Returns an error if
// the runner type is unknown or required fields are missing.
func NewRunnerFromConfig(rc config.RuntimeConfig) (Runner, error) {
	switch rc.Runner {
	case "", "tmux":
		return NewTmuxRunner(), nil
	case "docker":
		if rc.Docker.Image == "" {
			return nil, fmt.Errorf("docker runner requires runtimes.<name>.docker.image")
		}
		return NewDockerRunner(DockerConfig{
			Image:      rc.Docker.Image,
			Network:    rc.Docker.Network,
			ExtraFlags: rc.Docker.ExtraFlags,
		}), nil
	case "ssh":
		if rc.SSH.Host == "" {
			return nil, fmt.Errorf("ssh runner requires runtimes.<name>.ssh.host")
		}
		return NewSSHRunner(SSHConfig{
			Host:       rc.SSH.Host,
			KeyFile:    rc.SSH.KeyFile,
			RemoteDir:  rc.SSH.RemoteDir,
			ExtraFlags: rc.SSH.ExtraFlags,
		}), nil
	default:
		return nil, fmt.Errorf("unknown runner type: %q (expected tmux, docker, or ssh)", rc.Runner)
	}
}
