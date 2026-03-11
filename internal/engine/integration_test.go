package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newIntegrationStores creates real FileStore and SQLiteStore backed by temp
// files on disk (not :memory:). Returns both stores and a cleanup function.
func newIntegrationStores(t *testing.T) (state.EventStore, state.ProjectionStore, func()) {
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

func TestIntegration_PlannerToDispatcher(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	// Create a fake repo directory so ScanRepo detects Go.
	repoDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module integ-test"), 0644)

	plannerResponse := `[
		{
			"id": "s-001",
			"title": "Create user model",
			"description": "Define User struct with validation",
			"acceptance_criteria": "User struct exists with name, email fields",
			"complexity": 2,
			"depends_on": []
		},
		{
			"id": "s-002",
			"title": "Add REST endpoints",
			"description": "Create CRUD handlers for /users",
			"acceptance_criteria": "GET/POST/PUT/DELETE /users work",
			"complexity": 5,
			"depends_on": ["s-001"]
		},
		{
			"id": "s-003",
			"title": "Write unit tests",
			"description": "Add tests for user model and handlers",
			"acceptance_criteria": "80% coverage on user package",
			"complexity": 3,
			"depends_on": ["s-001"]
		}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: plannerResponse,
		Model:   "claude-opus-4",
	})

	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, es, ps)

	// --- Phase 1: Plan ---
	planResult, err := planner.Plan(context.Background(), "r-integ-1", "Add user management API", repoDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if len(planResult.Stories) != 3 {
		t.Fatalf("expected 3 planned stories, got %d", len(planResult.Stories))
	}

	// Verify stories were projected into the store.
	allStories, err := ps.ListStories(state.StoryFilter{ReqID: "r-integ-1"})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(allStories) != 3 {
		t.Fatalf("expected 3 projected stories, got %d", len(allStories))
	}

	// --- Phase 2: Dispatch Wave 1 ---
	dispatcher := engine.NewDispatcher(cfg, es, ps)
	completed := make(map[string]bool)

	assignments, err := dispatcher.DispatchWave(planResult.Graph, completed, "r-integ-1", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch wave 1: %v", err)
	}

	// Story IDs are prefixed: reqID "r-integ-1" (9 chars) → prefix "r-integ-"
	// So "s-001" → "r-integ--s-001", "s-002" → "r-integ--s-002", etc.

	// Wave 1: s-001 (no deps) is the only story ready.
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment in wave 1, got %d", len(assignments))
	}
	if assignments[0].StoryID != "r-integ--s-001" {
		t.Fatalf("expected r-integ--s-001 in wave 1, got %s", assignments[0].StoryID)
	}

	// Verify story is now 'assigned' in projection.
	s001, err := ps.GetStory("r-integ--s-001")
	if err != nil {
		t.Fatalf("get story r-integ--s-001: %v", err)
	}
	if s001.Status != "assigned" {
		t.Fatalf("expected s-001 status 'assigned', got %q", s001.Status)
	}

	// Verify routing: complexity 2 should go to junior.
	if assignments[0].Role != "junior" {
		t.Fatalf("expected junior role for complexity 2, got %s", assignments[0].Role)
	}

	// --- Phase 3: Dispatch Wave 2 ---
	completed["r-integ--s-001"] = true
	assignments2, err := dispatcher.DispatchWave(planResult.Graph, completed, "r-integ-1", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch wave 2: %v", err)
	}

	// Wave 2: s-002 and s-003 both depend only on s-001, which is now complete.
	if len(assignments2) != 2 {
		t.Fatalf("expected 2 assignments in wave 2, got %d", len(assignments2))
	}

	// Verify routing by complexity.
	assignmentMap := make(map[string]engine.Assignment)
	for _, a := range assignments2 {
		assignmentMap[a.StoryID] = a
	}

	if a, ok := assignmentMap["r-integ--s-002"]; ok {
		if a.Role != "intermediate" {
			t.Fatalf("s-002 (complexity 5) should route to intermediate, got %s", a.Role)
		}
	} else {
		t.Fatal("r-integ--s-002 not found in wave 2 assignments")
	}

	if a, ok := assignmentMap["r-integ--s-003"]; ok {
		if a.Role != "junior" {
			t.Fatalf("s-003 (complexity 3) should route to junior, got %s", a.Role)
		}
	} else {
		t.Fatal("r-integ--s-003 not found in wave 2 assignments")
	}

	// Verify all wave 2 stories are now 'assigned'.
	for _, id := range []string{"r-integ--s-002", "r-integ--s-003"} {
		story, err := ps.GetStory(id)
		if err != nil {
			t.Fatalf("get story %s: %v", id, err)
		}
		if story.Status != "assigned" {
			t.Fatalf("expected %s status 'assigned', got %q", id, story.Status)
		}
	}
}

func TestIntegration_FullPipeline_PlanDispatchReviewQAMerge(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	repoDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module pipeline-test"), 0644)

	// Single story for simplicity.
	plannerResponse := `[
		{
			"id": "s-pipe-1",
			"title": "Implement feature X",
			"description": "Build out feature X end to end",
			"acceptance_criteria": "Feature X works correctly",
			"complexity": 3,
			"depends_on": []
		}
	]`

	reviewResponse := `{
		"passed": true,
		"comments": [],
		"summary": "Code looks great, all acceptance criteria met"
	}`

	cfg := config.DefaultConfig()
	replayClient := llm.NewReplayClient(
		llm.CompletionResponse{Content: plannerResponse, Model: "claude-opus-4"},
		llm.CompletionResponse{Content: reviewResponse, Model: "claude-sonnet-4"},
	)

	// --- Step 1: Plan ---
	planner := engine.NewPlanner(replayClient, cfg, es, ps)
	planResult, err := planner.Plan(context.Background(), "r-pipe-1", "Build feature X", repoDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// --- Step 2: Dispatch ---
	dispatcher := engine.NewDispatcher(cfg, es, ps)
	assignments, err := dispatcher.DispatchWave(planResult.Graph, map[string]bool{}, "r-pipe-1", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(assignments))
	}

	storyID := assignments[0].StoryID

	// --- Step 3: Simulate agent work (started → completed) ---
	for _, evt := range []state.Event{
		state.NewEvent(state.EventStoryStarted, assignments[0].AgentID, storyID, nil),
		state.NewEvent(state.EventStoryCompleted, assignments[0].AgentID, storyID, nil),
	} {
		if err := es.Append(evt); err != nil {
			t.Fatalf("append event: %v", err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatalf("project event: %v", err)
		}
	}

	// Verify story is now in 'review'.
	story, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "review" {
		t.Fatalf("expected 'review', got %q", story.Status)
	}

	// --- Step 4: Review ---
	reviewer := engine.NewReviewer(replayClient, cfg.Models.Senior.Model, cfg.Models.Senior.MaxTokens, es, ps)
	reviewResult, err := reviewer.Review(
		context.Background(),
		storyID,
		"Implement feature X",
		"Feature X works correctly",
		"diff --git a/feature.go b/feature.go\n+func FeatureX() { /* implementation */ }",
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !reviewResult.Passed {
		t.Fatal("expected review to pass")
	}

	// Verify story is now in 'qa'.
	story, _ = ps.GetStory(storyID)
	if story.Status != "qa" {
		t.Fatalf("expected 'qa' after review pass, got %q", story.Status)
	}

	// --- Step 5: QA (mock runner that always passes) ---
	runner := &mockRunner{results: map[string]mockRunResult{
		"go": {output: "ok", err: nil},
	}}
	qa := engine.NewQA(engine.QAConfig{
		BuildCommand: "go build ./...",
		TestCommand:  "go test ./...",
	}, runner, es, ps)

	qaResult, err := qa.Run(context.Background(), storyID, repoDir)
	if err != nil {
		t.Fatalf("qa: %v", err)
	}
	if !qaResult.Passed {
		t.Fatal("expected QA to pass")
	}

	// Verify story is now 'pr_submitted'.
	story, _ = ps.GetStory(storyID)
	if story.Status != "pr_submitted" {
		t.Fatalf("expected 'pr_submitted' after QA pass, got %q", story.Status)
	}

	// --- Step 6: Merge (mock GitHub ops) ---
	ghOps := &mockGitHubOps{
		createPR: engine.PRCreationResult{Number: 1, URL: "https://github.com/test/repo/pull/1"},
	}
	merger := engine.NewMerger(config.MergeConfig{AutoMerge: true, BaseBranch: "main"}, ghOps, es, ps)

	mergeResult, err := merger.Merge(storyID, "Implement feature X", repoDir, assignments[0].Branch)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !mergeResult.Merged {
		t.Fatal("expected story to be merged")
	}

	// --- Verify Final State ---
	story, _ = ps.GetStory(storyID)
	if story.Status != "merged" {
		t.Fatalf("expected final status 'merged', got %q", story.Status)
	}

	// Verify the requirement was submitted and planned.
	req, err := ps.GetRequirement("r-pipe-1")
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Status != "pending" {
		// Planner emits REQ_SUBMITTED (pending) and REQ_PLANNED. The planner
		// only appends REQ_PLANNED to the event store but does not project it,
		// so the projected status stays 'pending'. This is expected.
		t.Logf("requirement status is %q (REQ_PLANNED appended but not projected by planner)", req.Status)
	}

	// Verify LLM was called exactly twice: once for planning, once for review.
	if replayClient.CallCount() != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", replayClient.CallCount())
	}

	// Verify event counts.
	verifyEventCount := func(eventType state.EventType, expected int) {
		t.Helper()
		events, err := es.List(state.EventFilter{Type: eventType})
		if err != nil {
			t.Fatalf("list %s events: %v", eventType, err)
		}
		if len(events) != expected {
			t.Fatalf("expected %d %s events, got %d", expected, eventType, len(events))
		}
	}

	verifyEventCount(state.EventReqSubmitted, 1)
	verifyEventCount(state.EventStoryCreated, 1)
	verifyEventCount(state.EventStoryAssigned, 1)
	verifyEventCount(state.EventStoryStarted, 1)
	verifyEventCount(state.EventStoryCompleted, 1)
	verifyEventCount(state.EventStoryReviewPassed, 1)
	verifyEventCount(state.EventStoryQAStarted, 1)
	verifyEventCount(state.EventStoryQAPassed, 1)
	verifyEventCount(state.EventStoryPRCreated, 1)
	verifyEventCount(state.EventStoryMerged, 1)
}

func TestIntegration_MultiStoryPipeline(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	repoDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module multi-test"), 0644)

	plannerResponse := `[
		{
			"id": "s-m1",
			"title": "Foundation layer",
			"description": "Core abstractions",
			"acceptance_criteria": "Core types defined",
			"complexity": 2,
			"depends_on": []
		},
		{
			"id": "s-m2",
			"title": "Business logic",
			"description": "Implement business rules",
			"acceptance_criteria": "Rules pass validation",
			"complexity": 8,
			"depends_on": ["s-m1"]
		}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: plannerResponse,
		Model:   "claude-opus-4",
	})

	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, es, ps)
	planResult, err := planner.Plan(context.Background(), "r-multi", "Build multi-story feature", repoDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Story IDs are prefixed: reqID "r-multi" (7 chars) → prefix "r-multi"
	// So "s-m1" → "r-multi-s-m1", "s-m2" → "r-multi-s-m2"

	// Dispatch wave 1: only s-m1 (no deps).
	dispatcher := engine.NewDispatcher(cfg, es, ps)
	wave1, err := dispatcher.DispatchWave(planResult.Graph, map[string]bool{}, "r-multi", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch wave 1: %v", err)
	}
	if len(wave1) != 1 || wave1[0].StoryID != "r-multi-s-m1" {
		t.Fatalf("wave 1: expected [r-multi-s-m1], got %v", wave1)
	}

	// Simulate s-m1 completion through the full lifecycle.
	for _, evtType := range []state.EventType{
		state.EventStoryStarted,
		state.EventStoryCompleted,
		state.EventStoryReviewPassed,
		state.EventStoryQAPassed,
		state.EventStoryMerged,
	} {
		evt := state.NewEvent(evtType, "agent-m1", "r-multi-s-m1", nil)
		if err := es.Append(evt); err != nil {
			t.Fatalf("append %s: %v", evtType, err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatalf("project %s: %v", evtType, err)
		}
	}

	// Verify s-m1 is merged.
	sm1, _ := ps.GetStory("r-multi-s-m1")
	if sm1.Status != "merged" {
		t.Fatalf("expected r-multi-s-m1 'merged', got %q", sm1.Status)
	}

	// Dispatch wave 2: s-m2 depends on s-m1 which is now completed.
	wave2, err := dispatcher.DispatchWave(planResult.Graph, map[string]bool{"r-multi-s-m1": true}, "r-multi", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch wave 2: %v", err)
	}
	if len(wave2) != 1 || wave2[0].StoryID != "r-multi-s-m2" {
		t.Fatalf("wave 2: expected [r-multi-s-m2], got %v", wave2)
	}

	// Complexity 8 should route to senior.
	if wave2[0].Role != "senior" {
		t.Fatalf("expected senior role for complexity 8, got %s", wave2[0].Role)
	}

	// Verify s-m2 is assigned.
	sm2, _ := ps.GetStory("r-multi-s-m2")
	if sm2.Status != "assigned" {
		t.Fatalf("expected r-multi-s-m2 'assigned', got %q", sm2.Status)
	}

	// No more waves should be dispatchable.
	completed := map[string]bool{"r-multi-s-m1": true, "r-multi-s-m2": true}
	wave3, err := dispatcher.DispatchWave(planResult.Graph, completed, "r-multi", planResult.Stories)
	if err != nil {
		t.Fatalf("dispatch wave 3: %v", err)
	}
	if len(wave3) != 0 {
		t.Fatalf("expected 0 assignments when all completed, got %d", len(wave3))
	}
}

func TestIntegration_PlannerEventPersistence(t *testing.T) {
	es, ps, cleanup := newIntegrationStores(t)
	defer cleanup()

	repoDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module persist-test"), 0644)

	response := `[
		{"id": "s-p1", "title": "Task A", "description": "Do A", "acceptance_criteria": "A done", "complexity": 1, "depends_on": []}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{Content: response})
	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, es, ps)

	_, err := planner.Plan(context.Background(), "r-persist", "Persist test", repoDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Verify events were persisted to the real file store (not in-memory).
	reqEvents, err := es.List(state.EventFilter{Type: state.EventReqSubmitted})
	if err != nil {
		t.Fatalf("list req events: %v", err)
	}
	if len(reqEvents) != 1 {
		t.Fatalf("expected 1 REQ_SUBMITTED event, got %d", len(reqEvents))
	}

	storyEvents, err := es.List(state.EventFilter{Type: state.EventStoryCreated})
	if err != nil {
		t.Fatalf("list story events: %v", err)
	}
	if len(storyEvents) != 1 {
		t.Fatalf("expected 1 STORY_CREATED event, got %d", len(storyEvents))
	}

	plannedEvents, err := es.List(state.EventFilter{Type: state.EventReqPlanned})
	if err != nil {
		t.Fatalf("list planned events: %v", err)
	}
	if len(plannedEvents) != 1 {
		t.Fatalf("expected 1 REQ_PLANNED event, got %d", len(plannedEvents))
	}

	// Verify projection has the story (prefixed: "r-persis" + "-" + "s-p1").
	story, err := ps.GetStory("r-persis-s-p1")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Title != "Task A" {
		t.Fatalf("expected title 'Task A', got %q", story.Title)
	}

	// Verify the requirement projection.
	req, err := ps.GetRequirement("r-persist")
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Title != "Persist test" {
		t.Fatalf("expected requirement title 'Persist test', got %q", req.Title)
	}
}
