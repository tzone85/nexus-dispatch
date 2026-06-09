//go:build windows

package docker

// defaultDockerHost returns the platform default when DOCKER_HOST is unset.
// Windows uses the Docker Desktop named pipe `npipe:////./pipe/docker_engine`
// natively, but Go's stdlib cannot dial named pipes without an extra
// dependency. We default to the TCP endpoint (which Docker Desktop exposes
// when "Expose daemon on tcp://localhost:2375 without TLS" is enabled) and
// document that users without that option must set DOCKER_HOST explicitly.
func defaultDockerHost() string {
	return "tcp://localhost:2375"
}
