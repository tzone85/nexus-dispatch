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

// SG-4 security: type validation tests

func TestValidateToolCall_TypeMismatch_StringGotNumber(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"title": {"type": "string"}},
		"required": ["title"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{"title": 42}`)}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected type mismatch error: string field got number")
	}
}

func TestValidateToolCall_TypeMismatch_NumberGotString(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"count": {"type": "integer"}},
		"required": ["count"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{"count": "not a number"}`)}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected type mismatch error: integer field got string")
	}
}

func TestValidateToolCall_TypeMismatch_BoolGotString(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"enabled": {"type": "boolean"}},
		"required": ["enabled"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{"enabled": "yes"}`)}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected type mismatch error: boolean field got string")
	}
}

func TestValidateToolCall_TypeMismatch_ArrayGotObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"items": {"type": "array"}},
		"required": ["items"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{"items": {"key": "val"}}`)}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected type mismatch error: array field got object")
	}
}

func TestValidateToolCall_TypeMatch_AllTypes(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"count": {"type": "integer"},
			"ratio": {"type": "number"},
			"active": {"type": "boolean"},
			"tags": {"type": "array"},
			"meta": {"type": "object"}
		},
		"required": ["name", "count"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{
		"name": "hello",
		"count": 5,
		"ratio": 3.14,
		"active": true,
		"tags": ["a", "b"],
		"meta": {"key": "val"}
	}`)}

	if err := ValidateToolCall(td, tc); err != nil {
		t.Fatalf("expected all types to pass validation, got: %v", err)
	}
}

func TestValidateToolCall_OptionalField_Absent(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"optional": {"type": "integer"}
		},
		"required": ["name"]
	}`)
	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{Name: "test", Arguments: json.RawMessage(`{"name": "hello"}`)}

	if err := ValidateToolCall(td, tc); err != nil {
		t.Fatalf("optional absent field should not cause error: %v", err)
	}
}

// SG-6 security: response truncation tests

func TestTruncateContent_UnderLimit(t *testing.T) {
	result := TruncateContent("short", 100)
	if result != "short" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestTruncateContent_ExactLimit(t *testing.T) {
	input := "12345"
	result := TruncateContent(input, 5)
	if result != "12345" {
		t.Errorf("expected unchanged at exact limit, got %q", result)
	}
}

func TestTruncateContent_OverLimit(t *testing.T) {
	input := "1234567890"
	result := TruncateContent(input, 5)
	if len(result) <= 5 {
		t.Errorf("expected truncation notice appended, got len %d", len(result))
	}
	if result[:5] != "12345" {
		t.Errorf("expected first 5 chars preserved, got %q", result[:5])
	}
	if !contains(result, "truncated") {
		t.Error("expected 'truncated' in output")
	}
}

func TestTruncateContent_ZeroLimit(t *testing.T) {
	result := TruncateContent("anything", 0)
	if result != "anything" {
		t.Errorf("zero limit should return unchanged, got %q", result)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
