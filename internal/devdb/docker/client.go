package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// ClientConfig configures the Docker HTTP client.
// BaseURL defaults to the Unix-socket transport ("/var/run/docker.sock");
// tests pass an httptest URL instead.
type ClientConfig struct {
	BaseURL string
	Timeout time.Duration
}

// Client is a thin wrapper around the Docker Engine HTTP API.
// Only the subset of endpoints we need is exposed.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient returns a ready-to-use Docker client.
// Dial selection (in priority order):
//  1. cfg.BaseURL (explicit override; used by tests).
//  2. DOCKER_HOST env: unix://... dials the unix socket; tcp:// or http(s):// is used directly.
//  3. OS-specific fallback (see defaultDockerHost): /var/run/docker.sock on Unix,
//     tcp://localhost:2375 on Windows (requires Docker Desktop's "Expose daemon on
//     tcp://localhost:2375 without TLS" option, or a user-set DOCKER_HOST).
func NewClient(cfg ClientConfig) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	transport := &http.Transport{}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		host := os.Getenv("DOCKER_HOST")
		if host == "" {
			host = defaultDockerHost()
		}
		switch {
		case strings.HasPrefix(host, "unix://"):
			sock := strings.TrimPrefix(host, "unix://")
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "unix", sock)
			}
			baseURL = "http://docker"
		case strings.HasPrefix(host, "tcp://"):
			baseURL = strings.Replace(host, "tcp://", "http://", 1)
		case strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
			baseURL = host
		default:
			baseURL = host
		}
	}
	return &Client{
		httpClient: &http.Client{Transport: transport, Timeout: cfg.Timeout},
		baseURL:    baseURL,
	}
}

// ContainerState is a compact subset of Docker's inspect response.
type ContainerState struct {
	Exists  bool
	Running bool
}

// InspectContainer returns the state of a container by name or ID.
// 404 → Exists=false, no error.
func (c *Client) InspectContainer(ctx context.Context, name string) (ContainerState, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/containers/"+name+"/json", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ContainerState{}, fmt.Errorf("docker inspect: %w", devdb.ErrProviderDown)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ContainerState{Exists: false}, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return ContainerState{}, fmt.Errorf("docker inspect %s: status %d: %s", name, resp.StatusCode, body)
	}
	var body struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ContainerState{}, fmt.Errorf("docker inspect decode: %w", err)
	}
	return ContainerState{Exists: true, Running: body.State.Running}, nil
}

// CreateContainerSpec describes the host container we want to ensure exists.
type CreateContainerSpec struct {
	Name          string
	Image         string
	HostPort      int
	VolumeMount   string // host path → /var/lib/postgresql/data
	AdminPassword string
	Network       string
}

// CreateContainer creates the host Postgres container with port + volume + env.
// Returns the container ID.
func (c *Client) CreateContainer(ctx context.Context, spec CreateContainerSpec) (string, error) {
	body := map[string]any{
		"Image": spec.Image,
		"Env":   []string{"POSTGRES_PASSWORD=" + spec.AdminPassword, "POSTGRES_USER=postgres"},
		"HostConfig": map[string]any{
			"NetworkMode": spec.Network,
			"Binds":       []string{spec.VolumeMount + ":/var/lib/postgresql/data"},
			"PortBindings": map[string]any{
				"5432/tcp": []map[string]string{{"HostPort": fmt.Sprintf("%d", spec.HostPort)}},
			},
		},
		"ExposedPorts": map[string]any{"5432/tcp": map[string]any{}},
	}
	bb, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/containers/create?name="+spec.Name, bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker create: %w", devdb.ErrProviderDown)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("docker create %s: %d %s", spec.Name, resp.StatusCode, body)
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// StartContainer issues POST /containers/<id>/start. 204 and 304 (already started) both OK.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/containers/"+id+"/start", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker start: %w", devdb.ErrProviderDown)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 304 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker start %s: %d %s", id, resp.StatusCode, body)
	}
	return nil
}

// EnsureNetwork creates the named network if missing (idempotent).
// 201 created and 409 already-exists are both OK.
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	bb := []byte(fmt.Sprintf(`{"Name":%q,"CheckDuplicate":true}`, name))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/networks/create", bytes.NewReader(bb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker network create: %w", devdb.ErrProviderDown)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker network create %s: %d %s", name, resp.StatusCode, body)
	}
	return nil
}

// Ping verifies the Docker daemon is reachable. Unreachability is mapped to
// devdb.ErrProviderDown so callers can errors.Is.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/_ping", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker ping: %w", devdb.ErrProviderDown)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("docker ping %d: %w", resp.StatusCode, devdb.ErrProviderDown)
	}
	return nil
}
