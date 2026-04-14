package state_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Tests to boost coverage for BackfillAcceptanceCriteria.

func TestSQLiteStore_BackfillAcceptanceCriteria(t *testing.T) {
	db, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer db.Close()

	// Create a requirement
	reqEvt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          "r-001",
		"title":       "Test req",
		"description": "test",
	})
	if err := db.Project(reqEvt); err != nil {
		t.Fatalf("project req: %v", err)
	}

	// Create a story WITHOUT acceptance criteria
	storyEvt := state.NewEvent(state.EventStoryCreated, "system", "s-001", map[string]any{
		"id":          "s-001",
		"title":       "Story One",
		"description": "test story",
		"complexity":  3,
	})
	if err := db.Project(storyEvt); err != nil {
		t.Fatalf("project story: %v", err)
	}

	// Story should have empty AC
	story, err := db.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.AcceptanceCriteria != "" {
		t.Errorf("expected empty AC, got %q", story.AcceptanceCriteria)
	}

	// Now backfill with an event that has AC
	backfillEvt := state.NewEvent(state.EventStoryCreated, "system", "s-001", map[string]any{
		"id":                  "s-001",
		"title":               "Story One",
		"acceptance_criteria": "Must pass all tests",
		"complexity":          3,
	})
	db.BackfillAcceptanceCriteria([]state.Event{backfillEvt})

	// Story should now have AC
	story, err = db.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story after backfill: %v", err)
	}
	if story.AcceptanceCriteria != "Must pass all tests" {
		t.Errorf("expected backfilled AC, got %q", story.AcceptanceCriteria)
	}
}
