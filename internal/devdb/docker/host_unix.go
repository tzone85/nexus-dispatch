//go:build !windows

package docker

// defaultDockerHost returns the platform default when DOCKER_HOST is unset.
// On Unix this is the canonical engine socket.
func defaultDockerHost() string {
	return "unix:///var/run/docker.sock"
}
