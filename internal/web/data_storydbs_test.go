package web

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestBuildSnapshot_NoStoryDBsByDefault(t *testing.T) {
	s := newTestServer(t)
	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if snap.StoryDBs != nil {
		t.Errorf("expected nil StoryDBs map, got: %+v", snap.StoryDBs)
	}
	if snap.DBSummary != nil {
		t.Errorf("expected nil DBSummary, got: %+v", snap.DBSummary)
	}
}

func TestBuildSnapshot_WithStoryDBs(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Project a STORY_DB_CREATED event.
	created := state.NewEvent(state.EventStoryDBCreated, "lifecycle", storyID, map[string]any{
		"db_id": "db-1", "db_name": "nxd-" + storyID, "provider": "docker",
		"template": "base",
	})
	if err := s.eventStore.Append(created); err != nil {
		t.Fatal(err)
	}
	if err := s.projStore.Project(created); err != nil {
		t.Fatal(err)
	}

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if snap.StoryDBs == nil {
		t.Fatal("expected non-nil StoryDBs map")
	}
	got, ok := snap.StoryDBs[storyID]
	if !ok {
		t.Fatalf("expected entry for %s, got keys: %v", storyID, mapKeys(snap.StoryDBs))
	}
	if got.Status != "created" || got.Provider != "docker" {
		t.Errorf("StoryDB row mismatch: %+v", got)
	}
	if snap.DBSummary == nil {
		t.Fatal("expected DBSummary populated")
	}
	if snap.DBSummary.Created != 1 || snap.DBSummary.Failed != 0 || snap.DBSummary.Deleted != 0 {
		t.Errorf("DBSummary counts wrong: %+v", *snap.DBSummary)
	}
}

func TestBuildSnapshot_DBSummary_MixedStatuses(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	for _, ev := range []state.Event{
		state.NewEvent(state.EventStoryDBCreated, "lc", storyID, map[string]any{
			"db_id": "d1", "db_name": "n1", "provider": "docker",
		}),
		state.NewEvent(state.EventStoryDBFailed, "lc", "other-story", map[string]any{
			"db_id": "d2", "db_name": "n2", "provider": "docker", "error": "boom",
		}),
	} {
		if err := s.eventStore.Append(ev); err != nil {
			t.Fatal(err)
		}
		if err := s.projStore.Project(ev); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if snap.DBSummary == nil {
		t.Fatal("expected DBSummary")
	}
	if snap.DBSummary.Created != 1 {
		t.Errorf("Created = %d, want 1", snap.DBSummary.Created)
	}
	if snap.DBSummary.Failed != 1 {
		t.Errorf("Failed = %d, want 1", snap.DBSummary.Failed)
	}
}

func mapKeys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
