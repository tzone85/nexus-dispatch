package docker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestClient_Ping_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_ping" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("OK"))
	}))
	defer srv.Close()

	c := docker.NewClient(docker.ClientConfig{BaseURL: srv.URL})
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestClient_Ping_Unreachable(t *testing.T) {
	c := docker.NewClient(docker.ClientConfig{BaseURL: "http://127.0.0.1:1"})
	err := c.Ping(context.Background())
	if !errors.Is(err, devdb.ErrProviderDown) {
		t.Errorf("Ping(unreachable) = %v, want ErrProviderDown", err)
	}
}

func TestClient_InspectContainer_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/nxd-devdb-pg16/json" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"no such container"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := docker.NewClient(docker.ClientConfig{BaseURL: srv.URL})
	state, err := c.InspectContainer(context.Background(), "nxd-devdb-pg16")
	if err != nil {
		t.Errorf("InspectContainer NotFound should not error, got %v", err)
	}
	if state.Exists {
		t.Errorf("expected Exists=false")
	}
}

func TestClient_InspectContainer_Running(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/nxd-devdb-pg16/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"State":{"Running":true}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := docker.NewClient(docker.ClientConfig{BaseURL: srv.URL})
	state, err := c.InspectContainer(context.Background(), "nxd-devdb-pg16")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Exists || !state.Running {
		t.Errorf("state = %+v, want Exists=true Running=true", state)
	}
}
