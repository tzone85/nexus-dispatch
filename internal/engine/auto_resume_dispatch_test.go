package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestDispatchNextWave_EmitsStageCompleted guards against the regression
// caught during the live test sweep: when the monitor's auto-resume path
// dispatches the next wave, it MUST emit a STAGE_COMPLETED event with
// stage="dispatch". Without the event, the metrics reporter and dashboard
// see only the initial dispatch from cli/resume.go and miss every wave
// after that.
//
// The test sets up a minimal Monitor with real stores, primes a single
// requirement + story so dispatchNextWave doesn't bail early, and asserts
// a STAGE_COMPLETED event lands in the event store.
func TestDispatchNextWave_EmitsStageCompleted(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	defer ps.Close()

	// Seed a requirement (status=in_progress) and one story so the
	// auto-resume path can find work but doesn't actually need an
	// executor (dispatch returns assignments and we stop there — no
	// SpawnAll because m.executor is nil for this test).
	reqEvt := state.NewEvent(state.EventReqSubmitted, "test", "", map[string]any{
		"id":          "r-1",
		"title":       "test req",
		"description": "test",
	})
	ps.Project(reqEvt)

	// Seed an in-progress story whose dependency is unmet so the
	// dispatcher returns no assignments — that lets dispatchNextWave
	// reach the STAGE_COMPLETED emit point without needing an executor.
	storyEvt := state.NewEvent(state.EventStoryCreated, "planner", "s-2", map[string]any{
		"id":          "s-2",
		"req_id":      "r-1",
		"title":       "blocked",
		"description": "",
		"complexity":  1,
	})
	ps.Project(storyEvt)

	cfg := config.DefaultConfig()
	dispatcher := NewDispatcher(cfg, es, ps)

	// DAG with s-2 depending on a non-existent s-1; ReadyNodes returns
	// nothing so DispatchWave produces zero assignments.
	dag := graph.New()
	dag.AddNode("s-2")
	dag.AddEdge("s-2", "s-1")

	rc := &RunContext{
		ReqID: "r-1",
		PlannedStories: []PlannedStory{
			{ID: "s-2", Title: "blocked", Complexity: 1, DependsOn: []string{"s-1"}},
		},
		DAG:        dag,
		WaveNumber: 1,
	}

	m := &Monitor{
		eventStore: es,
		projStore:  ps,
		config:     cfg,
		dispatcher: dispatcher,
		// executor and escalation deliberately nil — we only exercise
		// the dispatch path.
		escalation: NewEscalationMachine(es, cfg.Routing),
	}

	_ = m.dispatchNextWave(context.Background(), rc, "")

	// Walk the event store for STAGE_COMPLETED. We can't assume it's
	// the only event (dispatcher emits STORY_ASSIGNED), so we filter.
	events, err := es.List(state.EventFilter{Type: state.EventStageCompleted})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected STAGE_COMPLETED event from dispatchNextWave, got none")
	}

	payload := state.DecodePayload(events[0].Payload)
	if payload["stage"] != "dispatch" {
		t.Errorf("stage = %v, want dispatch", payload["stage"])
	}
	if payload["outcome"] != "success" {
		t.Errorf("outcome = %v, want success", payload["outcome"])
	}
	if events[0].AgentID != "auto-resume" {
		t.Errorf("agent_id = %q, want auto-resume", events[0].AgentID)
	}
	if _, ok := payload["duration_ms"]; !ok {
		t.Errorf("payload missing duration_ms: %v", payload)
	}

}
