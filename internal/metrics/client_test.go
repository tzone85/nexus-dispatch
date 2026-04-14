package metrics

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

type mockLLMClient struct {
	resp llm.CompletionResponse
	err  error
}

func (m *mockLLMClient) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	return m.resp, m.err
}

func TestNewMetricsClient(t *testing.T) {
	recorder := NewRecorder(filepath.Join(t.TempDir(), "metrics.jsonl"))
	inner := &mockLLMClient{resp: llm.CompletionResponse{Content: "ok"}}

	mc := NewMetricsClient(inner, recorder, "req-001", "planning", "tech_lead")
	if mc == nil {
		t.Fatal("NewMetricsClient returned nil")
	}
}

func TestMetricsClient_WithPhase(t *testing.T) {
	recorder := NewRecorder(filepath.Join(t.TempDir(), "metrics.jsonl"))
	inner := &mockLLMClient{}

	mc := NewMetricsClient(inner, recorder, "req-001", "planning", "tech_lead")
	mc2 := mc.WithPhase("review")

	if mc2.phase != "review" {
		t.Errorf("expected phase 'review', got %q", mc2.phase)
	}
	if mc2.role != "tech_lead" {
		t.Error("role should be preserved")
	}
	if mc.phase != "planning" {
		t.Error("original should be unchanged (immutability)")
	}
}

func TestMetricsClient_WithRole(t *testing.T) {
	recorder := NewRecorder(filepath.Join(t.TempDir(), "metrics.jsonl"))
	inner := &mockLLMClient{}

	mc := NewMetricsClient(inner, recorder, "req-001", "planning", "tech_lead")
	mc2 := mc.WithRole("senior")

	if mc2.role != "senior" {
		t.Errorf("expected role 'senior', got %q", mc2.role)
	}
	if mc2.phase != "planning" {
		t.Error("phase should be preserved")
	}
}

func TestMetricsClient_Complete(t *testing.T) {
	recorder := NewRecorder(filepath.Join(t.TempDir(), "metrics.jsonl"))
	inner := &mockLLMClient{
		resp: llm.CompletionResponse{
			Content: "test response",
			Usage:   llm.Usage{InputTokens: 100, OutputTokens: 50},
		},
	}

	mc := NewMetricsClient(inner, recorder, "req-001", "planning", "tech_lead")

	resp, err := mc.Complete(context.Background(), llm.CompletionRequest{
		Model: "gemma4:26b",
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Content != "test response" {
		t.Errorf("expected 'test response', got %q", resp.Content)
	}
}

func TestMetricsClient_Complete_Error(t *testing.T) {
	recorder := NewRecorder(filepath.Join(t.TempDir(), "metrics.jsonl"))
	inner := &mockLLMClient{
		err: fmt.Errorf("internal error"),
	}

	mc := NewMetricsClient(inner, recorder, "req-001", "planning", "tech_lead")

	_, err := mc.Complete(context.Background(), llm.CompletionRequest{
		Model: "gemma4:26b",
	})
	if err == nil {
		t.Error("expected error from inner client")
	}
}
