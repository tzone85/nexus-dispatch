package improver

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// defaultAnalyzers returns the built-in heuristic set. Tests can pass
// their own list via WithAnalyzer and an empty seed.
func defaultAnalyzers() []Analyzer {
	return []Analyzer{
		MetricsAnalyzer{
			HighFailureRate:        25.0, // %
			HighEscalationCount:    5,
			HighAvgLatencyMs:       4000,
			TokensPerStoryWarning:  50_000, // 50k tokens/story is wasteful
			TokensPerStoryCritical: 150_000,
		},
	}
}

// MetricsAnalyzer derives suggestions from metrics.jsonl. The thresholds
// are exposed as fields so plugins can tune them per project. Each
// suggestion lists the actual numbers as evidence so the operator can
// decide whether the heuristic is firing on real data or noise.
type MetricsAnalyzer struct {
	HighFailureRate        float64
	HighEscalationCount    int
	HighAvgLatencyMs       int64
	TokensPerStoryWarning  int
	TokensPerStoryCritical int
}

func (m MetricsAnalyzer) Name() string { return "metrics" }

func (m MetricsAnalyzer) Run(ctx context.Context, info ProjectInfo) ([]Suggestion, error) {
	if info.StateDir == "" {
		return nil, nil
	}

	rec := metrics.NewRecorder(filepath.Join(info.StateDir, "metrics.jsonl"))
	entries, err := rec.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read metrics: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	summary := metrics.Summarize(entries)
	total := summary.SuccessCount + summary.FailureCount
	if total == 0 {
		return nil, nil
	}

	var out []Suggestion

	failureRate := float64(summary.FailureCount) / float64(total) * 100
	if failureRate > m.HighFailureRate {
		out = append(out, Suggestion{
			ID:          "metrics.high_failure_rate",
			Title:       "LLM call failure rate is high",
			Description: fmt.Sprintf("Failure rate %.0f%% across %d calls exceeds threshold %.0f%%.", failureRate, total, m.HighFailureRate),
			Category:    "reliability",
			Severity:    severityForRatio(failureRate, m.HighFailureRate),
			Evidence: []string{
				fmt.Sprintf("%d failures / %d total calls", summary.FailureCount, total),
			},
			Action: "Inspect recent ERROR events; check if a model / API is unhealthy.",
		})
	}

	if summary.EscalationCount >= m.HighEscalationCount {
		out = append(out, Suggestion{
			ID:          "metrics.escalations_frequent",
			Title:       "Stories escalating frequently",
			Description: fmt.Sprintf("%d escalations recorded — agents are getting stuck.", summary.EscalationCount),
			Category:    "reliability",
			Severity:    SeverityWarning,
			Evidence: []string{
				fmt.Sprintf("escalation_count = %d", summary.EscalationCount),
			},
			Action: "Review story complexity / acceptance criteria; tighten QA criteria so simpler stories pass first try.",
		})
	}

	avgMs := summary.TotalDurationMs / int64(total)
	if avgMs > m.HighAvgLatencyMs {
		out = append(out, Suggestion{
			ID:          "metrics.high_latency",
			Title:       "Average LLM call latency is high",
			Description: fmt.Sprintf("Average call latency %dms exceeds %dms target.", avgMs, m.HighAvgLatencyMs),
			Category:    "performance",
			Severity:    SeverityWarning,
			Evidence: []string{
				fmt.Sprintf("avg %dms over %d calls", avgMs, total),
			},
			Action: "Run smaller models for routine roles, or increase Ollama concurrency if GPU has headroom.",
		})
	}

	if summary.TotalStories > 0 {
		tokensPerStory := (summary.TotalTokensIn + summary.TotalTokensOut) / summary.TotalStories
		switch {
		case tokensPerStory >= m.TokensPerStoryCritical:
			out = append(out, Suggestion{
				ID:          "metrics.tokens_per_story_critical",
				Title:       "Token usage per story is very high",
				Description: fmt.Sprintf("%d tokens/story (limit %d). Likely runaway iteration loops.", tokensPerStory, m.TokensPerStoryCritical),
				Category:    "cost",
				Severity:    SeverityCritical,
				Evidence: []string{
					fmt.Sprintf("%d tokens / %d stories = %d", summary.TotalTokensIn+summary.TotalTokensOut, summary.TotalStories, tokensPerStory),
				},
				Action: "Drop runtimes.gemma.max_iterations and tighten qa.success_criteria so agents finish sooner.",
			})
		case tokensPerStory >= m.TokensPerStoryWarning:
			out = append(out, Suggestion{
				ID:          "metrics.tokens_per_story_warning",
				Title:       "Token usage per story is creeping up",
				Description: fmt.Sprintf("%d tokens/story (warning at %d).", tokensPerStory, m.TokensPerStoryWarning),
				Category:    "cost",
				Severity:    SeverityWarning,
				Evidence: []string{
					fmt.Sprintf("%d tokens / %d stories", summary.TotalTokensIn+summary.TotalTokensOut, summary.TotalStories),
				},
				Action: "Check whether the planner is over-elaborating prompts. Trim system prompts where possible.",
			})
		}
	}

	return out, nil
}

// severityForRatio classifies how badly a metric exceeds its threshold.
// 2x = critical, anything above = warning.
func severityForRatio(actual, threshold float64) Severity {
	if threshold > 0 && actual >= threshold*2 {
		return SeverityCritical
	}
	return SeverityWarning
}
