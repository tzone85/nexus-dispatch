package metrics

import (
	"context"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// MetricsClient wraps an llm.Client to automatically record usage metrics.
//
// Three "narrow" fields (story, tier, stage) were added in B1.2/B2.2/B2.3:
// they let downstream reports break costs down by story, by escalation
// tier, and by pipeline stage instead of just by phase. Existing call
// sites that don't carry these are still supported via With* helpers.
type MetricsClient struct {
	inner    llm.Client
	recorder *Recorder
	reqID    string
	storyID  string
	phase    string
	stage    string
	role     string
	tier     int
}

// NewMetricsClient creates a MetricsClient that records metrics for each LLM call.
func NewMetricsClient(inner llm.Client, recorder *Recorder, reqID, phase, role string) *MetricsClient {
	return &MetricsClient{
		inner:    inner,
		recorder: recorder,
		reqID:    reqID,
		phase:    phase,
		role:     role,
	}
}

// WithPhase returns a new MetricsClient with the given phase, preserving all other fields.
func (m *MetricsClient) WithPhase(phase string) *MetricsClient {
	cp := *m
	cp.phase = phase
	return &cp
}

// WithRole returns a new MetricsClient with the given role, preserving all other fields.
func (m *MetricsClient) WithRole(role string) *MetricsClient {
	cp := *m
	cp.role = role
	return &cp
}

// WithStory pins a story_id onto subsequent metric records. Used by the
// reviewer / QA / native runtime so per-story cost can be reconstructed.
func (m *MetricsClient) WithStory(storyID string) *MetricsClient {
	cp := *m
	cp.storyID = storyID
	return &cp
}

// WithTier pins an escalation tier (0=junior … 3=tech_lead) so per-tier
// cost can be aggregated by the reporter.
func (m *MetricsClient) WithTier(tier int) *MetricsClient {
	cp := *m
	cp.tier = tier
	return &cp
}

// WithStage pins a pipeline-stage label ("planner", "executor", "reviewer",
// "qa", "merger", ...). Distinct from phase which is more granular.
func (m *MetricsClient) WithStage(stage string) *MetricsClient {
	cp := *m
	cp.stage = stage
	return &cp
}

// LabelStory, LabelTier, LabelStage, LabelRole, LabelPhase return a labelled
// llm.Client when the underlying client is a *MetricsClient and the bare
// input otherwise. They let downstream code (executor, reviewer,
// manager, …) tag metric records with story/tier/stage/role/phase
// without depending on the concrete metrics wrapper.
//
// The helpers walk through any *llm.SemaphoreClient wrapper because the
// native runtime executor wraps the metrics client with a semaphore for
// concurrency control. After labelling the inner *MetricsClient we
// rewrap with the same shared semaphore channel so the global
// concurrency limit is preserved across labelled siblings.
//
// If the pipeline is configured without metrics (e.g. in tests or
// `--dry-run` without metrics wiring) the original client is returned
// untouched.
func LabelStory(client llm.Client, storyID string) llm.Client {
	if storyID == "" {
		return client
	}
	return relabel(client, func(mc *MetricsClient) *MetricsClient { return mc.WithStory(storyID) })
}

func LabelTier(client llm.Client, tier int) llm.Client {
	return relabel(client, func(mc *MetricsClient) *MetricsClient { return mc.WithTier(tier) })
}

func LabelStage(client llm.Client, stage string) llm.Client {
	if stage == "" {
		return client
	}
	return relabel(client, func(mc *MetricsClient) *MetricsClient { return mc.WithStage(stage) })
}

func LabelRole(client llm.Client, role string) llm.Client {
	if role == "" {
		return client
	}
	return relabel(client, func(mc *MetricsClient) *MetricsClient { return mc.WithRole(role) })
}

func LabelPhase(client llm.Client, phase string) llm.Client {
	if phase == "" {
		return client
	}
	return relabel(client, func(mc *MetricsClient) *MetricsClient { return mc.WithPhase(phase) })
}

// relabel walks through a SemaphoreClient wrapper if present, applies fn
// to the underlying *MetricsClient, and rewraps with the shared
// semaphore. Returns the original client when no *MetricsClient is in
// the chain.
func relabel(client llm.Client, fn func(*MetricsClient) *MetricsClient) llm.Client {
	if sem, ok := client.(*llm.SemaphoreClient); ok {
		labelled := relabel(sem.Inner(), fn)
		if labelled == sem.Inner() {
			return client
		}
		return sem.Rewrap(labelled)
	}
	if mc, ok := client.(*MetricsClient); ok {
		return fn(mc)
	}
	return client
}

// Complete delegates to the inner client and records the metric entry.
func (m *MetricsClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	start := time.Now()
	resp, err := m.inner.Complete(ctx, req)

	_ = m.recorder.Record(MetricEntry{
		Timestamp:  start,
		ReqID:      m.reqID,
		StoryID:    m.storyID,
		Phase:      m.phase,
		Stage:      m.stage,
		Role:       m.role,
		Tier:       m.tier,
		Model:      req.Model,
		TokensIn:   resp.Usage.InputTokens,
		TokensOut:  resp.Usage.OutputTokens,
		DurationMs: time.Since(start).Milliseconds(),
		Success:    err == nil,
	})

	return resp, err
}
