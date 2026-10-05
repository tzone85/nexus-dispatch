package engine

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestPlannerTools_Definitions(t *testing.T) {
	tools := PlannerTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 planner tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("tool %q has invalid parameters JSON: %v", tool.Name, err)
		}
	}

	for _, name := range []string{"create_story", "set_wave_plan", "request_clarification"} {
		if !names[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestPlannerTools_ValidateCreateStory(t *testing.T) {
	tools := PlannerTools()
	var createStory llm.ToolDefinition
	for _, td := range tools {
		if td.Name == "create_story" {
			createStory = td
			break
		}
	}

	validCall := llm.ToolCall{
		Name: "create_story",
		Arguments: json.RawMessage(`{
			"title": "Auth module",
			"description": "Implement authentication",
			"complexity": 3,
			"acceptance_criteria": "Users can log in",
			"dependencies": []
		}`),
	}
	if err := llm.ValidateToolCall(createStory, validCall); err != nil {
		t.Errorf("valid call rejected: %v", err)
	}

	invalidCall := llm.ToolCall{
		Name:      "create_story",
		Arguments: json.RawMessage(`{"description": "no title"}`),
	}
	if err := llm.ValidateToolCall(createStory, invalidCall); err == nil {
		t.Error("expected error for missing 'title'")
	}
}

func TestProcessPlannerToolCalls_CreateStory(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "create_story",
			Arguments: json.RawMessage(`{
				"title": "Auth module",
				"description": "Implement JWT authentication",
				"complexity": 3,
				"acceptance_criteria": "Login endpoint returns token",
				"dependencies": []
			}`),
		},
		{
			Name: "create_story",
			Arguments: json.RawMessage(`{
				"title": "User CRUD",
				"description": "CRUD endpoints for users",
				"complexity": 5,
				"acceptance_criteria": "All CRUD operations work",
				"dependencies": ["s-001"]
			}`),
		},
		{
			Name: "set_wave_plan",
			Arguments: json.RawMessage(`{"waves": [["s-001"], ["s-002"]]}`),
		},
	}

	result, err := ProcessPlannerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Stories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(result.Stories))
	}
	if result.Stories[0].Title != "Auth module" {
		t.Errorf("story[0].Title = %q", result.Stories[0].Title)
	}
	if result.Stories[0].Complexity != 3 {
		t.Errorf("story[0].Complexity = %d", result.Stories[0].Complexity)
	}
	if len(result.Stories[1].DependsOn) != 1 || result.Stories[1].DependsOn[0] != "s-001" {
		t.Errorf("story[1].DependsOn = %v", result.Stories[1].DependsOn)
	}
	if len(result.Waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(result.Waves))
	}
}

// TestProcessPlannerToolCalls_CarriesOwnedFilesAndWaveHint pins the invariant
// that the tool-calling planner path (the default for gemma4 and all cloud Tech
// Lead providers) preserves owned_files and wave_hint through to PlannedStory.
// If these are dropped, the dispatcher's file-conflict avoidance and
// sequential-serialization become silent no-ops and overlapping stories race.
func TestProcessPlannerToolCalls_CarriesOwnedFilesAndWaveHint(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "create_story",
			Arguments: json.RawMessage(`{
				"title": "Wire main",
				"description": "Boot the app",
				"complexity": 3,
				"acceptance_criteria": "App starts",
				"dependencies": [],
				"owned_files": ["cmd/main.go", "internal/app/app.go"],
				"wave_hint": "sequential"
			}`),
		},
		{
			// Small models (Gemma) sometimes emit owned_files as a
			// comma-separated string; it must still parse via FlexibleStringSlice.
			Name: "create_story",
			Arguments: json.RawMessage(`{
				"title": "Add util",
				"description": "Helper",
				"complexity": 2,
				"acceptance_criteria": "Helper works",
				"owned_files": "internal/util/util.go,internal/util/util_test.go"
			}`),
		},
	}

	result, err := ProcessPlannerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stories := mapToolStories(result.Stories)
	if len(stories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(stories))
	}
	if got := stories[0].OwnedFiles; len(got) != 2 || got[0] != "cmd/main.go" || got[1] != "internal/app/app.go" {
		t.Errorf("story[0].OwnedFiles = %v, want [cmd/main.go internal/app/app.go]", got)
	}
	if stories[0].WaveHint != "sequential" {
		t.Errorf("story[0].WaveHint = %q, want sequential", stories[0].WaveHint)
	}
	if got := stories[1].OwnedFiles; len(got) != 2 || got[0] != "internal/util/util.go" {
		t.Errorf("story[1].OwnedFiles = %v, want the comma-split paths", got)
	}
}

func TestProcessPlannerToolCalls_Clarification(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "request_clarification",
			Arguments: json.RawMessage(`{"question": "What auth method?", "context": "Multiple options exist"}`),
		},
	}

	result, err := ProcessPlannerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Clarification == nil {
		t.Fatal("expected clarification request")
	}
	if result.Clarification.Question != "What auth method?" {
		t.Errorf("question = %q", result.Clarification.Question)
	}
}
