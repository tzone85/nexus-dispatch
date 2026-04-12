package web

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

func TestConvertSummary(t *testing.T) {
	s := metrics.Summary{
		TotalRequirements: 2,
		TotalStories:      8,
		TotalTokensIn:     10000,
		TotalTokensOut:    5000,
		SuccessCount:      7,
		FailureCount:      1,
		TotalDurationMs:   16000,
		EscalationCount:   2,
		ByPhase: map[string]metrics.PhaseSummary{
			"planning": {Count: 3, TokensIn: 5000, TokensOut: 2000},
			"review":   {Count: 5, TokensIn: 5000, TokensOut: 3000},
		},
	}

	ms := convertSummary(s)

	if ms.TotalRequirements != 2 {
		t.Errorf("TotalRequirements = %d, want 2", ms.TotalRequirements)
	}
	if ms.TotalStories != 8 {
		t.Errorf("TotalStories = %d, want 8", ms.TotalStories)
	}
	if ms.TotalTokens != 15000 {
		t.Errorf("TotalTokens = %d, want 15000", ms.TotalTokens)
	}
	if ms.SuccessRate < 87 || ms.SuccessRate > 88 {
		t.Errorf("SuccessRate = %.1f, want ~87.5", ms.SuccessRate)
	}
	if ms.AvgLatencyMs != 2000 {
		t.Errorf("AvgLatencyMs = %d, want 2000", ms.AvgLatencyMs)
	}
	if ms.EscalationCount != 2 {
		t.Errorf("EscalationCount = %d, want 2", ms.EscalationCount)
	}
	if len(ms.ByPhase) != 2 {
		t.Fatalf("ByPhase count = %d, want 2", len(ms.ByPhase))
	}
	if ms.ByPhase["planning"].Count != 3 {
		t.Errorf("planning count = %d, want 3", ms.ByPhase["planning"].Count)
	}
	if ms.ByPhase["planning"].Tokens != 7000 {
		t.Errorf("planning tokens = %d, want 7000", ms.ByPhase["planning"].Tokens)
	}
}

func TestConvertSummary_ZeroCalls(t *testing.T) {
	s := metrics.Summary{}
	ms := convertSummary(s)
	if ms.SuccessRate != 0 {
		t.Errorf("SuccessRate = %.1f, want 0 for zero calls", ms.SuccessRate)
	}
	if ms.AvgLatencyMs != 0 {
		t.Errorf("AvgLatencyMs = %d, want 0 for zero calls", ms.AvgLatencyMs)
	}
}

func TestMemPalaceCheck_Nil(t *testing.T) {
	result := MemPalaceCheck(nil)
	if result != nil {
		t.Errorf("expected nil for nil MemPalace, got %v", result)
	}
}
