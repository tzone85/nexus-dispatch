package metrics

import (
	"context"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// MetricsClient wraps an llm.Client to automatically record usage metrics.
type MetricsClient struct {
	inner    llm.Client
	recorder *Recorder
	reqID    string
	phase    string
	role     string
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
	return &MetricsClient{
		inner:    m.inner,
		recorder: m.recorder,
		reqID:    m.reqID,
		phase:    phase,
		role:     m.role,
	}
}

// WithRole returns a new MetricsClient with the given role, preserving all other fields.
func (m *MetricsClient) WithRole(role string) *MetricsClient {
	return &MetricsClient{
		inner:    m.inner,
		recorder: m.recorder,
		reqID:    m.reqID,
		phase:    m.phase,
		role:     role,
	}
}

// Complete delegates to the inner client and records the metric entry.
func (m *MetricsClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	start := time.Now()
	resp, err := m.inner.Complete(ctx, req)

	_ = m.recorder.Record(MetricEntry{
		Timestamp:  start,
		ReqID:      m.reqID,
		Phase:      m.phase,
		Role:       m.role,
		Model:      req.Model,
		TokensIn:   resp.Usage.InputTokens,
		TokensOut:  resp.Usage.OutputTokens,
		DurationMs: time.Since(start).Milliseconds(),
		Success:    err == nil,
	})

	return resp, err
}
