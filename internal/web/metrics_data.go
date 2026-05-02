// internal/web/metrics_data.go
package web

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

// MetricsSummary is the JSON-friendly metrics overview sent to the dashboard.
type MetricsSummary struct {
	TotalRequirements int                    `json:"total_requirements"`
	TotalStories      int                    `json:"total_stories"`
	TotalTokens       int                    `json:"total_tokens"`
	SuccessRate       float64                `json:"success_rate"`
	AvgLatencyMs      int64                  `json:"avg_latency_ms"`
	EscalationCount   int                    `json:"escalation_count"`
	ByPhase           map[string]PhaseMetric `json:"by_phase"`
}

// PhaseMetric holds per-phase token and call count data.
type PhaseMetric struct {
	Count  int `json:"count"`
	Tokens int `json:"tokens"`
}

// MemPalaceStatus reports whether the MemPalace bridge is reachable.
type MemPalaceStatus struct {
	Available bool   `json:"available"`
	Wing      string `json:"wing"`
}

// ReviewGateItem represents a story or requirement awaiting human review.
type ReviewGateItem struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// InvestigationItem summarises a completed investigation event.
type InvestigationItem struct {
	ReqID       string `json:"req_id"`
	Summary     string `json:"summary"`
	ModuleCount int    `json:"module_count"`
	SmellCount  int    `json:"smell_count"`
	RiskCount   int    `json:"risk_count"`
}

// RecoveryItem summarises a story recovery event.
type RecoveryItem struct {
	StoryID     string `json:"story_id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Timestamp   string `json:"timestamp"`
}

// HumanReviewItem surfaces a HUMAN_REVIEW_NEEDED event so the dashboard
// can show a prominent banner with the diagnosis and any suggested
// directives. Carries enough context that an operator can decide and
// dispatch a corrective directive without leaving the page.
type HumanReviewItem struct {
	StoryID    string   `json:"story_id"`
	Reason     string   `json:"reason"`
	Diagnosis  string   `json:"diagnosis"`
	Directives []string `json:"directives"`
	Timestamp  string   `json:"timestamp"`
}

// AgentTrace summarises the latest progress events for one agent,
// supporting the dashboard's agent drill-down panel.
type AgentTrace struct {
	AgentID string             `json:"agent_id"`
	StoryID string             `json:"story_id"`
	Recent  []AgentProgressRow `json:"recent"`
}

// AgentProgressRow is one row of the drill-down: a STORY_PROGRESS event
// rendered as iteration / phase / detail / tool.
type AgentProgressRow struct {
	Iteration int    `json:"iteration"`
	Phase     string `json:"phase"`
	Detail    string `json:"detail"`
	Tool      string `json:"tool"`
	Timestamp string `json:"timestamp"`
}

// MetricsCache reads metrics.jsonl via a Recorder and caches the summary
// for up to 10 seconds to avoid repeated file I/O on every WebSocket tick.
type MetricsCache struct {
	recorder *metrics.Recorder

	mu      sync.Mutex
	cached  *MetricsSummary
	expires time.Time
}

const metricsCacheTTL = 10 * time.Second

// NewMetricsCache creates a cache backed by metrics.jsonl inside stateDir.
func NewMetricsCache(stateDir string) *MetricsCache {
	return &MetricsCache{
		recorder: metrics.NewRecorder(filepath.Join(stateDir, "metrics.jsonl")),
	}
}

// Get returns the current MetricsSummary, reading from disk at most once per
// TTL window. Returns nil when no metrics data exists.
func (mc *MetricsCache) Get() *MetricsSummary {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.cached != nil && time.Now().Before(mc.expires) {
		return mc.cached
	}

	entries, err := mc.recorder.ReadAll()
	if err != nil || len(entries) == 0 {
		mc.cached = nil
		mc.expires = time.Now().Add(metricsCacheTTL)
		return nil
	}

	summary := metrics.Summarize(entries)
	ms := convertSummary(summary)
	mc.cached = &ms
	mc.expires = time.Now().Add(metricsCacheTTL)
	return mc.cached
}

// convertSummary maps the internal metrics.Summary to the dashboard-friendly
// MetricsSummary type. It creates a new value rather than mutating the input.
func convertSummary(s metrics.Summary) MetricsSummary {
	total := s.SuccessCount + s.FailureCount
	var successRate float64
	if total > 0 {
		successRate = float64(s.SuccessCount) / float64(total) * 100
	}

	var avgLatency int64
	if total > 0 {
		avgLatency = s.TotalDurationMs / int64(total)
	}

	byPhase := make(map[string]PhaseMetric, len(s.ByPhase))
	for phase, ps := range s.ByPhase {
		byPhase[phase] = PhaseMetric{
			Count:  ps.Count,
			Tokens: ps.TokensIn + ps.TokensOut,
		}
	}

	return MetricsSummary{
		TotalRequirements: s.TotalRequirements,
		TotalStories:      s.TotalStories,
		TotalTokens:       s.TotalTokensIn + s.TotalTokensOut,
		SuccessRate:       successRate,
		AvgLatencyMs:      avgLatency,
		EscalationCount:   s.EscalationCount,
		ByPhase:           byPhase,
	}
}

// MemPalaceCheck returns the availability status of the given MemPalace.
// Returns nil when mp is nil.
func MemPalaceCheck(mp *memory.MemPalace) *MemPalaceStatus {
	if mp == nil {
		return nil
	}
	return &MemPalaceStatus{
		Available: mp.IsAvailable(),
		Wing:      "nxd_meta",
	}
}
