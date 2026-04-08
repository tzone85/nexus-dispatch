package engine

import (
	"encoding/json"
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// ToolStory holds a single story extracted from a create_story tool call.
// The ID is auto-generated as s-001, s-002, etc.
type ToolStory struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Complexity         int      `json:"complexity"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	DependsOn          []string `json:"depends_on"`
}

// ClarificationRequest holds a question the planner needs answered before
// it can produce a complete plan.
type ClarificationRequest struct {
	Question string `json:"question"`
	Context  string `json:"context"`
}

// PlannerToolResult aggregates the output of processing all planner tool calls.
type PlannerToolResult struct {
	Stories       []ToolStory
	Waves         [][]string
	Clarification *ClarificationRequest
}

// PlannerTools returns the three tool definitions available to the Tech Lead
// planner role: create_story, set_wave_plan, and request_clarification.
func PlannerTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "create_story",
			Description: "Create an implementable story decomposed from the requirement. Call once per story.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {
						"type": "string",
						"description": "Brief title for the story"
					},
					"description": {
						"type": "string",
						"description": "What to implement, including exact file paths"
					},
					"complexity": {
						"type": "integer",
						"description": "Fibonacci complexity score (1, 2, 3, 5, 8, 13)"
					},
					"acceptance_criteria": {
						"type": "string",
						"description": "How to verify the story is done"
					},
					"dependencies": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Story IDs this story depends on (empty if none)"
					}
				},
				"required": ["title", "description", "complexity", "acceptance_criteria"]
			}`),
		},
		{
			Name:        "set_wave_plan",
			Description: "Define the execution wave plan — which stories run in parallel vs sequentially.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"waves": {
						"type": "array",
						"items": {
							"type": "array",
							"items": {"type": "string"}
						},
						"description": "Ordered list of waves; each wave is an array of story IDs that can run in parallel"
					}
				},
				"required": ["waves"]
			}`),
		},
		{
			Name:        "request_clarification",
			Description: "Request clarification from the user before producing a plan.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"question": {
						"type": "string",
						"description": "The clarification question to ask the user"
					},
					"context": {
						"type": "string",
						"description": "Why this clarification is needed"
					}
				},
				"required": ["question"]
			}`),
		},
	}
}

// ProcessPlannerToolCalls iterates over a slice of tool calls from the Tech
// Lead LLM response and builds a PlannerToolResult. Stories are assigned
// sequential IDs (s-001, s-002, ...).
func ProcessPlannerToolCalls(calls []llm.ToolCall) (PlannerToolResult, error) {
	var result PlannerToolResult
	storyCount := 0

	for _, call := range calls {
		switch call.Name {
		case "create_story":
			story, err := parseCreateStory(call.Arguments, storyCount+1)
			if err != nil {
				return PlannerToolResult{}, fmt.Errorf("parse create_story: %w", err)
			}
			storyCount++
			result.Stories = append(result.Stories, story)

		case "set_wave_plan":
			waves, err := parseWavePlan(call.Arguments)
			if err != nil {
				return PlannerToolResult{}, fmt.Errorf("parse set_wave_plan: %w", err)
			}
			result.Waves = waves

		case "request_clarification":
			clarification, err := parseClarification(call.Arguments)
			if err != nil {
				return PlannerToolResult{}, fmt.Errorf("parse request_clarification: %w", err)
			}
			result.Clarification = &clarification

		default:
			return PlannerToolResult{}, fmt.Errorf("unknown planner tool: %s", call.Name)
		}
	}

	return result, nil
}

// createStoryArgs mirrors the JSON schema for the create_story tool.
type createStoryArgs struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Complexity         int      `json:"complexity"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Dependencies       []string `json:"dependencies"`
}

func parseCreateStory(raw json.RawMessage, seq int) (ToolStory, error) {
	var args createStoryArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return ToolStory{}, fmt.Errorf("unmarshal create_story args: %w", err)
	}
	if args.Title == "" {
		return ToolStory{}, fmt.Errorf("create_story: title is required")
	}

	deps := args.Dependencies
	if deps == nil {
		deps = []string{}
	}

	return ToolStory{
		ID:                 fmt.Sprintf("s-%03d", seq),
		Title:              args.Title,
		Description:        args.Description,
		Complexity:         args.Complexity,
		AcceptanceCriteria: args.AcceptanceCriteria,
		DependsOn:          deps,
	}, nil
}

// wavePlanArgs mirrors the JSON schema for the set_wave_plan tool.
type wavePlanArgs struct {
	Waves [][]string `json:"waves"`
}

func parseWavePlan(raw json.RawMessage) ([][]string, error) {
	var args wavePlanArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("unmarshal set_wave_plan args: %w", err)
	}
	return args.Waves, nil
}

func parseClarification(raw json.RawMessage) (ClarificationRequest, error) {
	var req ClarificationRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return ClarificationRequest{}, fmt.Errorf("unmarshal request_clarification args: %w", err)
	}
	if req.Question == "" {
		return ClarificationRequest{}, fmt.Errorf("request_clarification: question is required")
	}
	return req, nil
}
