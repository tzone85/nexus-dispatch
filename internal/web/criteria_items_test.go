package web

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestBuildSnapshot_AcceptanceCriteriaItems verifies the snapshot exposes each
// story's acceptance criteria pre-split into readable items, keyed by story ID,
// so the dashboard can render a clean list when a user clicks through a story.
func TestBuildSnapshot_AcceptanceCriteriaItems(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	storyID := "story-ac1"
	evt := state.NewEvent(state.EventStoryCreated, "system", storyID, map[string]any{
		"id":                  storyID,
		"req_id":              reqID,
		"title":               "Domain model",
		"description":         "Define the entities.",
		"acceptance_criteria": "Failing tests written first. go test green. WorldState.copy() is independent.",
		"complexity":          3,
	})
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("project: %v", err)
	}

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	items := snap.AcceptanceCriteriaItems[storyID]
	want := []string{
		"Failing tests written first.",
		"go test green.",
		"WorldState.copy() is independent.",
	}
	if len(items) != len(want) {
		t.Fatalf("expected %d items, got %d: %#v", len(want), len(items), items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item[%d] = %q, want %q", i, items[i], want[i])
		}
	}
}
