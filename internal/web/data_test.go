package web

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestBuildSnapshot_EmptyStores(t *testing.T) {
	s := newTestServer(t)
	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snap.Stories) != 0 {
		t.Errorf("expected 0 stories, got %d", len(snap.Stories))
	}
	if len(snap.Agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(snap.Agents))
	}
	if len(snap.Escalations) != 0 {
		t.Errorf("expected 0 escalations, got %d", len(snap.Escalations))
	}
	if snap.DAG != nil {
		t.Error("expected nil DAG")
	}
}

func TestBuildSnapshot_WithData(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if len(snap.Requirements) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(snap.Requirements))
	}
	if snap.Requirements[0].ID != reqID {
		t.Errorf("requirement ID = %q, want %q", snap.Requirements[0].ID, reqID)
	}
	if len(snap.Stories) != 1 {
		t.Fatalf("expected 1 story, got %d", len(snap.Stories))
	}
	if snap.Stories[0].ID != storyID {
		t.Errorf("story ID = %q, want %q", snap.Stories[0].ID, storyID)
	}
	// New story should be in "planned" pipeline bucket (status=draft maps to planned).
	if snap.Pipeline.Planned != 1 {
		t.Errorf("pipeline planned = %d, want 1", snap.Pipeline.Planned)
	}
}

func TestBuildSnapshot_PipelineCounts(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Move story to in_progress.
	startEvt := state.NewEvent(state.EventStoryStarted, "agent-1", storyID, nil)
	s.eventStore.Append(startEvt)
	s.projStore.Project(startEvt)

	snap, _ := s.BuildSnapshot()
	if snap.Pipeline.InProgress != 1 {
		t.Errorf("pipeline in_progress = %d, want 1", snap.Pipeline.InProgress)
	}
	if snap.Pipeline.Planned != 0 {
		t.Errorf("pipeline planned = %d, want 0", snap.Pipeline.Planned)
	}
}

func TestBuildSnapshot_ReviewGates(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Move story to merge_ready.
	readyEvt := state.NewEvent(state.EventStoryMergeReady, "qa", storyID, nil)
	s.eventStore.Append(readyEvt)
	s.projStore.Project(readyEvt)

	// Move req to pending_review.
	pendingEvt := state.NewEvent(state.EventReqPendingReview, "system", "", map[string]any{"id": reqID})
	s.eventStore.Append(pendingEvt)
	s.projStore.Project(pendingEvt)

	snap, _ := s.BuildSnapshot()
	if len(snap.ReviewGates) != 2 {
		t.Fatalf("expected 2 review gates (1 story + 1 req), got %d", len(snap.ReviewGates))
	}
}

func TestBuildSnapshot_Events(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	seedStory(t, s, reqID)

	snap, _ := s.BuildSnapshot()
	// Should contain REQ_SUBMITTED and STORY_CREATED events.
	if len(snap.Events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(snap.Events))
	}
}

func TestSnapshotJSON(t *testing.T) {
	s := newTestServer(t)
	seedRequirement(t, s)

	data, err := s.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(snap.Requirements) != 1 {
		t.Errorf("expected 1 requirement, got %d", len(snap.Requirements))
	}
}

func TestMapStatusToBucket(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"draft", "planned"},
		{"estimated", "planned"},
		{"planned", "planned"},
		{"assigned", "planned"},
		{"in_progress", "in_progress"},
		{"review", "review"},
		{"qa", "qa"},
		{"qa_started", "qa"},
		{"qa_failed", "qa"},
		{"pr_submitted", "pr_submitted"},
		{"merged", "merged"},
		{"split", "split"},
		{"unknown_status", "planned"},
	}
	for _, tt := range tests {
		got := mapStatusToBucket(tt.status)
		if got != tt.want {
			t.Errorf("mapStatusToBucket(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestIntFromPayload(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want int
	}{
		{"float64", map[string]any{"count": float64(42)}, "count", 42},
		{"int", map[string]any{"count": 7}, "count", 7},
		{"missing", map[string]any{}, "count", 0},
		{"string", map[string]any{"count": "not a number"}, "count", 0},
		{"nil map", nil, "count", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intFromPayload(tt.m, tt.key)
			if got != tt.want {
				t.Errorf("intFromPayload = %d, want %d", got, tt.want)
			}
		})
	}
}
