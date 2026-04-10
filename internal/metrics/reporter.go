package metrics

import (
	"fmt"
	"io"
	"time"
)

// Summary aggregates metrics across all recorded LLM calls.
type Summary struct {
	TotalRequirements int
	TotalStories      int
	TotalTokensIn     int
	TotalTokensOut    int
	TotalDurationMs   int64
	SuccessCount      int
	FailureCount      int
	EscalationCount   int
	ByPhase           map[string]PhaseSummary
}

// PhaseSummary aggregates metrics for a single phase.
type PhaseSummary struct {
	Count     int
	TokensIn  int
	TokensOut int
}

// Summarize computes a Summary from a slice of MetricEntry records.
func Summarize(entries []MetricEntry) Summary {
	s := Summary{ByPhase: make(map[string]PhaseSummary)}
	reqs := map[string]bool{}
	stories := map[string]bool{}

	for _, e := range entries {
		reqs[e.ReqID] = true
		if e.StoryID != "" {
			stories[e.StoryID] = true
		}
		s.TotalTokensIn += e.TokensIn
		s.TotalTokensOut += e.TokensOut
		s.TotalDurationMs += e.DurationMs
		if e.Success {
			s.SuccessCount++
		} else {
			s.FailureCount++
		}
		if e.Escalated {
			s.EscalationCount++
		}
		ps := s.ByPhase[e.Phase]
		ps.Count++
		ps.TokensIn += e.TokensIn
		ps.TokensOut += e.TokensOut
		s.ByPhase[e.Phase] = ps
	}

	s.TotalRequirements = len(reqs)
	s.TotalStories = len(stories)
	return s
}

// PrintSummary writes a human-readable summary to the given writer.
func PrintSummary(w io.Writer, s Summary) {
	total := s.SuccessCount + s.FailureCount
	successRate := 0.0
	if total > 0 {
		successRate = float64(s.SuccessCount) / float64(total) * 100
	}

	fmt.Fprintf(w, "Requirements: %d | Stories: %d\n", s.TotalRequirements, s.TotalStories)
	fmt.Fprintf(w, "LLM calls: %d (%.0f%% success)\n", total, successRate)
	fmt.Fprintf(w, "Escalations: %d\n\n", s.EscalationCount)

	totalTokens := s.TotalTokensIn + s.TotalTokensOut
	fmt.Fprintf(w, "Token usage:\n")
	for phase, ps := range s.ByPhase {
		phaseTotal := ps.TokensIn + ps.TokensOut
		fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", phase+":", phaseTotal/1000, ps.Count)
	}
	fmt.Fprintf(w, "  %-14s %6dK tokens\n\n", "Total:", totalTokens/1000)

	if total > 0 {
		avgMs := s.TotalDurationMs / int64(total)
		fmt.Fprintf(w, "Avg latency: %s per call\n", time.Duration(avgMs)*time.Millisecond)
	}
}
