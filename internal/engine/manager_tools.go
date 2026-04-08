package engine

import (
	"encoding/json"
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// validEscalationActions defines the allowed values for the escalation_decision
// action field.
var validEscalationActions = map[string]bool{
	"reassign_higher_tier": true,
	"split_story":          true,
	"mark_blocked":         true,
	"retry":                true,
	"abandon":              true,
}

// EscalationDecision represents a structured escalation decision returned by
// the manager tool call.
type EscalationDecision struct {
	StoryID    string `json:"story_id"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	AssignedTo string `json:"assigned_to,omitempty"`
}

// StorySplit represents a decision to split a story into smaller parts.
type StorySplit struct {
	OriginalStoryID string              `json:"original_story_id"`
	NewStories      []ManagerSplitChild `json:"new_stories"`
}

// ManagerSplitChild holds the definition for one child of a split story
// created via the split_story manager tool. This is distinct from the
// escalation.SplitChild type which carries additional fields (ID, Suffix,
// AcceptanceCriteria, OwnedFiles) used during split execution.
type ManagerSplitChild struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Complexity  int    `json:"complexity"`
}

// ManagerToolResult holds the structured output from processing manager tool
// calls. Exactly one of the tool-specific fields will be populated.
type ManagerToolResult struct {
	Decision *EscalationDecision `json:"decision,omitempty"`
	Split    *StorySplit         `json:"split,omitempty"`
}

// ManagerTools returns the tool definitions available to the manager agent.
// It defines two tools:
//   - escalation_decision: decide how to handle a failed/escalated story
//   - split_story: decompose a story into smaller children
func ManagerTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "escalation_decision",
			Description: "Decide how to handle a failed or escalated story. Choose an action and optionally reassign to a specific agent.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"story_id": {
						"type": "string",
						"description": "The ID of the story being escalated"
					},
					"action": {
						"type": "string",
						"enum": ["reassign_higher_tier", "split_story", "mark_blocked", "retry", "abandon"],
						"description": "The escalation action to take"
					},
					"reason": {
						"type": "string",
						"description": "Explanation of why this action was chosen"
					},
					"assigned_to": {
						"type": "string",
						"description": "Agent ID to reassign to (for reassign_higher_tier)"
					}
				},
				"required": ["story_id", "action", "reason"]
			}`),
		},
		{
			Name:        "split_story",
			Description: "Split a story into smaller, more manageable child stories when the original is too complex.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"original_story_id": {
						"type": "string",
						"description": "The ID of the story to split"
					},
					"new_stories": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"title": {"type": "string", "description": "Title for the child story"},
								"description": {"type": "string", "description": "Description of what this child story covers"},
								"complexity": {"type": "integer", "description": "Estimated complexity (1-8)"}
							},
							"required": ["title", "description", "complexity"]
						},
						"description": "The child stories to create from the split"
					}
				},
				"required": ["original_story_id", "new_stories"]
			}`),
		},
	}
}

// ProcessManagerToolCalls processes tool calls from the manager LLM response.
// It validates the action enum and returns a structured ManagerToolResult.
func ProcessManagerToolCalls(calls []llm.ToolCall) (ManagerToolResult, error) {
	for _, call := range calls {
		switch call.Name {
		case "escalation_decision":
			return processEscalationDecision(call.Arguments)
		case "split_story":
			return processSplitStory(call.Arguments)
		}
	}
	return ManagerToolResult{}, fmt.Errorf("no recognized manager tool call found")
}

// processEscalationDecision unmarshals and validates an escalation_decision
// tool call.
func processEscalationDecision(args json.RawMessage) (ManagerToolResult, error) {
	var decision EscalationDecision
	if err := json.Unmarshal(args, &decision); err != nil {
		return ManagerToolResult{}, fmt.Errorf("parse escalation_decision arguments: %w", err)
	}

	if !validEscalationActions[decision.Action] {
		return ManagerToolResult{}, fmt.Errorf("invalid escalation action %q: must be one of reassign_higher_tier, split_story, mark_blocked, retry, abandon", decision.Action)
	}

	return ManagerToolResult{Decision: &decision}, nil
}

// processSplitStory unmarshals a split_story tool call.
func processSplitStory(args json.RawMessage) (ManagerToolResult, error) {
	var split StorySplit
	if err := json.Unmarshal(args, &split); err != nil {
		return ManagerToolResult{}, fmt.Errorf("parse split_story arguments: %w", err)
	}

	return ManagerToolResult{Split: &split}, nil
}
