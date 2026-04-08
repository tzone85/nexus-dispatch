package engine

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestReviewerTools_Definitions(t *testing.T) {
	tools := ReviewerTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 reviewer tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
	if !names["submit_review"] {
		t.Error("missing submit_review tool")
	}
	if !names["request_more_context"] {
		t.Error("missing request_more_context tool")
	}
}

func TestProcessReviewerToolCalls_Approve(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "submit_review",
			Arguments: json.RawMessage(`{
				"verdict": "approve",
				"summary": "Clean implementation with good test coverage",
				"file_comments": [
					{"file": "main.go", "line": 42, "severity": "minor", "message": "Consider renaming"}
				],
				"suggested_changes": []
			}`),
		},
	}

	result, err := ProcessReviewerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Verdict != "approve" {
		t.Errorf("Verdict = %q, want %q", result.Verdict, "approve")
	}
	if len(result.FileComments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(result.FileComments))
	}
	if result.FileComments[0].Severity != "minor" {
		t.Errorf("comment severity = %q", result.FileComments[0].Severity)
	}
}

func TestProcessReviewerToolCalls_RequestChanges(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "submit_review",
			Arguments: json.RawMessage(`{
				"verdict": "request_changes",
				"summary": "Missing error handling",
				"file_comments": [],
				"suggested_changes": [{"file": "main.go", "old_text": "return nil", "new_text": "return fmt.Errorf(\"failed\")"}]
			}`),
		},
	}

	result, err := ProcessReviewerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Verdict != "request_changes" {
		t.Errorf("Verdict = %q", result.Verdict)
	}
	if len(result.SuggestedChanges) != 1 {
		t.Errorf("expected 1 suggested change, got %d", len(result.SuggestedChanges))
	}
}

func TestProcessReviewerToolCalls_InvalidVerdict(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "submit_review",
			Arguments: json.RawMessage(`{"verdict": "maybe", "summary": "not sure"}`),
		},
	}

	_, err := ProcessReviewerToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid verdict")
	}
}

func TestProcessReviewerToolCalls_ContextRequest(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "request_more_context",
			Arguments: json.RawMessage(`{"files": ["config.go", "main.go"], "reason": "Need to check imports"}`),
		},
	}

	result, err := ProcessReviewerToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ContextRequest == nil {
		t.Fatal("expected context request")
	}
	if len(result.ContextRequest.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(result.ContextRequest.Files))
	}
}
