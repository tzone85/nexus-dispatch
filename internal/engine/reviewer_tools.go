package engine

import (
	"encoding/json"
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// validVerdicts defines the allowed values for the submit_review verdict field.
var validVerdicts = map[string]bool{
	"approve":         true,
	"request_changes": true,
	"reject":          true,
}

// ReviewFileComment represents a reviewer comment attached to a specific file
// and line.
type ReviewFileComment struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// ReviewSuggestedChange represents a concrete code change the reviewer
// suggests.
type ReviewSuggestedChange struct {
	File    string `json:"file"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// ReviewContextRequest is returned when the reviewer needs additional files
// before it can complete its review.
type ReviewContextRequest struct {
	Files  []string `json:"files"`
	Reason string   `json:"reason"`
}

// ReviewToolResult holds the structured output from processing reviewer tool
// calls. Exactly one of the tool-specific fields will be populated.
type ReviewToolResult struct {
	Verdict          string                 `json:"verdict,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	FileComments     []ReviewFileComment    `json:"file_comments,omitempty"`
	SuggestedChanges []ReviewSuggestedChange `json:"suggested_changes,omitempty"`
	ContextRequest   *ReviewContextRequest  `json:"context_request,omitempty"`
}

// ReviewerTools returns the tool definitions available to the reviewer agent.
// It defines two tools:
//   - submit_review: structured code review with verdict, comments, and
//     suggested changes
//   - request_more_context: ask for additional file contents before reviewing
func ReviewerTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "submit_review",
			Description: "Submit a structured code review with a verdict, summary, file-level comments, and optional suggested changes.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"verdict": {
						"type": "string",
						"enum": ["approve", "request_changes", "reject"],
						"description": "Review verdict: approve, request_changes, or reject"
					},
					"summary": {
						"type": "string",
						"description": "Brief summary of the review findings"
					},
					"file_comments": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"file": {"type": "string", "description": "File path"},
								"line": {"type": "integer", "description": "Line number"},
								"severity": {"type": "string", "enum": ["critical", "major", "minor", "info"], "description": "Comment severity"},
								"message": {"type": "string", "description": "Review comment"}
							},
							"required": ["file", "line", "severity", "message"]
						},
						"description": "Line-level review comments"
					},
					"suggested_changes": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"file": {"type": "string", "description": "File path"},
								"old_text": {"type": "string", "description": "Text to replace"},
								"new_text": {"type": "string", "description": "Replacement text"}
							},
							"required": ["file", "old_text", "new_text"]
						},
						"description": "Concrete code changes to suggest"
					}
				},
				"required": ["verdict", "summary"]
			}`),
		},
		{
			Name:        "request_more_context",
			Description: "Request additional file contents before completing the review. Use when the diff alone is insufficient to evaluate correctness.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"files": {
						"type": "array",
						"items": {"type": "string"},
						"description": "File paths to retrieve"
					},
					"reason": {
						"type": "string",
						"description": "Why these files are needed for the review"
					}
				},
				"required": ["files", "reason"]
			}`),
		},
	}
}

// ProcessReviewerToolCalls processes tool calls from the reviewer LLM
// response. It validates the verdict and returns a structured ReviewToolResult.
func ProcessReviewerToolCalls(calls []llm.ToolCall) (ReviewToolResult, error) {
	for _, call := range calls {
		switch call.Name {
		case "submit_review":
			return processSubmitReview(call.Arguments)
		case "request_more_context":
			return processContextRequest(call.Arguments)
		}
	}
	return ReviewToolResult{}, fmt.Errorf("no recognized tool call found")
}

// processSubmitReview unmarshals and validates a submit_review tool call.
func processSubmitReview(args json.RawMessage) (ReviewToolResult, error) {
	var raw struct {
		Verdict          string                 `json:"verdict"`
		Summary          string                 `json:"summary"`
		FileComments     []ReviewFileComment    `json:"file_comments"`
		SuggestedChanges []ReviewSuggestedChange `json:"suggested_changes"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return ReviewToolResult{}, fmt.Errorf("parse submit_review arguments: %w", err)
	}

	if !validVerdicts[raw.Verdict] {
		return ReviewToolResult{}, fmt.Errorf("invalid verdict %q: must be one of approve, request_changes, reject", raw.Verdict)
	}

	return ReviewToolResult{
		Verdict:          raw.Verdict,
		Summary:          raw.Summary,
		FileComments:     raw.FileComments,
		SuggestedChanges: raw.SuggestedChanges,
	}, nil
}

// processContextRequest unmarshals a request_more_context tool call.
func processContextRequest(args json.RawMessage) (ReviewToolResult, error) {
	var req ReviewContextRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return ReviewToolResult{}, fmt.Errorf("parse request_more_context arguments: %w", err)
	}

	return ReviewToolResult{
		ContextRequest: &req,
	}, nil
}
