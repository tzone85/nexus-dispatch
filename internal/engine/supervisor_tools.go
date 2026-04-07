package engine

import (
	"encoding/json"
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// validDriftTypes defines the allowed values for the report_drift drift_type field.
var validDriftTypes = map[string]bool{
	"scope_creep":        true,
	"stuck":              true,
	"quality_regression": true,
	"dependency_blocked": true,
}

// validSeverities defines the allowed values for the report_drift severity field.
var validSeverities = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

// validRecommendations defines the allowed values for the report_drift recommendation field.
var validRecommendations = map[string]bool{
	"continue": true,
	"reassign": true,
	"escalate": true,
	"pause":    true,
}

// DriftReport represents a single drift detection from the supervisor.
type DriftReport struct {
	StoryID        string `json:"story_id"`
	DriftType      string `json:"drift_type"`
	Severity       string `json:"severity"`
	Recommendation string `json:"recommendation"`
}

// Reprioritization represents a request to move a story to a different wave.
type Reprioritization struct {
	StoryID string `json:"story_id"`
	NewWave int    `json:"new_wave"`
	Reason  string `json:"reason"`
}

// SupervisorToolResult holds the structured output from processing supervisor
// tool calls. Both Drifts and Reprioritizations may be populated when the LLM
// makes multiple tool calls in a single response.
type SupervisorToolResult struct {
	Drifts           []DriftReport    `json:"drifts,omitempty"`
	Reprioritizations []Reprioritization `json:"reprioritizations,omitempty"`
}

// SupervisorTools returns the tool definitions available to the supervisor agent.
// It defines two tools:
//   - report_drift: report that a story is drifting from the requirement
//   - reprioritize: request that a story be moved to a different wave
func SupervisorTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "report_drift",
			Description: "Report that a story is drifting from the original requirement. Includes drift type, severity, and a recommended action.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"story_id": {
						"type": "string",
						"description": "The story ID that is drifting"
					},
					"drift_type": {
						"type": "string",
						"enum": ["scope_creep", "stuck", "quality_regression", "dependency_blocked"],
						"description": "Type of drift detected"
					},
					"severity": {
						"type": "string",
						"enum": ["low", "medium", "high", "critical"],
						"description": "Severity of the drift"
					},
					"recommendation": {
						"type": "string",
						"enum": ["continue", "reassign", "escalate", "pause"],
						"description": "Recommended action to address the drift"
					}
				},
				"required": ["story_id", "drift_type", "severity", "recommendation"]
			}`),
		},
		{
			Name:        "reprioritize",
			Description: "Request that a story be moved to a different execution wave. Use when dependencies change or priorities shift.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"story_id": {
						"type": "string",
						"description": "The story ID to reprioritize"
					},
					"new_wave": {
						"type": "integer",
						"description": "The new wave number for the story"
					},
					"reason": {
						"type": "string",
						"description": "Why the story should be reprioritized"
					}
				},
				"required": ["story_id", "new_wave", "reason"]
			}`),
		},
	}
}

// ProcessSupervisorToolCalls processes tool calls from the supervisor LLM
// response. It validates enum fields and accumulates results from all
// recognized tool calls. Returns an error if no recognized tool calls are
// found or if any validation fails.
func ProcessSupervisorToolCalls(calls []llm.ToolCall) (SupervisorToolResult, error) {
	var result SupervisorToolResult
	recognized := false

	for _, call := range calls {
		switch call.Name {
		case "report_drift":
			drift, err := processReportDrift(call.Arguments)
			if err != nil {
				return SupervisorToolResult{}, err
			}
			result.Drifts = append(result.Drifts, drift)
			recognized = true
		case "reprioritize":
			repri, err := processReprioritize(call.Arguments)
			if err != nil {
				return SupervisorToolResult{}, err
			}
			result.Reprioritizations = append(result.Reprioritizations, repri)
			recognized = true
		}
	}

	if !recognized {
		return SupervisorToolResult{}, fmt.Errorf("no recognized supervisor tool call found")
	}

	return result, nil
}

// processReportDrift unmarshals and validates a report_drift tool call.
func processReportDrift(args json.RawMessage) (DriftReport, error) {
	var raw DriftReport
	if err := json.Unmarshal(args, &raw); err != nil {
		return DriftReport{}, fmt.Errorf("parse report_drift arguments: %w", err)
	}

	if !validDriftTypes[raw.DriftType] {
		return DriftReport{}, fmt.Errorf("invalid drift_type %q: must be one of scope_creep, stuck, quality_regression, dependency_blocked", raw.DriftType)
	}

	if !validSeverities[raw.Severity] {
		return DriftReport{}, fmt.Errorf("invalid severity %q: must be one of low, medium, high, critical", raw.Severity)
	}

	if !validRecommendations[raw.Recommendation] {
		return DriftReport{}, fmt.Errorf("invalid recommendation %q: must be one of continue, reassign, escalate, pause", raw.Recommendation)
	}

	return raw, nil
}

// processReprioritize unmarshals a reprioritize tool call.
func processReprioritize(args json.RawMessage) (Reprioritization, error) {
	var raw Reprioritization
	if err := json.Unmarshal(args, &raw); err != nil {
		return Reprioritization{}, fmt.Errorf("parse reprioritize arguments: %w", err)
	}

	return raw, nil
}
