package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHasToolSupport_Gemma4(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     bool
	}{
		{"ollama", "gemma4:26b", true},
		{"ollama", "gemma4:31b", true},
		{"ollama", "gemma4:e4b", true},
		{"google", "gemma-4-26b", true},
		{"google+ollama", "gemma4:26b", true},
		{"anthropic", "claude-opus-4-20250514", true},
		{"openai", "gpt-4o", true},
		{"ollama", "deepseek-coder-v2:latest", false},
		{"ollama", "qwen2.5-coder:14b", false},
		{"ollama", "codellama:13b", false},
	}

	for _, tt := range tests {
		got := HasToolSupport(tt.provider, tt.model)
		if got != tt.want {
			t.Errorf("HasToolSupport(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestInjectToolSchema_ProducesValidPrompt(t *testing.T) {
	tools := []ToolDefinition{
		{
			Name:        "create_story",
			Description: "Create a story",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		},
	}

	result := InjectToolSchema("You are a planner.", tools)

	if !strings.Contains(result, "create_story") {
		t.Error("expected tool name in injected prompt")
	}
	if !strings.Contains(result, "You are a planner.") {
		t.Error("expected original system prompt preserved")
	}
	if !strings.Contains(result, "JSON") {
		t.Error("expected JSON instruction in injected prompt")
	}
}

func TestInjectToolSchema_EmptyTools(t *testing.T) {
	result := InjectToolSchema("You are a planner.", nil)
	if result != "You are a planner." {
		t.Errorf("expected unchanged prompt for empty tools, got %q", result)
	}
}

func TestParseToolCallsFromText_Valid(t *testing.T) {
	text := `{"tool_calls": [{"name": "create_story", "arguments": {"title": "Auth"}}]}`

	calls, err := ParseToolCallsFromText(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "create_story" {
		t.Errorf("Name = %q, want %q", calls[0].Name, "create_story")
	}
}

func TestParseToolCallsFromText_WithCodeFences(t *testing.T) {
	text := "```json\n{\"tool_calls\": [{\"name\": \"submit_review\", \"arguments\": {\"verdict\": \"approve\"}}]}\n```"

	calls, err := ParseToolCallsFromText(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestParseToolCallsFromText_Invalid(t *testing.T) {
	_, err := ParseToolCallsFromText("this is not json at all")
	if err == nil {
		t.Fatal("expected error for non-JSON text")
	}
}
