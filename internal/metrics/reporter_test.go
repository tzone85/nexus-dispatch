package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintSummary_WithData(t *testing.T) {
	entries := []MetricEntry{
		{ReqID: "r1", StoryID: "s1", Phase: "plan", TokensIn: 5000, TokensOut: 2000, DurationMs: 1200, Success: true},
		{ReqID: "r1", StoryID: "s2", Phase: "execute", TokensIn: 8000, TokensOut: 3000, DurationMs: 2400, Success: true, Escalated: true},
		{ReqID: "r2", StoryID: "s3", Phase: "review", TokensIn: 2000, TokensOut: 1000, DurationMs: 800, Success: false},
	}
	s := Summarize(entries)

	var buf bytes.Buffer
	PrintSummary(&buf, s)
	out := buf.String()

	if !strings.Contains(out, "Requirements: 2") {
		t.Errorf("expected Requirements: 2, got:\n%s", out)
	}
	if !strings.Contains(out, "Stories: 3") {
		t.Errorf("expected Stories: 3, got:\n%s", out)
	}
	// 2 success + 1 failure = 3 calls, 66% success rate
	if !strings.Contains(out, "LLM calls: 3") {
		t.Errorf("expected LLM calls: 3, got:\n%s", out)
	}
	if !strings.Contains(out, "Escalations: 1") {
		t.Errorf("expected Escalations: 1, got:\n%s", out)
	}
	if !strings.Contains(out, "Token usage:") {
		t.Errorf("expected Token usage: section, got:\n%s", out)
	}
	// Avg latency line should appear because total > 0
	if !strings.Contains(out, "Avg latency:") {
		t.Errorf("expected Avg latency: line, got:\n%s", out)
	}
}

func TestPrintSummary_ZeroCallsNoLatencyLine(t *testing.T) {
	s := Summarize([]MetricEntry{})

	var buf bytes.Buffer
	PrintSummary(&buf, s)
	out := buf.String()

	if !strings.Contains(out, "LLM calls: 0") {
		t.Errorf("expected LLM calls: 0, got:\n%s", out)
	}
	// success rate should be 0 when there are no calls
	if !strings.Contains(out, "0%") {
		t.Errorf("expected 0%% success rate, got:\n%s", out)
	}
	// Avg latency line must be absent when total == 0
	if strings.Contains(out, "Avg latency:") {
		t.Errorf("unexpected Avg latency: line when no calls:\n%s", out)
	}
}

func TestPrintSummary_TokenTotalsInOutput(t *testing.T) {
	// Use token counts large enough that the /1000 display is non-zero and verifiable.
	entries := []MetricEntry{
		{ReqID: "r1", StoryID: "s1", Phase: "plan", TokensIn: 10_000, TokensOut: 5_000, DurationMs: 500, Success: true},
	}
	s := Summarize(entries)

	var buf bytes.Buffer
	PrintSummary(&buf, s)
	out := buf.String()

	// Total tokens = 15 000 → 15K
	if !strings.Contains(out, "15K") {
		t.Errorf("expected 15K total tokens in output, got:\n%s", out)
	}
	// Phase "plan" tokens = 15 000 → 15K
	if !strings.Contains(out, "plan:") {
		t.Errorf("expected plan: phase in output, got:\n%s", out)
	}
}

func TestPrintSummary_100PercentSuccess(t *testing.T) {
	entries := []MetricEntry{
		{ReqID: "r1", StoryID: "s1", Phase: "plan", TokensIn: 100, TokensOut: 50, DurationMs: 300, Success: true},
		{ReqID: "r1", StoryID: "s2", Phase: "plan", TokensIn: 200, TokensOut: 80, DurationMs: 400, Success: true},
	}
	s := Summarize(entries)

	var buf bytes.Buffer
	PrintSummary(&buf, s)
	out := buf.String()

	if !strings.Contains(out, "100%") {
		t.Errorf("expected 100%% success rate, got:\n%s", out)
	}
}
