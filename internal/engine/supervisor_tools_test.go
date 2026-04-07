package engine

import (
	"encoding/json"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestSupervisorTools_Definitions(t *testing.T) {
	tools := SupervisorTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 supervisor tools, got %d", len(tools))
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

	if !names["report_drift"] {
		t.Error("missing report_drift tool")
	}
	if !names["reprioritize"] {
		t.Error("missing reprioritize tool")
	}
}

func TestProcessSupervisorToolCalls_ReportDrift(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "report_drift",
			Arguments: json.RawMessage(`{
				"story_id": "s-003",
				"drift_type": "stuck",
				"severity": "high",
				"recommendation": "reassign"
			}`),
		},
	}

	result, err := ProcessSupervisorToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(result.Drifts))
	}
	if result.Drifts[0].DriftType != "stuck" {
		t.Errorf("drift_type = %q", result.Drifts[0].DriftType)
	}
	if result.Drifts[0].Severity != "high" {
		t.Errorf("severity = %q", result.Drifts[0].Severity)
	}
}

func TestProcessSupervisorToolCalls_InvalidDriftType(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "report_drift",
			Arguments: json.RawMessage(`{
				"story_id": "s-001",
				"drift_type": "invalid_type",
				"severity": "low",
				"recommendation": "continue"
			}`),
		},
	}

	_, err := ProcessSupervisorToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid drift_type")
	}
}

func TestProcessSupervisorToolCalls_InvalidSeverity(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "report_drift",
			Arguments: json.RawMessage(`{
				"story_id": "s-001",
				"drift_type": "stuck",
				"severity": "extreme",
				"recommendation": "continue"
			}`),
		},
	}

	_, err := ProcessSupervisorToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func TestProcessSupervisorToolCalls_InvalidRecommendation(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "report_drift",
			Arguments: json.RawMessage(`{
				"story_id": "s-001",
				"drift_type": "stuck",
				"severity": "low",
				"recommendation": "delete"
			}`),
		},
	}

	_, err := ProcessSupervisorToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for invalid recommendation")
	}
}

func TestProcessSupervisorToolCalls_Reprioritize(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "reprioritize",
			Arguments: json.RawMessage(`{
				"story_id": "s-002",
				"new_wave": 3,
				"reason": "Dependency resolved"
			}`),
		},
	}

	result, err := ProcessSupervisorToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reprioritizations) != 1 {
		t.Fatalf("expected 1 reprioritization, got %d", len(result.Reprioritizations))
	}
	if result.Reprioritizations[0].NewWave != 3 {
		t.Errorf("new_wave = %d", result.Reprioritizations[0].NewWave)
	}
}

func TestProcessSupervisorToolCalls_MultipleCalls(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name: "report_drift",
			Arguments: json.RawMessage(`{
				"story_id": "s-001",
				"drift_type": "scope_creep",
				"severity": "medium",
				"recommendation": "continue"
			}`),
		},
		{
			Name: "reprioritize",
			Arguments: json.RawMessage(`{
				"story_id": "s-002",
				"new_wave": 2,
				"reason": "Blocked by infra"
			}`),
		},
	}

	result, err := ProcessSupervisorToolCalls(calls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(result.Drifts))
	}
	if len(result.Reprioritizations) != 1 {
		t.Fatalf("expected 1 reprioritization, got %d", len(result.Reprioritizations))
	}
}

func TestProcessSupervisorToolCalls_NoRecognized(t *testing.T) {
	calls := []llm.ToolCall{
		{
			Name:      "unknown_tool",
			Arguments: json.RawMessage(`{}`),
		},
	}

	_, err := ProcessSupervisorToolCalls(calls)
	if err == nil {
		t.Fatal("expected error for unrecognized tool call")
	}
}
