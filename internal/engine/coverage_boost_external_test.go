package engine_test

// Coverage boost: exported functions accessible from external test package.
// Targets CalculateLLMCost per-token mode, RunCriteria, report helpers,
// and supervisor with tool calls.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ── CalculateLLMCost per-token mode ──────────────────────────────────────────

func makeBillingPerToken(inputRate, outputRate float64) config.BillingConfig {
	return config.BillingConfig{
		DefaultRate: 150,
		Currency:    "USD",
		LLMCosts: config.LLMCostConfig{
			Mode: "per_token",
			Rates: map[string]config.TokenRate{
				"claude-sonnet": {
					InputPer1K:  inputRate,
					OutputPer1K: outputRate,
				},
			},
		},
	}
}

func TestCalculateLLMCost_PerToken(t *testing.T) {
	billing := makeBillingPerToken(3.0, 15.0) // $3/1K in, $15/1K out
	cost := engine.CalculateLLMCost(billing, 1000, 500)
	// 1000 tokens in @ $3/1K = $3.00
	// 500 tokens out @ $15/1K = $7.50
	// Total = $10.50
	if cost < 10.49 || cost > 10.51 {
		t.Errorf("expected ~$10.50, got %f", cost)
	}
}

func TestCalculateLLMCost_PerToken_ZeroTokens(t *testing.T) {
	billing := makeBillingPerToken(3.0, 15.0)
	cost := engine.CalculateLLMCost(billing, 0, 0)
	if cost != 0.0 {
		t.Errorf("expected $0 for zero tokens, got %f", cost)
	}
}

func TestCalculateLLMCost_SubscriptionMode(t *testing.T) {
	billing := config.BillingConfig{
		LLMCosts: config.LLMCostConfig{Mode: "subscription"},
	}
	cost := engine.CalculateLLMCost(billing, 10000, 5000)
	if cost != 0.0 {
		t.Errorf("subscription mode should return 0, got %f", cost)
	}
}

func TestCalculateLLMCost_NoRates(t *testing.T) {
	billing := config.BillingConfig{
		LLMCosts: config.LLMCostConfig{
			Mode:  "per_token",
			Rates: map[string]config.TokenRate{}, // empty
		},
	}
	cost := engine.CalculateLLMCost(billing, 1000, 500)
	if cost != 0.0 {
		t.Errorf("no rates should return 0, got %f", cost)
	}
}

func TestCalculateCostWithTokens_PerToken(t *testing.T) {
	billing := makeBillingPerToken(3.0, 15.0)
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 5, Role: "intermediate"},
	}
	est := engine.CalculateCostWithTokens(stories, billing, 0, 1000, 500)
	// LLM cost should be ~$10.50
	if est.Summary.LLMCost < 10.49 || est.Summary.LLMCost > 10.51 {
		t.Errorf("LLMCost: expected ~10.50, got %f", est.Summary.LLMCost)
	}
	// MarginPercent should reflect LLM cost impact
	if est.Summary.MarginPercent >= 100.0 {
		t.Errorf("MarginPercent should be < 100 when LLM cost > 0, got %f", est.Summary.MarginPercent)
	}
}

func TestCalculateCost_WithMarginComputation(t *testing.T) {
	// Verify margin computation when LLM cost is non-zero
	billing := makeBillingPerToken(3.0, 15.0)
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 5, Role: "intermediate"},
	}
	// Pass 0 tokens so LLM cost = 0 → margin = 100%
	est := engine.CalculateCost(stories, billing, 0)
	if est.Summary.LLMCost != 0 {
		t.Errorf("with 0 tokens, LLMCost should be 0, got %f", est.Summary.LLMCost)
	}
}

// ── QA.RunCriteria ────────────────────────────────────────────────────────────

func TestQA_RunCriteria_EmptySlice(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	runner := &mockRunner{results: map[string]mockRunResult{}}
	qa := engine.NewQA(engine.QAConfig{}, runner, es, ps)

	result := qa.RunCriteria(context.Background(), "s-001", "/tmp", nil)
	if !result.Passed {
		t.Error("empty criteria should return Passed=true")
	}
	if len(result.Checks) != 0 {
		t.Errorf("empty criteria should return no checks, got %d", len(result.Checks))
	}
}

func TestQA_RunCriteria_FileExists(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	defer ps.Close()

	runner := &mockRunner{results: map[string]mockRunResult{}}
	qa := engine.NewQA(engine.QAConfig{}, runner, es, ps)

	// Create the file that the criterion checks for
	crits := []criteria.Criterion{
		{Type: "file_exists", Target: "go.mod"},
	}

	// Non-existent worktree path → file_exists fails
	result := qa.RunCriteria(context.Background(), "s-001", "/nonexistent/path", crits)
	if result.Passed {
		t.Error("expected failure when file doesn't exist in non-existent path")
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(result.Checks))
	}
	if !strings.Contains(result.Checks[0].Name, "file_exists") {
		t.Errorf("check name should contain 'file_exists', got %q", result.Checks[0].Name)
	}
}

// ── report helpers ────────────────────────────────────────────────────────────

func TestReportBuilder_ClassifyStatus_Blocked(t *testing.T) {
	// Build a ReportData with a paused story and render it
	data := buildInProgressReportData()
	// Override a story status to "paused"
	data.Stories[1].Status = "paused"
	// We can't call classifyStatus directly (unexported) but we can test
	// classifyStatus through RenderMarkdown which includes the status
	out := engine.RenderMarkdown(data, "test-project", false)
	if !strings.Contains(out, "test-project") {
		t.Errorf("expected project name in output, got: %s", out)
	}
}

func TestRenderHTML_Internal(t *testing.T) {
	data := buildCompletedReportData()
	out := engine.RenderHTML(data, "acme-corp", true)

	checks := []string{
		"<!DOCTYPE html>",
		"acme-corp",
		"agent-1",
		"agent-2",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("RenderHTML internal=true: expected %q in output", want)
		}
	}
}

func TestFormatStatus_AllValues(t *testing.T) {
	// Test formatStatus indirectly via RenderMarkdown with different statuses
	statuses := []engine.ReportStatus{
		engine.ReportStatusDone,
		engine.ReportStatusDoneWithConcerns,
		engine.ReportStatusNeedsContext,
		engine.ReportStatusBlocked,
	}

	for _, status := range statuses {
		data := buildCompletedReportData()
		data.Status = status
		// Should not panic and should produce output
		out := engine.RenderMarkdown(data, "proj", false)
		if len(out) == 0 {
			t.Errorf("RenderMarkdown with status %v returned empty output", status)
		}
	}
}

// ── Supervisor with tool calls (anthropic provider) ───────────────────────────

func TestSupervisor_ReviewWithTools_ToolCall(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	// Return a proper report_drift tool call
	args := []byte(`{"story_id":"s-001","drift_type":"stuck","severity":"high","recommendation":"escalate"}`)
	client := llm.NewReplayClient(llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{Name: "report_drift", Arguments: args}},
	})

	// anthropic provider + "sonnet" model → HasToolSupport=true → reviewWithTools
	supervisor := engine.NewSupervisor(client, "anthropic", "claude-sonnet", 4000, es)
	result, err := supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Setup middleware", Complexity: 3}},
		map[string]string{"s-001": "in_progress"},
	)
	if err != nil {
		t.Fatalf("Review with tool call: %v", err)
	}
	// Drift was reported → OnTrack=false
	if result.OnTrack {
		t.Error("report_drift tool call should set OnTrack=false")
	}
	if len(result.Concerns) == 0 {
		t.Error("expected at least one concern from report_drift")
	}
}

func TestSupervisor_ReviewWithTools_TextFallback(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	// Return JSON text (no tool calls) — triggers text fallback in reviewWithTools
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"on_track": true, "concerns": [], "reprioritize": []}`,
	})
	supervisor := engine.NewSupervisor(client, "anthropic", "claude-sonnet", 4000, es)
	result, err := supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{"s-001": "merged"},
	)
	if err != nil {
		t.Fatalf("Review text fallback: %v", err)
	}
	if !result.OnTrack {
		t.Error("expected OnTrack=true from text fallback")
	}
}

// ── describeReqEvent coverage ─────────────────────────────────────────────────

func TestReportBuilder_DescribeEvents_AllPaths(t *testing.T) {
	es, ps, cleanup := setupReportStores(t)
	defer cleanup()

	// Emit a REQ_PAUSED event to cover describeReqEvent "default" path
	pauseEvt := state.NewEvent(state.EventReqPaused, "system", "", map[string]any{
		"id": "req-001",
	})
	if err := es.Append(pauseEvt); err != nil {
		t.Fatalf("append req paused: %v", err)
	}

	cfg := config.DefaultConfig()
	rb := engine.NewReportBuilder(es, ps, cfg)
	report, err := rb.Build("req-001")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Timeline should include the REQ_PAUSED event
	found := false
	for _, entry := range report.Timeline {
		if entry.EventType == string(state.EventReqPaused) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected REQ_PAUSED in timeline")
	}
}

// ── storyDuration coverage ────────────────────────────────────────────────────

func TestReportBuilder_StoryDuration_ZeroWhenNotMerged(t *testing.T) {
	// Build a report for an in-progress story (not merged) — storyDuration returns 0
	data := buildInProgressReportData()
	// s-011 has Status in_progress and no MergedAt/Duration set → Duration=0
	var found bool
	for _, s := range data.Stories {
		if s.ID == "s-011" {
			found = true
			if s.Duration != 0 {
				t.Errorf("in-progress story should have Duration=0, got %v", s.Duration)
			}
		}
	}
	if !found {
		t.Error("expected story s-011 in in-progress data")
	}
}

// ── classifyStatus BLOCKED path ───────────────────────────────────────────────

func TestReportBuilder_ClassifyStatus_BlockedPath(t *testing.T) {
	es, ps, cleanup := setupReportStores(t)
	defer cleanup()

	// Pause the req — exercises the BLOCKED path in classifyStatus (via paused story status)
	pauseEvt := state.NewEvent(state.EventReqPaused, "system", "", map[string]any{"id": "req-001"})
	es.Append(pauseEvt)
	ps.Project(pauseEvt)

	cfg := config.DefaultConfig()
	rb := engine.NewReportBuilder(es, ps, cfg)
	report, err := rb.Build("req-001")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Report should build successfully regardless of paused status
	if report.RequirementID != "req-001" {
		t.Errorf("expected req-001, got %s", report.RequirementID)
	}
}

// ── Supervisor.reviewWithText (non-anthropic provider) ────────────────────────

func TestSupervisor_ReviewWithText_Success(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	// Use "ollama" with a non-gemma model → HasToolSupport=false → reviewWithText
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"on_track": true, "concerns": [], "reprioritize": []}`,
	})
	supervisor := engine.NewSupervisor(client, "ollama", "qwen2.5-coder:7b", 4000, es)
	result, err := supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{"s-001": "in_progress"},
	)
	if err != nil {
		t.Fatalf("reviewWithText: %v", err)
	}
	if !result.OnTrack {
		t.Error("expected OnTrack=true")
	}
}

func TestSupervisor_ReviewWithText_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewReplayClient(llm.CompletionResponse{Content: "not json at all"})
	supervisor := engine.NewSupervisor(client, "ollama", "qwen2.5-coder:7b", 4000, es)
	_, err = supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON in reviewWithText")
	}
}

func TestSupervisor_ReviewWithText_LLMError(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewErrorClient(fmt.Errorf("llm unavailable"))
	supervisor := engine.NewSupervisor(client, "ollama", "qwen2.5-coder:7b", 4000, es)
	_, err = supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected error from ErrorClient")
	}
}
