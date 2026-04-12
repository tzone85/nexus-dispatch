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

func TestSQLiteStore_ListRequirementsFiltered(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": "r-001", "title": "Auth", "description": "d", "repo_path": "/repo/a",
	}))
	db.Project(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": "r-002", "title": "Dashboard", "description": "d", "repo_path": "/repo/b",
	}))

	// Unfiltered.
	all, err := db.ListRequirementsFiltered(state.ReqFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 requirements, got %d", len(all))
	}

	// Filter by repo path.
	filtered, err := db.ListRequirementsFiltered(state.ReqFilter{RepoPath: "/repo/a"})
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 requirement for /repo/a, got %d", len(filtered))
	}
	if filtered[0].ID != "r-001" {
		t.Errorf("expected r-001, got %s", filtered[0].ID)
	}

	// Filter excluding archived.
	db.ArchiveRequirement("r-001")
	excluded, err := db.ListRequirementsFiltered(state.ReqFilter{ExcludeArchived: true})
	if err != nil {
		t.Fatalf("list exclude archived: %v", err)
	}
	if len(excluded) != 1 {
		t.Fatalf("expected 1 non-archived, got %d", len(excluded))
	}
	if excluded[0].ID != "r-002" {
		t.Errorf("expected r-002, got %s", excluded[0].ID)
	}
}

func TestSQLiteStore_ListRequirements(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": "r-001", "title": "Auth", "description": "d",
	}))

	reqs, err := db.ListRequirements()
	if err != nil {
		t.Fatalf("ListRequirements: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(reqs))
	}
}

func TestSQLiteStore_InsertAgent(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	err := db.InsertAgent("agent-001", "senior", "gemma4:26b", "gemma", "nxd-session-1")
	if err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}

	agents, err := db.ListAgents(state.AgentFilter{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "agent-001" {
		t.Errorf("ID = %q, want agent-001", agents[0].ID)
	}
	if agents[0].Type != "senior" {
		t.Errorf("Type = %q, want senior", agents[0].Type)
	}
	if agents[0].SessionName != "nxd-session-1" {
		t.Errorf("SessionName = %q, want nxd-session-1", agents[0].SessionName)
	}
}

func TestSQLiteStore_ListAgents_StatusFilter(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.InsertAgent("a-001", "senior", "gemma4", "gemma", "s1")
	db.InsertAgent("a-002", "junior", "gemma4", "gemma", "s2")

	// Both agents default to "idle" status.
	all, _ := db.ListAgents(state.AgentFilter{})
	if len(all) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(all))
	}

	idle, _ := db.ListAgents(state.AgentFilter{Status: "idle"})
	if len(idle) != 2 {
		t.Fatalf("expected 2 idle agents, got %d", len(idle))
	}

	active, _ := db.ListAgents(state.AgentFilter{Status: "active"})
	if len(active) != 0 {
		t.Fatalf("expected 0 active agents, got %d", len(active))
	}
}

func TestSQLiteStore_ArchiveRequirement(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": "r-001", "title": "Auth", "description": "d",
	}))

	if err := db.ArchiveRequirement("r-001"); err != nil {
		t.Fatalf("ArchiveRequirement: %v", err)
	}

	req, err := db.GetRequirement("r-001")
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	if req.Status != "archived" {
		t.Errorf("expected status=archived, got %q", req.Status)
	}
}

func TestSQLiteStore_ArchiveStoriesByReq(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "task1", "description": "d", "complexity": 3,
	}))
	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-002", map[string]any{
		"id": "s-002", "req_id": "r-001", "title": "task2", "description": "d", "complexity": 2,
	}))

	if err := db.ArchiveStoriesByReq("r-001"); err != nil {
		t.Fatalf("ArchiveStoriesByReq: %v", err)
	}

	stories, _ := db.ListStories(state.StoryFilter{ReqID: "r-001"})
	for _, s := range stories {
		if s.Status != "archived" {
			t.Errorf("story %s: status=%q, want archived", s.ID, s.Status)
		}
	}
}

func TestSQLiteStore_ListStoryDeps(t *testing.T) {
	db, _ := state.NewSQLiteStore(":memory:")
	defer db.Close()

	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "scaffold", "description": "d", "complexity": 2,
	}))
	db.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-002", map[string]any{
		"id": "s-002", "req_id": "r-001", "title": "feature", "description": "d", "complexity": 5,
		"depends_on": []any{"s-001"},
	}))

	deps, err := db.ListStoryDeps("r-001")
	if err != nil {
		t.Fatalf("ListStoryDeps: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].StoryID != "s-002" || deps[0].DependsOnID != "s-001" {
		t.Errorf("dep = %+v, want s-002 -> s-001", deps[0])
	}
}

func TestDecodePayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantKey string
	}{
		{"valid", []byte(`{"key":"value"}`), "key"},
		{"empty", nil, ""},
		{"invalid", []byte(`not json`), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := state.DecodePayload(tt.payload)
			if tt.wantKey != "" {
				if _, ok := m[tt.wantKey]; !ok {
					t.Errorf("expected key %q in decoded payload", tt.wantKey)
				}
			} else {
				if len(m) != 0 {
					t.Errorf("expected empty map, got %v", m)
				}
			}
		})
	}
}
