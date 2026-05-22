//go:build integration

package docker_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

// newIntegrationProvider returns a bootstrapped docker.Provider OR skips the
// test if Docker is unreachable / bootstrap fails. Tests must defer Delete on
// any DBs they create to avoid leaks.
func newIntegrationProvider(t *testing.T) *docker.Provider {
	t.Helper()
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skipf("docker socket not available: %v", err)
	}
	dir := t.TempDir()
	p := docker.NewProvider(docker.Config{
		ContainerName:  "nxd-devdb-test-pg16",
		HostPortRange:  "5599-5599",
		TemplateVolume: dir,
		Image:          "postgres:16",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := p.EnsureContainer(ctx); err != nil {
		t.Skipf("EnsureContainer failed (skipping integration): %v", err)
	}
	return p
}

func TestIntegration_Provider_CreateDelete(t *testing.T) {
	p := newIntegrationProvider(t)
	ctx := context.Background()

	name := "nxd-int-test-create-1"
	db, err := p.Create(ctx, devdb.CreateOpts{Name: name})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = p.Delete(ctx, db.ID) }()

	list, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range list {
		if d.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DB %q not in list after Create", name)
	}
}

func TestIntegration_Provider_ForkFromTemplate(t *testing.T) {
	p := newIntegrationProvider(t)
	ctx := context.Background()

	tplName := "nxd-int-test-template"
	if _, err := p.Create(ctx, devdb.CreateOpts{Name: tplName}); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	defer func() { _ = p.Delete(ctx, tplName) }()

	forkName := "nxd-int-test-fork-1"
	if _, err := p.Fork(ctx, tplName, devdb.CreateOpts{Name: forkName}); err != nil {
		t.Fatalf("Fork: %v", err)
	}
	defer func() { _ = p.Delete(ctx, forkName) }()
}
