package engine

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestManagerTools_Definitions(t *testing.T) {
	tools := ManagerTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 manager tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("tool %q: invalid parameters JSON: %v", tool.Name, err)
		}
	}

	if !names["escalation_decision"] {
		t.Error("missing escalation_decision tool")
	}
	if !names["split_story"] {
		t.Error("missing split_story tool")
	}
}

func TestProcessManagerToolCalls_EscalationDecision(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "escalation_decision",
			Arguments: json.RawMessage(`{
				"story_id": "s-003",
				"action": "reassign_higher_tier",
				"reason": "Complexity underestimated",
				"assigned_to": "agent-senior-1"
			}`),
		},
	}

	result, err := ProcessManagerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Decision == nil {
		t.Fatal("expected escalation decision")
	}
	if result.Decision.Action != "reassign_higher_tier" {
		t.Errorf("action = %q", result.Decision.Action)
	}
	if result.Decision.AssignedTo != "agent-senior-1" {
		t.Errorf("assigned_to = %q", result.Decision.AssignedTo)
	}
}

func TestProcessManagerToolCalls_InvalidAction(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "escalation_decision",
			Arguments: json.RawMessage(`{
				"story_id": "s-001",
				"action": "delete_everything",
				"reason": "frustrated"
			}`),
		},
	}

	_, err := ProcessManagerToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestProcessManagerToolCalls_SplitStory(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "split_story",
			Arguments: json.RawMessage(`{
				"original_story_id": "s-005",
				"new_stories": [
					{"title": "Part A", "description": "First half", "complexity": 3},
					{"title": "Part B", "description": "Second half", "complexity": 2}
				]
			}`),
		},
	}

	result, err := ProcessManagerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Split == nil {
		t.Fatal("expected split result")
	}
	if result.Split.OriginalStoryID != "s-005" {
		t.Errorf("original_story_id = %q", result.Split.OriginalStoryID)
	}
	if len(result.Split.NewStories) != 2 {
		t.Fatalf("expected 2 new stories, got %d", len(result.Split.NewStories))
	}
	if result.Split.NewStories[0].Title != "Part A" {
		t.Errorf("new story title = %q", result.Split.NewStories[0].Title)
	}
}

func TestProcessManagerToolCalls_NoRecognizedTool(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "unknown_tool",
			Arguments: json.RawMessage(`{}`),
		},
	}

	_, err := ProcessManagerToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for unrecognized tool")
	}
}

func TestProcessManagerToolCalls_InvalidJSON(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "escalation_decision",
			Arguments: json.RawMessage(`not json`),
		},
	}

	_, err := ProcessManagerToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
