package state_test

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newIntegrationStores creates a real FileStore and SQLiteStore backed by temp
// files. The caller must defer the returned cleanup function.
func newIntegrationStores(t *testing.T) (*state.FileStore, *state.SQLiteStore, func()) {
	t.Helper()

	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}

	es, err := state.NewFileStore(filepath.Join(resolved, "events.jsonl"))
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}

	ps, err := state.NewSQLiteStore(filepath.Join(resolved, "projections.db"))
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}

	cleanup := func() {
		es.Close()
		ps.Close()
	}
	return es, ps, cleanup
}

// emitAndProject is a test helper that appends an event to the file store and
// projects it into the SQLite store, failing the test on error.
func emitAndProject(t *testing.T, es *state.FileStore, ps *state.SQLiteStore, evt state.Event) {
	t.Helper()
	if err := es.Append(evt); err != nil {
		t.Fatalf("append event %s: %v", evt.Type, err)
	}
	if err := ps.Project(evt); err != nil {
		t.Fatalf("project event %s: %v", evt.Type, err)
	}
}

func TestIntegration_ReqSubmittedProjection(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          "r-100",
		"title":       "Add OAuth2 support",
		"description": "Implement OAuth2 across all microservices",
	})
	emitAndProject(t, es, ps, evt)

	// Verify event was persisted in the file store.
	events, err := es.List(state.EventFilter{Type: state.EventReqSubmitted})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 REQ_SUBMITTED event, got %d", len(events))
	}

	// Verify projection materialised the requirement.
	req, err := ps.GetRequirement("r-100")
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Title != "Add OAuth2 support" {
		t.Fatalf("expected title 'Add OAuth2 support', got %q", req.Title)
	}
	if req.Status != "pending" {
		t.Fatalf("expected status 'pending', got %q", req.Status)
	}
}

func TestIntegration_MultipleStoriesWithDependencies(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	stories := []struct {
		id         string
		title      string
		complexity int
	}{
		{"s-010", "Create data model", 2},
		{"s-011", "Implement API layer", 5},
		{"s-012", "Add integration tests", 3},
	}

	for _, s := range stories {
		evt := state.NewEvent(state.EventStoryCreated, "tech-lead", s.id, map[string]any{
			"id":          s.id,
			"req_id":      "r-200",
			"title":       s.title,
			"description": "Description for " + s.title,
			"complexity":  s.complexity,
		})
		emitAndProject(t, es, ps, evt)
	}

	// Verify all stories are returned by ListStories.
	result, err := ps.ListStories(state.StoryFilter{ReqID: "r-200"})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 stories, got %d", len(result))
	}

	// Verify individual story data round-trips correctly.
	for i, s := range stories {
		if result[i].ID != s.id {
			t.Fatalf("story %d: expected ID %q, got %q", i, s.id, result[i].ID)
		}
		if result[i].Title != s.title {
			t.Fatalf("story %d: expected title %q, got %q", i, s.title, result[i].Title)
		}
		if result[i].Complexity != s.complexity {
			t.Fatalf("story %d: expected complexity %d, got %d", i, s.complexity, result[i].Complexity)
		}
		if result[i].Status != "draft" {
			t.Fatalf("story %d: expected status 'draft', got %q", i, result[i].Status)
		}
	}

	// Verify event count matches.
	evtCount, err := es.Count(state.EventFilter{Type: state.EventStoryCreated})
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if evtCount != 3 {
		t.Fatalf("expected 3 STORY_CREATED events, got %d", evtCount)
	}
}

func TestIntegration_FullStoryLifecycle(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	const storyID = "s-050"

	// 1. Create the story.
	emitAndProject(t, es, ps, state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
		"id":          storyID,
		"req_id":      "r-300",
		"title":       "Lifecycle test story",
		"description": "Tests every status transition",
		"complexity":  3,
	}))

	// Define the full lifecycle as a table of (event type, agent, payload,
	// expected status after projection).
	type step struct {
		eventType      state.EventType
		agentID        string
		payload        map[string]any
		expectedStatus string
	}

	steps := []step{
		{state.EventStoryEstimated, "tech-lead", nil, "estimated"},
		{state.EventStoryAssigned, "tech-lead", map[string]any{"agent_id": "jr-50"}, "assigned"},
		{state.EventStoryStarted, "jr-50", nil, "in_progress"},
		{state.EventStoryCompleted, "jr-50", nil, "review"},
		{state.EventStoryReviewRequested, "jr-50", nil, "review"},
		{state.EventStoryReviewPassed, "sr-10", nil, "qa"},
		{state.EventStoryQAStarted, "qa-10", nil, "qa"},
		{state.EventStoryQAPassed, "qa-10", nil, "pr_submitted"},
		{state.EventStoryPRCreated, "merger", nil, "pr_submitted"},
		{state.EventStoryMerged, "system", nil, "merged"},
	}

	for i, s := range steps {
		evt := state.NewEvent(s.eventType, s.agentID, storyID, s.payload)
		emitAndProject(t, es, ps, evt)

		story, err := ps.GetStory(storyID)
		if err != nil {
			t.Fatalf("step %d (%s): get story: %v", i, s.eventType, err)
		}
		if story.Status != s.expectedStatus {
			t.Fatalf("step %d (%s): expected status %q, got %q", i, s.eventType, s.expectedStatus, story.Status)
		}
	}

	// After the full lifecycle, the story should have an agent assigned.
	finalStory, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatalf("get final story: %v", err)
	}
	if finalStory.AgentID != "jr-50" {
		t.Fatalf("expected agent_id 'jr-50', got %q", finalStory.AgentID)
	}
	if finalStory.Status != "merged" {
		t.Fatalf("expected final status 'merged', got %q", finalStory.Status)
	}

	// Verify all events were persisted in the file store.
	allEvents, err := es.List(state.EventFilter{StoryID: storyID})
	if err != nil {
		t.Fatalf("list all story events: %v", err)
	}
	// 1 (created) + 10 lifecycle steps = 11 events total
	expectedEventCount := 1 + len(steps)
	if len(allEvents) != expectedEventCount {
		t.Fatalf("expected %d events for story, got %d", expectedEventCount, len(allEvents))
	}
}

func TestIntegration_RequirementStatusProgression(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	const reqID = "r-400"

	// Submit requirement.
	emitAndProject(t, es, ps, state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          reqID,
		"title":       "Requirement lifecycle",
		"description": "Tests requirement status progression",
	}))

	req, err := ps.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("get req: %v", err)
	}
	if req.Status != "pending" {
		t.Fatalf("expected 'pending', got %q", req.Status)
	}

	// Analyze.
	emitAndProject(t, es, ps, state.NewEvent(state.EventReqAnalyzed, "system", "", map[string]any{
		"id": reqID,
	}))
	req, _ = ps.GetRequirement(reqID)
	if req.Status != "analyzed" {
		t.Fatalf("expected 'analyzed', got %q", req.Status)
	}

	// Plan.
	emitAndProject(t, es, ps, state.NewEvent(state.EventReqPlanned, "tech-lead", "", map[string]any{
		"id": reqID,
	}))
	req, _ = ps.GetRequirement(reqID)
	if req.Status != "planned" {
		t.Fatalf("expected 'planned', got %q", req.Status)
	}

	// Complete.
	emitAndProject(t, es, ps, state.NewEvent(state.EventReqCompleted, "system", "", map[string]any{
		"id": reqID,
	}))
	req, _ = ps.GetRequirement(reqID)
	if req.Status != "completed" {
		t.Fatalf("expected 'completed', got %q", req.Status)
	}
}

func TestIntegration_ListStoriesFilterByStatus(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	// Create three stories, then transition one to in_progress and one to merged.
	ids := []string{"s-A", "s-B", "s-C"}
	for _, id := range ids {
		emitAndProject(t, es, ps, state.NewEvent(state.EventStoryCreated, "tech-lead", id, map[string]any{
			"id":          id,
			"req_id":      "r-500",
			"title":       "Story " + id,
			"description": "desc",
			"complexity":  2,
		}))
	}

	// s-A -> in_progress
	emitAndProject(t, es, ps, state.NewEvent(state.EventStoryStarted, "jr-1", "s-A", nil))
	// s-C -> merged (shortcut through states)
	emitAndProject(t, es, ps, state.NewEvent(state.EventStoryMerged, "system", "s-C", nil))

	// Only s-B should still be in 'draft'.
	drafts, err := ps.ListStories(state.StoryFilter{Status: "draft"})
	if err != nil {
		t.Fatalf("list drafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft story, got %d", len(drafts))
	}
	if drafts[0].ID != "s-B" {
		t.Fatalf("expected draft story 's-B', got %q", drafts[0].ID)
	}

	// One story should be in_progress.
	inProgress, err := ps.ListStories(state.StoryFilter{Status: "in_progress"})
	if err != nil {
		t.Fatalf("list in_progress: %v", err)
	}
	if len(inProgress) != 1 || inProgress[0].ID != "s-A" {
		t.Fatalf("expected 1 in_progress story 's-A', got %v", inProgress)
	}

	// One story should be merged.
	merged, err := ps.ListStories(state.StoryFilter{Status: "merged"})
	if err != nil {
		t.Fatalf("list merged: %v", err)
	}
	if len(merged) != 1 || merged[0].ID != "s-C" {
		t.Fatalf("expected 1 merged story 's-C', got %v", merged)
	}
}
