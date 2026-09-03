package engine

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

func newAttemptStores(t *testing.T) (*state.FileStore, *state.SQLiteStore) {
	t.Helper()
	es, err := state.NewFileStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { es.Close(); ps.Close() })
	return es, ps
}

func TestNewAttemptID_UniqueAndStoryScoped(t *testing.T) {
	a := newAttemptID("s-1")
	b := newAttemptID("s-1")
	if a == b {
		t.Fatalf("two attempts for the same story must differ, both %q", a)
	}
	if !strings.HasPrefix(a, "s-1-a") {
		t.Fatalf("attempt id %q should be prefixed with the story id", a)
	}
}

func TestDispatchWave_StampsUniqueAttemptIDs(t *testing.T) {
	es, ps := newAttemptStores(t)
	cfg := config.DefaultConfig()
	d := NewDispatcher(cfg, es, ps)

	dag := graph.New()
	dag.AddNode("s-1")
	dag.AddNode("s-2")
	stories := []PlannedStory{
		{ID: "s-1", Title: "one", Complexity: 1, OwnedFiles: []string{"a.go"}},
		{ID: "s-2", Title: "two", Complexity: 1, OwnedFiles: []string{"b.go"}},
	}

	assignments, err := d.DispatchWave(dag, map[string]bool{}, "r-1", stories, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(assignments))
	}
	if assignments[0].AttemptID == "" || assignments[1].AttemptID == "" {
		t.Fatalf("assignments must carry attempt ids: %+v", assignments)
	}
	if assignments[0].AttemptID == assignments[1].AttemptID {
		t.Fatalf("attempt ids must be unique per dispatch, both %q", assignments[0].AttemptID)
	}

	for _, a := range assignments {
		for _, typ := range []state.EventType{state.EventAgentSpawned, state.EventStoryAssigned} {
			events, err := es.List(state.EventFilter{Type: typ, StoryID: a.StoryID})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatalf("%s for %s: got %d events, want 1", typ, a.StoryID, len(events))
			}
			if events[0].AttemptID != a.AttemptID {
				t.Fatalf("%s for %s: AttemptID %q, want %q", typ, a.StoryID, events[0].AttemptID, a.AttemptID)
			}
			if got := state.DecodePayload(events[0].Payload)["attempt_id"]; got != a.AttemptID {
				t.Fatalf("%s for %s: payload attempt_id %v, want %q", typ, a.StoryID, got, a.AttemptID)
			}
		}
	}

	// A second dispatch of the same story (re-dispatch after a reset) must
	// mint a fresh attempt id.
	again, err := d.DispatchWave(dag, map[string]bool{}, "r-1", stories[:1], 2)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].AttemptID == assignments[0].AttemptID {
		t.Fatalf("re-dispatch reused attempt id %q", again[0].AttemptID)
	}
}

func TestNativeAgentCompleted_AttemptScoped(t *testing.T) {
	es, _ := newAttemptStores(t)

	// Attempt 1 completed (its STORY_COMPLETED is in the store), attempt 2
	// is the one currently being polled.
	if err := es.Append(state.NewEventForAttempt(state.EventStoryCompleted, "agent-1", "s-1", "s-1-a1", nil)); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		attemptID string
		want      bool
	}{
		{"stale completion from attempt 1 must not complete attempt 2", "s-1-a2", false},
		{"attempt 1 sees its own completion", "s-1-a1", true},
		{"legacy caller without attempt falls back to any completion", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeAgentCompleted(es, "s-1", tc.attemptID); got != tc.want {
				t.Fatalf("nativeAgentCompleted(%q) = %v, want %v", tc.attemptID, got, tc.want)
			}
		})
	}
}

func TestNativeAgentCompleted_NoEvents(t *testing.T) {
	es, _ := newAttemptStores(t)
	if nativeAgentCompleted(es, "s-none", "s-none-a1") {
		t.Fatal("expected false with no events")
	}
	if nativeAgentCompleted(es, "s-none", "") {
		t.Fatal("expected false with no events (legacy)")
	}
}

func TestSpawn_CLIRuntime_StampsAttemptOnStoryStarted(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := newAttemptStores(t)

	stop := tmux.SetTestExec(
		func(args ...string) error { return nil },
		func(args ...string) (string, error) { return "", nil },
	)
	defer stop()

	runtimeCfg := map[string]config.RuntimeConfig{
		"aider": {Command: "true", Models: []string{"any-model"}},
	}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "any-model"
	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(reg, cfg, es, ps, nil)

	a := Assignment{
		StoryID: "STORY-ATT", ReqID: "REQ-1", Role: agent.RoleJunior,
		Branch: "story/STORY-ATT", AgentID: "junior-001", SessionName: "nxd-att",
		AttemptID: "STORY-ATT-a1",
	}
	res := e.spawn(context.Background(), repo, a, PlannedStory{ID: "STORY-ATT", Title: "t"}, nil, nil)
	if res.Error != nil {
		t.Fatalf("spawn: %v", res.Error)
	}
	started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted, AttemptID: "STORY-ATT-a1"})
	if len(started) != 1 {
		t.Fatalf("expected 1 STORY_STARTED stamped with the attempt, got %d", len(started))
	}
	if state.DecodePayload(started[0].Payload)["attempt_id"] != "STORY-ATT-a1" {
		t.Fatalf("payload missing attempt_id: %s", started[0].Payload)
	}
}

func TestSpawnNative_StampsAttemptOnLifecycleEvents(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := newAttemptStores(t)

	runtimeCfg := map[string]config.RuntimeConfig{
		"gemma": {Native: true, MaxIterations: 2, Models: []string{"gemma4"}},
	}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "gemma4"
	cfg.QA.SuccessCriteria = nil
	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(reg, cfg, es, ps, nil)
	// One plain-text response with no tool calls ends the native loop.
	e.SetLLMClient(llm.NewReplayClient(llm.CompletionResponse{Content: "done"}))

	a := Assignment{
		StoryID: "STORY-NAT", ReqID: "REQ-1", Role: agent.RoleJunior,
		Branch: "story/STORY-NAT", AgentID: "junior-nat", SessionName: "nxd-nat",
		AttemptID: "STORY-NAT-a7",
	}
	res := e.spawn(context.Background(), repo, a, PlannedStory{ID: "STORY-NAT", Title: "t"}, nil, e.buildNativeClient())
	if res.Error != nil {
		t.Fatalf("spawn: %v", res.Error)
	}
	if err := e.WaitForNativeShutdown(10 * time.Second); err != nil {
		t.Fatalf("native goroutine did not finish: %v", err)
	}

	for _, typ := range []state.EventType{state.EventStoryStarted, state.EventStoryProgress, state.EventStoryCompleted} {
		all, _ := es.List(state.EventFilter{Type: typ, StoryID: "STORY-NAT"})
		if len(all) == 0 {
			t.Fatalf("expected at least one %s event", typ)
		}
		for _, evt := range all {
			if evt.AttemptID != "STORY-NAT-a7" {
				t.Fatalf("%s event %s AttemptID=%q, want STORY-NAT-a7", typ, evt.ID, evt.AttemptID)
			}
		}
	}
}

func TestPollOnce_CLIRuntime_StampsAttemptOnStoryCompleted(t *testing.T) {
	es, ps := newAttemptStores(t)
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{
		"test-runtime": {
			Command: "echo", Args: []string{"test"}, Models: []string{"test-model"},
			Detection: config.RuntimeDetection{IdlePattern: `\$\s*$`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es)
	mon := NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)

	active := map[string]ActiveAgent{
		"nxd-test-1": {
			Assignment: Assignment{
				StoryID: "s-001", AgentID: "agent-1", SessionName: "nxd-test-1",
				Branch: "nxd/s-001", AttemptID: "s-001-a3",
			},
			RuntimeName:  "test-runtime",
			WorktreePath: t.TempDir(),
		},
	}
	// Without tmux the session does not exist → StatusTerminated → completion.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pipeline goroutine bails out immediately on a cancelled context
	var wg sync.WaitGroup
	mon.pollOnce(ctx, &wg, active, t.TempDir())
	wg.Wait()

	completed, _ := es.List(state.EventFilter{Type: state.EventStoryCompleted, StoryID: "s-001"})
	if len(completed) != 1 {
		t.Fatalf("expected 1 STORY_COMPLETED, got %d", len(completed))
	}
	if completed[0].AttemptID != "s-001-a3" {
		t.Fatalf("STORY_COMPLETED AttemptID=%q, want s-001-a3", completed[0].AttemptID)
	}
	if len(active) != 0 {
		t.Fatalf("finished agent should be removed from active, still %d", len(active))
	}
}
