package engine

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestRouteStory_Tier0_RoutesByComplexity(t *testing.T) {
	dir := t.TempDir()
	fs, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	defer fs.Close()
	cfg := config.Config{
		Routing: config.RoutingConfig{
			JuniorMaxComplexity:       3,
			IntermediateMaxComplexity: 5,
		},
	}
	d := &Dispatcher{
		eventStore: fs,
		config:     cfg,
	}
	role := d.routeStory(PlannedStory{ID: "s-001", Complexity: 2})
	if role != agent.RoleJunior {
		t.Errorf("expected RoleJunior at tier 0, got %s", role)
	}
}

func TestRouteStory_Tier1_RoutesSenior(t *testing.T) {
	dir := t.TempDir()
	fs, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	defer fs.Close()
	fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 0, "to_tier": 1,
	}))
	cfg := config.Config{
		Routing: config.RoutingConfig{
			JuniorMaxComplexity:       3,
			IntermediateMaxComplexity: 5,
		},
	}
	d := &Dispatcher{
		eventStore: fs,
		config:     cfg,
	}
	role := d.routeStory(PlannedStory{ID: "s-001", Complexity: 2})
	if role != agent.RoleSenior {
		t.Errorf("expected RoleSenior at tier 1, got %s", role)
	}
}
