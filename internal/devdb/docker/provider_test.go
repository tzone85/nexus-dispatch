//go:build !integration

package docker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestProvider_Name(t *testing.T) {
	p := docker.NewProvider(docker.Config{Image: "postgres:16", HostPortRange: "5500-5500"})
	if p.Name() != "docker" {
		t.Errorf("Name = %q, want docker", p.Name())
	}
}

func TestProvider_SatisfiesInterface(t *testing.T) {
	var _ devdb.Provider = docker.NewProvider(docker.Config{HostPortRange: "5500-5599"})
}

func TestProvider_BootstrapFlow_WithMockDaemon(t *testing.T) {
	inspectCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_ping":
			w.WriteHeader(200)
		case r.URL.Path == "/networks/create":
			w.WriteHeader(409) // already exists is OK
		case r.URL.Path == "/containers/nxd-devdb-pg16/json" && r.Method == "GET":
			inspectCalls++
			if inspectCalls == 1 {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"message":"no such container"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"State":{"Running":true}}`))
		case r.URL.Path == "/containers/create":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"Id":"container-abc"}`))
		case r.URL.Path == "/containers/container-abc/start":
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	p := docker.NewProviderWithClient(
		docker.Config{
			ContainerName:  "nxd-devdb-pg16",
			HostPortRange:  "5500-5500",
			TemplateVolume: dir,
			Image:          "postgres:16",
		},
		docker.NewClient(docker.ClientConfig{BaseURL: srv.URL}),
	)
	if err := p.EnsureContainer(context.Background()); err != nil {
		t.Fatalf("EnsureContainer: %v", err)
	}
}
