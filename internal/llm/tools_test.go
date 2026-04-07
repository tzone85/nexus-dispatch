package llm

import (
	"encoding/json"
	"testing"
)

func TestToolDefinition_MarshalJSON(t *testing.T) {
	td := ToolDefinition{
		Name:        "create_story",
		Description: "Create a new story from a requirement",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {"type": "string"},
				"complexity": {"type": "integer", "minimum": 1, "maximum": 13}
			},
			"required": ["title", "complexity"]
		}`),
	}

	data, err := json.Marshal(td)
	if err != nil {
		t.Fatalf("marshal ToolDefinition: %v", err)
	}

	var got ToolDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ToolDefinition: %v", err)
	}

	if got.Name != "create_story" {
		t.Errorf("Name = %q, want %q", got.Name, "create_story")
	}
	if got.Description != td.Description {
		t.Errorf("Description mismatch")
	}
}

func TestToolCall_MarshalJSON(t *testing.T) {
	tc := ToolCall{
		Name:      "create_story",
		Arguments: json.RawMessage(`{"title":"Auth endpoint","complexity":3}`),
	}

	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal ToolCall: %v", err)
	}

	var got ToolCall
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ToolCall: %v", err)
	}

	if got.Name != "create_story" {
		t.Errorf("Name = %q, want %q", got.Name, "create_story")
	}
}

func TestToolCallResult_RoundTrip(t *testing.T) {
	tcr := ToolCallResult{
		CallID:  "call_001",
		Content: `{"story_id": "s-001"}`,
		IsError: false,
	}

	data, err := json.Marshal(tcr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ToolCallResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.CallID != "call_001" {
		t.Errorf("CallID = %q, want %q", got.CallID, "call_001")
	}
	if got.IsError {
		t.Error("expected IsError=false")
	}
}

func TestValidateToolCall_ValidSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"complexity": {"type": "integer"}
		},
		"required": ["title"]
	}`)

	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{
		Name:      "test",
		Arguments: json.RawMessage(`{"title": "hello", "complexity": 3}`),
	}

	err := ValidateToolCall(td, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolCall_MissingRequired(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"}
		},
		"required": ["title"]
	}`)

	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{
		Name:      "test",
		Arguments: json.RawMessage(`{"complexity": 3}`),
	}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}
