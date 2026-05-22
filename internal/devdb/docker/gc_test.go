//go:build !integration

package docker_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestCollectOrphans_FiltersTemplatesAndActive(t *testing.T) {
	candidates := []string{
		"nxd-myproj-a8cbef1f-3a", // active
		"nxd-myproj-b9fde001-1c", // orphan
		"my-template-snapshot",   // wrong prefix
	}
	active := []string{"a8cbef1f-3a"}
	orphans := docker.CollectOrphansByName(candidates, "nxd", active)
	if len(orphans) != 1 || orphans[0] != "nxd-myproj-b9fde001-1c" {
		t.Errorf("orphans = %v, want [nxd-myproj-b9fde001-1c]", orphans)
	}
}

func TestCollectOrphans_EmptyActive_ReturnsAllPrefixed(t *testing.T) {
	candidates := []string{
		"nxd-x-a-1",
		"nxd-x-b-1",
		"other-x-c-1",
	}
	got := docker.CollectOrphansByName(candidates, "nxd", nil)
	if len(got) != 2 {
		t.Errorf("got %d orphans, want 2", len(got))
	}
}
