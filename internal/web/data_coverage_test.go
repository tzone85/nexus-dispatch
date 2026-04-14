package web

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestBuildSnapshot_AllPipelineBuckets(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Create stories and transition them to different statuses
	storyReview := seedStoryWithID(t, s, reqID, "s-review")
	storyQA := seedStoryWithID(t, s, reqID, "s-qa")
	storyPR := seedStoryWithID(t, s, reqID, "s-pr")
	storyMerged := seedStoryWithID(t, s, reqID, "s-merged")
	storySplit := seedStoryWithID(t, s, reqID, "s-split")

	// Transition to review
	emitAndProject(t, s, state.EventStoryCompleted, "agent-1", storyReview, nil)

	// Transition to qa
	emitAndProject(t, s, state.EventStoryReviewPassed, "agent-1", storyQA, nil)

	// Transition to pr_submitted
	emitAndProject(t, s, state.EventStoryQAPassed, "agent-1", storyPR, nil)

	// Transition to merged
	emitAndProject(t, s, state.EventStoryMerged, "agent-1", storyMerged, nil)

	// Transition to split
	emitAndProject(t, s, state.EventStorySplit, "agent-1", storySplit, nil)

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if snap.Pipeline.Review != 1 {
		t.Errorf("expected 1 review, got %d", snap.Pipeline.Review)
	}
	if snap.Pipeline.QA != 1 {
		t.Errorf("expected 1 qa, got %d", snap.Pipeline.QA)
	}
	if snap.Pipeline.PR != 1 {
		t.Errorf("expected 1 pr, got %d", snap.Pipeline.PR)
	}
	if snap.Pipeline.Merged != 1 {
		t.Errorf("expected 1 merged, got %d", snap.Pipeline.Merged)
	}
	if snap.Pipeline.Split != 1 {
		t.Errorf("expected 1 split, got %d", snap.Pipeline.Split)
	}
}

func TestBuildSnapshot_IncludesDAG(t *testing.T) {
	s := newTestServer(t)

	dag := &graph.DAGExport{
		Nodes: []graph.NodeExport{{ID: "s1", Wave: 0}},
		Edges: []graph.EdgeExport{{From: "s1", To: "s2"}},
		Waves: [][]string{{"s1"}, {"s2"}},
	}
	s.SetDAG(dag)

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if snap.DAG == nil {
		t.Fatal("expected DAG to be set")
	}
	if len(snap.DAG.Nodes) != 1 {
		t.Errorf("expected 1 DAG node, got %d", len(snap.DAG.Nodes))
	}
}

func TestSnapshotJSON_ReturnsValidJSON(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	seedStory(t, s, reqID)

	data, err := s.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}

	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(snap.Requirements) != 1 {
		t.Errorf("expected 1 requirement in JSON, got %d", len(snap.Requirements))
	}
}

func TestSnapshotJSON_EmptyState(t *testing.T) {
	s := newTestServer(t)

	data, err := s.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}

	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if snap.DAG != nil {
		t.Error("expected nil DAG in empty snapshot")
	}
}

func TestSetDAG(t *testing.T) {
	s := newTestServer(t)

	if s.dagExport != nil {
		t.Fatal("expected nil dagExport initially")
	}

	dag := &graph.DAGExport{
		Nodes: []graph.NodeExport{{ID: "story-1", Wave: 0}},
	}
	s.SetDAG(dag)

	if s.dagExport == nil {
		t.Fatal("expected dagExport to be set after SetDAG")
	}
	if s.dagExport.Nodes[0].ID != "story-1" {
		t.Errorf("expected node ID=story-1, got %q", s.dagExport.Nodes[0].ID)
	}
}

func TestSetDAG_NilClearsDAG(t *testing.T) {
	s := newTestServer(t)
	s.SetDAG(&graph.DAGExport{})
	s.SetDAG(nil)

	if s.dagExport != nil {
		t.Error("expected dagExport to be nil after SetDAG(nil)")
	}
}

func TestBuildSnapshot_EscalationsReturned(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Emit an escalation event
	emitAndProject(t, s, state.EventStoryEscalated, "dashboard", storyID, map[string]any{
		"from_tier": 0,
		"to_tier":   1,
		"reason":    "test escalation",
	})

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if len(snap.Escalations) != 1 {
		t.Errorf("expected 1 escalation, got %d", len(snap.Escalations))
	}
}

// emitAndProject creates and projects an event.
func emitAndProject(t *testing.T, s *Server, evtType state.EventType, agentID, storyID string, payload map[string]any) {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	evt := state.NewEvent(evtType, agentID, storyID, payload)
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("emit %s: %v", evtType, err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("project %s: %v", evtType, err)
	}
}

// seedStoryWithID is a variant of seedStory that allows specifying a custom story ID.
func seedStoryWithID(t *testing.T, s *Server, reqID, storyID string) string {
	t.Helper()
	evt := state.NewEvent(state.EventStoryCreated, "system", storyID, map[string]any{
		"id":                  storyID,
		"req_id":              reqID,
		"title":               "Test Story " + storyID,
		"description":         "A test story",
		"acceptance_criteria": "It works",
		"complexity":          2,
	})
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("seed story append: %v", err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("seed story project: %v", err)
	}
	return storyID
}
