package engine

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestClassifyStories(t *testing.T) {
	stories := []state.Story{
		{ID: "merged", Status: "merged"},
		{ID: "split", Status: "split"},
		{ID: "pr", Status: "pr_submitted"},
		{ID: "mr", Status: "merge_ready"},
		{ID: "draft", Status: "draft"},
		{ID: "prog", Status: "in_progress"},
	}
	got := classifyStories(stories)

	if !got.completed["merged"] || !got.completed["split"] {
		t.Fatalf("merged and split must count as completed: %v", got.completed)
	}
	if got.completed["pr"] || got.completed["mr"] {
		t.Fatalf("stories awaiting PR merge must NOT count as completed: %v", got.completed)
	}
	if len(got.awaitingMerge) != 2 || got.awaitingMerge[0] != "pr" || got.awaitingMerge[1] != "mr" {
		t.Fatalf("awaitingMerge = %v, want [pr mr]", got.awaitingMerge)
	}
	if got.pending != 2 {
		t.Fatalf("pending = %d, want 2 (draft + in_progress)", got.pending)
	}
	if got.allMerged() {
		t.Fatal("allMerged must be false while anything is pending or awaiting merge")
	}

	only := classifyStories([]state.Story{{ID: "a", Status: "merged"}, {ID: "b", Status: "split"}})
	if !only.allMerged() {
		t.Fatal("allMerged must be true when every story is merged/split")
	}
	prOnly := classifyStories([]state.Story{{ID: "a", Status: "merged"}, {ID: "b", Status: "pr_submitted"}})
	if prOnly.allMerged() || !prOnly.onlyAwaitingMerge() {
		t.Fatalf("merged + pr_submitted is 'awaiting merge', not done: %+v", prOnly)
	}

	planned := []PlannedStory{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	d := prOnly.dispatchable(planned)
	if len(d) != 2 || d[0].ID != "a" || d[1].ID != "c" {
		t.Fatalf("dispatchable must drop stories awaiting merge, got %v", d)
	}
	if !prOnly.isAwaitingMerge("b") || prOnly.isAwaitingMerge("a") {
		t.Fatal("isAwaitingMerge mismatch")
	}
	if got := only.dispatchable(planned); len(got) != 3 {
		t.Fatalf("with nothing awaiting merge dispatchable must be the full list, got %d", len(got))
	}
}

// seedWaveReq creates a requirement and stories with the given statuses.
func seedWaveReq(t *testing.T, es state.EventStore, ps state.ProjectionStore, reqID string, statuses map[string]string) {
	t.Helper()
	project := func(evt state.Event) {
		t.Helper()
		if err := es.Append(evt); err != nil {
			t.Fatal(err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatal(err)
		}
	}
	project(state.NewEvent(state.EventReqSubmitted, "user", "", map[string]any{"id": reqID, "title": "t", "description": "d"}))
	for id, status := range statuses {
		project(state.NewEvent(state.EventStoryCreated, "tl", id, map[string]any{
			"id": id, "req_id": reqID, "title": id, "description": "d", "complexity": 1,
		}))
		switch status {
		case "pr_submitted":
			project(state.NewEvent(state.EventStoryQAPassed, "qa", id, nil))
		case "merged":
			project(state.NewEvent(state.EventStoryMerged, "merger", id, map[string]any{"pr_number": 1}))
		}
	}
}

func newWaveMonitor(t *testing.T, es state.EventStore, ps state.ProjectionStore) *Monitor {
	t.Helper()
	cfg := config.DefaultConfig()
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	m.SetAutoResume(NewDispatcher(cfg, es, ps), NewExecutor(reg, cfg, es, ps, nil))
	return m
}

// TestDispatchNextWave_PRSubmittedDoesNotUnblockDependents: a dependent of a
// story whose PR is still open must not be dispatched — its parent's code is
// not on the base branch yet.
func TestDispatchNextWave_PRSubmittedDoesNotUnblockDependents(t *testing.T) {
	es, ps := newAttemptStores(t)
	seedWaveReq(t, es, ps, "r-pr", map[string]string{"parent": "pr_submitted", "child": "draft"})
	m := newWaveMonitor(t, es, ps)

	dag := graph.New()
	dag.AddNode("parent")
	dag.AddNode("child")
	dag.AddEdge("child", "parent")
	rc := &RunContext{ReqID: "r-pr", DAG: dag, PlannedStories: []PlannedStory{
		{ID: "parent", Title: "parent", Complexity: 1},
		{ID: "child", Title: "child", Complexity: 1, DependsOn: []string{"parent"}},
	}}

	agents := m.dispatchNextWave(context.Background(), rc, t.TempDir())
	if len(agents) != 0 {
		t.Fatalf("expected no agents, got %d", len(agents))
	}
	assigned, _ := es.List(state.EventFilter{Type: state.EventStoryAssigned})
	if len(assigned) != 0 {
		t.Fatalf("child must not be dispatched on a pr_submitted parent, got %d STORY_ASSIGNED", len(assigned))
	}
	pending, _ := es.List(state.EventFilter{Type: state.EventReqPendingReview})
	if len(pending) != 1 {
		t.Fatalf("expected 1 REQ_PENDING_REVIEW, got %d", len(pending))
	}
	payload := state.DecodePayload(pending[0].Payload)
	if payload["req_id"] != "r-pr" {
		t.Fatalf("payload req_id = %v", payload["req_id"])
	}
	open, _ := payload["open_pr_story_ids"].([]any)
	if len(open) != 1 || open[0] != "parent" {
		t.Fatalf("open_pr_story_ids = %v, want [parent]", payload["open_pr_story_ids"])
	}
	blocked, _ := payload["blocked_story_ids"].([]any)
	if len(blocked) != 1 || blocked[0] != "child" {
		t.Fatalf("blocked_story_ids = %v, want [child]", payload["blocked_story_ids"])
	}
	stalled, _ := es.List(state.EventFilter{Type: "PIPELINE_STALLED"})
	if len(stalled) != 0 {
		t.Fatal("waiting on a PR is not a stall")
	}
	req, _ := ps.GetRequirement("r-pr")
	if req.Status == "completed" || req.Status == "pending_review" {
		t.Fatalf("requirement status must stay in progress, got %q", req.Status)
	}
}

// TestDispatchNextWave_AllPRSubmittedIsNotCompleted: a requirement whose
// stories are all pr_submitted is awaiting merge, not done — no REQ_COMPLETED.
func TestDispatchNextWave_AllPRSubmittedIsNotCompleted(t *testing.T) {
	es, ps := newAttemptStores(t)
	seedWaveReq(t, es, ps, "r-open", map[string]string{"a": "merged", "b": "pr_submitted"})
	m := newWaveMonitor(t, es, ps)

	dag := graph.New()
	dag.AddNode("a")
	dag.AddNode("b")
	rc := &RunContext{ReqID: "r-open", DAG: dag, PlannedStories: []PlannedStory{{ID: "a"}, {ID: "b"}}}

	if agents := m.dispatchNextWave(context.Background(), rc, t.TempDir()); len(agents) != 0 {
		t.Fatalf("expected no agents, got %d", len(agents))
	}
	if done, _ := es.List(state.EventFilter{Type: state.EventReqCompleted}); len(done) != 0 {
		t.Fatal("REQ_COMPLETED must not be emitted while a PR is open")
	}
	pending, _ := es.List(state.EventFilter{Type: state.EventReqPendingReview})
	if len(pending) != 1 {
		t.Fatalf("expected 1 REQ_PENDING_REVIEW, got %d", len(pending))
	}
	open, _ := state.DecodePayload(pending[0].Payload)["open_pr_story_ids"].([]any)
	if len(open) != 1 || open[0] != "b" {
		t.Fatalf("open_pr_story_ids = %v, want [b]", open)
	}
}

// TestDispatchNextWave_AllMergedCompletes keeps the happy path: merged +
// split stories complete the requirement (legacy advisory path, no gate).
func TestDispatchNextWave_AllMergedCompletes(t *testing.T) {
	es, ps := newAttemptStores(t)
	seedWaveReq(t, es, ps, "r-done", map[string]string{"a": "merged"})
	m := newWaveMonitor(t, es, ps)
	rc := &RunContext{ReqID: "r-done", DAG: graph.New(), PlannedStories: []PlannedStory{{ID: "a"}}}

	m.dispatchNextWave(context.Background(), rc, t.TempDir())
	if done, _ := es.List(state.EventFilter{Type: state.EventReqCompleted}); len(done) != 1 {
		t.Fatalf("expected REQ_COMPLETED once all stories merged, got %d", len(done))
	}
}

// TestDispatchNextWave_StallWithoutOpenPRs keeps the stall diagnosis when
// nothing is dispatchable and no PR explains the wait.
func TestDispatchNextWave_StallWithoutOpenPRs(t *testing.T) {
	es, ps := newAttemptStores(t)
	seedWaveReq(t, es, ps, "r-stall", map[string]string{"orphan": "draft"})
	m := newWaveMonitor(t, es, ps)
	dag := graph.New()
	dag.AddNode("orphan")
	dag.AddEdge("orphan", "missing-dep")
	rc := &RunContext{ReqID: "r-stall", DAG: dag, PlannedStories: []PlannedStory{{ID: "orphan", DependsOn: []string{"missing-dep"}}}}

	m.dispatchNextWave(context.Background(), rc, t.TempDir())
	if stalled, _ := es.List(state.EventFilter{Type: "PIPELINE_STALLED"}); len(stalled) != 1 {
		t.Fatalf("expected PIPELINE_STALLED, got %d", len(stalled))
	}
	if pending, _ := es.List(state.EventFilter{Type: state.EventReqPendingReview}); len(pending) != 0 {
		t.Fatal("no open PR → no REQ_PENDING_REVIEW")
	}
}
