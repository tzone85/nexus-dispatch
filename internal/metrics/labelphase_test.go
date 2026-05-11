package metrics

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// TestLabelPhase_EmptyNoOps covers the early-return guard — passing
// an empty phase string must return the input client unchanged so
// callers can chain LabelPhase(client, c.Phase) without checking
// for empty values themselves.
func TestLabelPhase_EmptyNoOps(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(filepath.Join(dir, "metrics.jsonl"))
	defer rec.Close()
	mc := NewMetricsClient(fakeInnerLP{}, rec, "REQ-1", "execute", "")

	got := LabelPhase(mc, "")
	if got != llm.Client(mc) {
		t.Errorf("empty phase should pass client through unchanged")
	}
}

// TestLabelPhase_AppliesPhaseLabel covers the happy path — a
// non-empty phase wraps the MetricsClient with the new phase value.
func TestLabelPhase_AppliesPhaseLabel(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecorder(filepath.Join(dir, "metrics.jsonl"))
	defer rec.Close()
	mc := NewMetricsClient(fakeInnerLP{}, rec, "REQ-1", "execute", "")

	got := LabelPhase(mc, "review")
	gotMC, ok := got.(*MetricsClient)
	if !ok {
		t.Fatalf("expected *MetricsClient, got %T", got)
	}
	if gotMC.phase != "review" {
		t.Errorf("phase = %q, want review", gotMC.phase)
	}
}

// fakeInnerLP is a no-op llm.Client used just to satisfy the
// NewMetricsClient signature for these tests.
type fakeInnerLP struct{}

func (fakeInnerLP) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	return llm.CompletionResponse{}, nil
}
