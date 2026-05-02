package metrics

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// Summary aggregates metrics across all recorded LLM calls.
//
// The ByPhase/ByTier/ByStory/ByStage/ByRole maps allow the reporter to
// surface cost from multiple angles without re-scanning the source data.
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
	ByTier            map[int]TierSummary
	ByStory           map[string]StorySummary
	ByStage           map[string]StageSummary
	ByRole            map[string]RoleSummary
}

// PhaseSummary aggregates metrics for a single phase.
type PhaseSummary struct {
	Count     int
	TokensIn  int
	TokensOut int
}

// TierSummary aggregates metrics for a single escalation tier
// (0=junior, 1=senior, 2=manager, 3=tech_lead).
type TierSummary struct {
	Count      int
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// StorySummary aggregates metrics for a single story so per-story cost
// can be surfaced in the reporter / dashboard.
type StorySummary struct {
	Count      int
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// StageSummary aggregates metrics for a pipeline stage
// (planner / dispatcher / executor / reviewer / qa / merger).
type StageSummary struct {
	Count      int
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// RoleSummary aggregates metrics for a particular agent role
// (e.g., "frontend", "backend", "devops").
type RoleSummary struct {
	Count      int
	TokensIn   int
	TokensOut  int
	DurationMs int64
}

// Summarize computes a Summary from a slice of MetricEntry records.
func Summarize(entries []MetricEntry) Summary {
	s := Summary{
		ByPhase: make(map[string]PhaseSummary),
		ByTier:  make(map[int]TierSummary),
		ByStory: make(map[string]StorySummary),
		ByStage: make(map[string]StageSummary),
		ByRole:  make(map[string]RoleSummary),
	}
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

		ts := s.ByTier[e.Tier]
		ts.Count++
		ts.TokensIn += e.TokensIn
		ts.TokensOut += e.TokensOut
		ts.DurationMs += e.DurationMs
		s.ByTier[e.Tier] = ts

		if e.StoryID != "" {
			ss := s.ByStory[e.StoryID]
			ss.Count++
			ss.TokensIn += e.TokensIn
			ss.TokensOut += e.TokensOut
			ss.DurationMs += e.DurationMs
			s.ByStory[e.StoryID] = ss
		}

		if e.Stage != "" {
			sg := s.ByStage[e.Stage]
			sg.Count++
			sg.TokensIn += e.TokensIn
			sg.TokensOut += e.TokensOut
			sg.DurationMs += e.DurationMs
			s.ByStage[e.Stage] = sg
		}

		if e.Role != "" {
			rs := s.ByRole[e.Role]
			rs.Count++
			rs.TokensIn += e.TokensIn
			rs.TokensOut += e.TokensOut
			rs.DurationMs += e.DurationMs
			s.ByRole[e.Role] = rs
		}
	}

	s.TotalRequirements = len(reqs)
	s.TotalStories = len(stories)
	return s
}

// tierLabel maps the integer tier to a human label for the reporter.
func tierLabel(t int) string {
	switch t {
	case 0:
		return "junior"
	case 1:
		return "senior"
	case 2:
		return "manager"
	case 3:
		return "tech_lead"
	default:
		return fmt.Sprintf("tier_%d", t)
	}
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
	fmt.Fprintf(w, "Token usage by phase:\n")
	for _, phase := range sortedKeys(s.ByPhase) {
		ps := s.ByPhase[phase]
		phaseTotal := ps.TokensIn + ps.TokensOut
		fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", phase+":", phaseTotal/1000, ps.Count)
	}
	fmt.Fprintf(w, "  %-14s %6dK tokens\n\n", "Total:", totalTokens/1000)

	if len(s.ByStage) > 0 {
		fmt.Fprintf(w, "Token usage by stage:\n")
		for _, stage := range sortedKeys(s.ByStage) {
			sg := s.ByStage[stage]
			stageTotal := sg.TokensIn + sg.TokensOut
			fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", stage+":", stageTotal/1000, sg.Count)
		}
		fmt.Fprintln(w)
	}

	if len(s.ByTier) > 0 {
		fmt.Fprintf(w, "Token usage by tier:\n")
		tiers := make([]int, 0, len(s.ByTier))
		for t := range s.ByTier {
			tiers = append(tiers, t)
		}
		sort.Ints(tiers)
		for _, t := range tiers {
			ts := s.ByTier[t]
			tierTotal := ts.TokensIn + ts.TokensOut
			fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", tierLabel(t)+":", tierTotal/1000, ts.Count)
		}
		fmt.Fprintln(w)
	}

	if len(s.ByRole) > 0 {
		fmt.Fprintf(w, "Token usage by role:\n")
		for _, role := range sortedKeys(s.ByRole) {
			rs := s.ByRole[role]
			roleTotal := rs.TokensIn + rs.TokensOut
			fmt.Fprintf(w, "  %-14s %6dK tokens (%d calls)\n", role+":", roleTotal/1000, rs.Count)
		}
		fmt.Fprintln(w)
	}

	if len(s.ByStory) > 0 {
		// Cap at top 10 stories by total tokens to keep output bounded.
		type storyRow struct {
			id    string
			total int
			count int
		}
		rows := make([]storyRow, 0, len(s.ByStory))
		for id, ss := range s.ByStory {
			rows = append(rows, storyRow{id: id, total: ss.TokensIn + ss.TokensOut, count: ss.Count})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })
		if len(rows) > 10 {
			rows = rows[:10]
		}
		fmt.Fprintf(w, "Top stories by token usage:\n")
		for _, r := range rows {
			fmt.Fprintf(w, "  %-30s %6dK tokens (%d calls)\n", truncate(r.id, 30), r.total/1000, r.count)
		}
		fmt.Fprintln(w)
	}

	if total > 0 {
		avgMs := s.TotalDurationMs / int64(total)
		fmt.Fprintf(w, "Avg latency: %s per call\n", time.Duration(avgMs)*time.Millisecond)
	}
}

// sortedKeys returns the keys of any string-keyed map in lexicographic order
// so PrintSummary output is deterministic across runs (handy for tests and
// for diffing reports between sessions).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// truncate caps a string at n runes with an ellipsis so long IDs don't
// overflow the column width in PrintSummary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
