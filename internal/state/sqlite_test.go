package state_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestSQLiteStore_ProjectRequirement(t *testing.T) {
	db, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer db.Close()

	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          "r-001",
		"title":       "Add OAuth2",
		"description": "Implement OAuth2 across all services",
	})

	if err := db.Project(evt); err != nil {
		t.Fatalf("project: %v", err)
	}

	req, err := db.GetRequirement("r-001")
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Title != "Add OAuth2" {
		t.Fatalf("expected 'Add OAuth2', got %s", req.Title)
	}
	if req.Status != "pending" {
		t.Fatalf("expected 'pending', got %s", req.Status)
	}
}

func TestSQLiteStore_ProjectStory(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	evt := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id":          "s-001",
		"req_id":      "r-001",
		"title":       "OAuth middleware",
		"description": "Create Express middleware for OAuth2 token validation",
		"complexity":  5,
	})

	if err := db.Project(evt); err != nil {
		t.Fatalf("project: %v", err)
	}

	story, err := db.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Title != "OAuth middleware" {
		t.Fatalf("expected 'OAuth middleware', got %s", story.Title)
	}
	if story.Complexity != 5 {
		t.Fatalf("expected complexity 5, got %d", story.Complexity)
	}
	if story.Status != "draft" {
		t.Fatalf("expected 'draft', got %s", story.Status)
	}
}

func TestSQLiteStore_StoryStatusTransitions(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	// Create story
	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task", "description": "desc", "complexity": 3,
	}))

	// Assign
	db.Project(state.NewEvent(state.EventStoryAssigned, "tl", "s-001", map[string]any{
		"agent_id": "jr-1",
	}))

	story, _ := db.GetStory("s-001")
	if story.Status != "assigned" {
		t.Fatalf("expected 'assigned', got %s", story.Status)
	}
	if story.AgentID != "jr-1" {
		t.Fatalf("expected agent 'jr-1', got %s", story.AgentID)
	}

	// Start
	db.Project(state.NewEvent(state.EventStoryStarted, "jr-1", "s-001", nil))
	story, _ = db.GetStory("s-001")
	if story.Status != "in_progress" {
		t.Fatalf("expected 'in_progress', got %s", story.Status)
	}

	// Complete
	db.Project(state.NewEvent(state.EventStoryCompleted, "jr-1", "s-001", nil))
	story, _ = db.GetStory("s-001")
	if story.Status != "review" {
		t.Fatalf("expected 'review', got %s", story.Status)
	}

	// Review passed
	db.Project(state.NewEvent(state.EventStoryReviewPassed, "sr-1", "s-001", nil))
	story, _ = db.GetStory("s-001")
	if story.Status != "qa" {
		t.Fatalf("expected 'qa', got %s", story.Status)
	}

	// QA passed
	db.Project(state.NewEvent(state.EventStoryQAPassed, "qa-1", "s-001", nil))
	story, _ = db.GetStory("s-001")
	if story.Status != "pr_submitted" {
		t.Fatalf("expected 'pr_submitted', got %s", story.Status)
	}

	// Merged
	db.Project(state.NewEvent(state.EventStoryMerged, "system", "s-001", nil))
	story, _ = db.GetStory("s-001")
	if story.Status != "merged" {
		t.Fatalf("expected 'merged', got %s", story.Status)
	}
}

func TestSQLiteStore_ListStoriesByStatus(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task1", "description": "d", "complexity": 2,
	}))
	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-002", map[string]any{
		"id": "s-002", "req_id": "r-001", "title": "task2", "description": "d", "complexity": 5,
	}))
	db.Project(state.NewEvent(state.EventStoryStarted, "jr-1", "s-001", nil))

	stories, _ := db.ListStories(state.StoryFilter{Status: "draft"})
	if len(stories) != 1 {
		t.Fatalf("expected 1 draft story, got %d", len(stories))
	}
	if stories[0].ID != "s-002" {
		t.Fatalf("expected s-002, got %s", stories[0].ID)
	}
}

func TestSQLiteStore_StoryOwnedFiles(t *testing.T) {
	db, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer db.Close()

	evt := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id":          "s-001",
		"req_id":      "r-001",
		"title":       "Add user model",
		"description": "Create user struct",
		"complexity":  3,
		"owned_files": []string{"src/models/user.go", "src/models/user_test.go"},
		"wave_hint":   "sequential",
	})

	if err := db.Project(evt); err != nil {
		t.Fatalf("project: %v", err)
	}

	story, err := db.GetStory("s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}

	if len(story.OwnedFiles) != 2 {
		t.Fatalf("expected 2 owned files, got %d", len(story.OwnedFiles))
	}
	if story.OwnedFiles[0] != "src/models/user.go" {
		t.Fatalf("expected 'src/models/user.go', got %s", story.OwnedFiles[0])
	}
	if story.OwnedFiles[1] != "src/models/user_test.go" {
		t.Fatalf("expected 'src/models/user_test.go', got %s", story.OwnedFiles[1])
	}
	if story.WaveHint != "sequential" {
		t.Fatalf("expected wave_hint 'sequential', got %s", story.WaveHint)
	}
}

func TestSQLiteStore_StoryEscalation(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task", "description": "d", "complexity": 3,
	}))
	db.Project(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 0, "to_tier": 1, "reason": "review failed twice",
	}))

	story, _ := db.GetStory("s-001")
	if story.EscalationTier != 1 {
		t.Fatalf("expected escalation_tier 1, got %d", story.EscalationTier)
	}

	escalations, _ := db.ListEscalations()
	if len(escalations) != 1 {
		t.Fatalf("expected 1 escalation, got %d", len(escalations))
	}
	if escalations[0].FromTier != 0 || escalations[0].ToTier != 1 {
		t.Fatalf("expected tier 0->1, got %d->%d", escalations[0].FromTier, escalations[0].ToTier)
	}
}

func TestSQLiteStore_StoryRewritten(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "old title", "description": "old desc", "complexity": 3,
	}))
	db.Project(state.NewEvent(state.EventStoryRewritten, "manager", "s-001", map[string]any{
		"changes": map[string]any{"title": "new title", "description": "new desc"},
	}))

	story, _ := db.GetStory("s-001")
	if story.Title != "new title" {
		t.Fatalf("expected 'new title', got %s", story.Title)
	}
	if story.Status != "draft" {
		t.Fatalf("expected 'draft' after rewrite, got %s", story.Status)
	}
	if story.EscalationTier != 0 {
		t.Fatalf("expected escalation_tier 0 after rewrite, got %d", story.EscalationTier)
	}
}

func TestSQLiteStore_StorySplit(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task", "description": "d", "complexity": 8,
	}))
	db.Project(state.NewEvent(state.EventStorySplit, "manager", "s-001", map[string]any{
		"child_story_ids": []string{"s-001-a", "s-001-b"},
	}))

	story, _ := db.GetStory("s-001")
	if story.Status != "split" {
		t.Fatalf("expected 'split', got %s", story.Status)
	}
}

func TestSQLiteStore_SplitDepth(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task", "description": "d",
		"complexity": 3, "split_depth": 1,
	}))

	story, _ := db.GetStory("s-001")
	if story.SplitDepth != 1 {
		t.Fatalf("expected split_depth 1, got %d", story.SplitDepth)
	}
}
