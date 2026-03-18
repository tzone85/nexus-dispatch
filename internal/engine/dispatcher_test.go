package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newTestStores(t *testing.T) (state.EventStore, state.ProjectionStore, func()) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	cleanup := func() {
		es.Close()
		ps.Close()
	}
	return es, ps, cleanup
}

func TestDispatcher_DispatchWave(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate stories in projection (required for STORY_ASSIGNED update)
	for _, s := range []struct {
		id    string
		title string
		reqID string
	}{
		{"s-001", "Simple task", "r-001"},
		{"s-002", "Medium task", "r-001"},
		{"s-003", "Another simple", "r-001"},
	} {
		evt := state.NewEvent(state.EventStoryCreated, "tech-lead", s.id, map[string]any{
			"id":          s.id,
			"req_id":      s.reqID,
			"title":       s.title,
			"description": "desc",
			"complexity":  3,
		})
		ps.Project(evt)
	}

	cfg := config.DefaultConfig()
	dispatcher := engine.NewDispatcher(cfg, es, ps)

	stories := []engine.PlannedStory{
		{ID: "s-001", Title: "Simple task", Complexity: 2},
		{ID: "s-002", Title: "Medium task", Complexity: 5, DependsOn: []string{"s-001"}},
		{ID: "s-003", Title: "Another simple", Complexity: 3},
	}

	dag := graph.New()
	for _, s := range stories {
		dag.AddNode(s.ID)
	}
	dag.AddEdge("s-002", "s-001")

	// Wave 1: s-001 and s-003 are ready (no deps or deps satisfied)
	assignments, err := dispatcher.DispatchWave(dag, map[string]bool{}, "r-001", stories)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments in wave 1, got %d", len(assignments))
	}

	// Verify routing by complexity
	for _, a := range assignments {
		if a.StoryID == "s-001" && a.Role != agent.RoleJunior {
			t.Fatalf("s-001 (complexity 2) should route to junior, got %s", a.Role)
		}
		if a.StoryID == "s-003" && a.Role != agent.RoleJunior {
			t.Fatalf("s-003 (complexity 3) should route to junior, got %s", a.Role)
		}
	}

	// Verify branch naming
	for _, a := range assignments {
		expected := "nxd/" + a.StoryID
		if a.Branch != expected {
			t.Fatalf("expected branch %s, got %s", expected, a.Branch)
		}
	}

	// Wave 2: after s-001 and s-003 complete, s-002 becomes ready
	completed := map[string]bool{"s-001": true, "s-003": true}
	assignments2, err := dispatcher.DispatchWave(dag, completed, "r-001", stories)
	if err != nil {
		t.Fatalf("dispatch wave 2: %v", err)
	}
	if len(assignments2) != 1 {
		t.Fatalf("expected 1 assignment in wave 2, got %d", len(assignments2))
	}
	if assignments2[0].Role != agent.RoleIntermediate {
		t.Fatalf("s-002 (complexity 5) should route to intermediate, got %s", assignments2[0].Role)
	}
}

func TestDispatcher_EmptyWave(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	dispatcher := engine.NewDispatcher(config.DefaultConfig(), es, ps)
	dag := graph.New()
	dag.AddNode("s-001")

	assignments, err := dispatcher.DispatchWave(dag, map[string]bool{"s-001": true}, "r-001", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(assignments) != 0 {
		t.Fatalf("expected 0 assignments when all complete, got %d", len(assignments))
	}
}

func TestDispatcher_EventEmission(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate story in projection
	evt := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id":          "s-001",
		"req_id":      "r-001",
		"title":       "Task",
		"description": "desc",
		"complexity":  2,
	})
	ps.Project(evt)

	dispatcher := engine.NewDispatcher(config.DefaultConfig(), es, ps)
	stories := []engine.PlannedStory{
		{ID: "s-001", Title: "Task", Complexity: 2},
	}
	dag := graph.New()
	dag.AddNode("s-001")

	_, err := dispatcher.DispatchWave(dag, map[string]bool{}, "r-001", stories)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Verify AGENT_SPAWNED event
	spawnEvents, err := es.List(state.EventFilter{Type: state.EventAgentSpawned})
	if err != nil {
		t.Fatalf("list spawn events: %v", err)
	}
	if len(spawnEvents) != 1 {
		t.Fatalf("expected 1 AGENT_SPAWNED event, got %d", len(spawnEvents))
	}

	// Verify STORY_ASSIGNED event
	assignEvents, err := es.List(state.EventFilter{Type: state.EventStoryAssigned})
	if err != nil {
		t.Fatalf("list assign events: %v", err)
	}
	if len(assignEvents) != 1 {
		t.Fatalf("expected 1 STORY_ASSIGNED event, got %d", len(assignEvents))
	}

	// Verify projection updated
	story, err := ps.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "assigned" {
		t.Fatalf("expected story status 'assigned', got %s", story.Status)
	}
}

func TestDispatchWave_SequentialFirst(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate stories in projection
	for _, s := range []struct {
		id    string
		title string
	}{
		{"s-001", "Sequential config"},
		{"s-002", "Parallel A"},
		{"s-003", "Parallel B"},
	} {
		evt := state.NewEvent(state.EventStoryCreated, "tech-lead", s.id, map[string]any{
			"id": s.id, "req_id": "r-001", "title": s.title, "description": "d", "complexity": 3,
		})
		ps.Project(evt)
	}

	cfg := config.DefaultConfig()
	dispatcher := engine.NewDispatcher(cfg, es, ps)

	stories := []engine.PlannedStory{
		{ID: "s-001", Title: "Sequential config", Complexity: 2, OwnedFiles: []string{"package.json"}, WaveHint: "sequential"},
		{ID: "s-002", Title: "Parallel A", Complexity: 3, OwnedFiles: []string{"src/a.go"}, WaveHint: "parallel"},
		{ID: "s-003", Title: "Parallel B", Complexity: 3, OwnedFiles: []string{"src/b.go"}, WaveHint: "parallel"},
	}

	dag := graph.New()
	for _, s := range stories {
		dag.AddNode(s.ID)
	}

	// All 3 stories are ready, but s-001 is sequential — only it should dispatch
	assignments, err := dispatcher.DispatchWave(dag, map[string]bool{}, "r-001", stories)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment (sequential first), got %d", len(assignments))
	}
	if assignments[0].StoryID != "s-001" {
		t.Fatalf("expected sequential story s-001, got %s", assignments[0].StoryID)
	}
}

func TestDispatchWave_RejectsOverlap(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate stories in projection
	for _, s := range []struct {
		id    string
		title string
	}{
		{"s-001", "Story A"},
		{"s-002", "Story B"},
	} {
		evt := state.NewEvent(state.EventStoryCreated, "tech-lead", s.id, map[string]any{
			"id": s.id, "req_id": "r-001", "title": s.title, "description": "d", "complexity": 3,
		})
		ps.Project(evt)
	}

	cfg := config.DefaultConfig()
	dispatcher := engine.NewDispatcher(cfg, es, ps)

	stories := []engine.PlannedStory{
		{ID: "s-001", Title: "Story A", Complexity: 3, OwnedFiles: []string{"src/shared.go", "src/a.go"}, WaveHint: "parallel"},
		{ID: "s-002", Title: "Story B", Complexity: 3, OwnedFiles: []string{"src/shared.go", "src/b.go"}, WaveHint: "parallel"},
	}

	dag := graph.New()
	for _, s := range stories {
		dag.AddNode(s.ID)
	}

	// Both are ready and parallel, but they share src/shared.go — only 1 should dispatch
	assignments, err := dispatcher.DispatchWave(dag, map[string]bool{}, "r-001", stories)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment (overlap filtering), got %d", len(assignments))
	}
}
