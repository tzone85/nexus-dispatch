package llm_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

type mockClient struct {
	completeFunc func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error)
	callCount    atomic.Int32
}

func (m *mockClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	m.callCount.Add(1)
	return m.completeFunc(ctx, req)
}

func TestFallbackClient_UsesPrimaryOnSuccess(t *testing.T) {
	primary := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-primary"}, nil
		},
	}
	fallback := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-fallback"}, nil
		},
	}

	client := llm.NewFallbackClient(primary, fallback, 60*time.Second)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from-primary" {
		t.Errorf("Content = %q, want %q", resp.Content, "from-primary")
	}
	if fallback.callCount.Load() != 0 {
		t.Error("fallback should not be called when primary succeeds")
	}
}

func TestFallbackClient_FallsBackOnQuotaError(t *testing.T) {
	primary := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{}, &llm.QuotaError{StatusCode: 429, Message: "rate limited"}
		},
	}
	fallback := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-fallback"}, nil
		},
	}

	client := llm.NewFallbackClient(primary, fallback, 60*time.Second)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from-fallback" {
		t.Errorf("Content = %q, want %q", resp.Content, "from-fallback")
	}
}

func TestFallbackClient_SkipsPrimaryAfterQuota(t *testing.T) {
	primaryCalls := atomic.Int32{}
	primary := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			primaryCalls.Add(1)
			return llm.CompletionResponse{}, &llm.QuotaError{StatusCode: 429, Message: "rate limited"}
		},
	}
	fallback := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-fallback"}, nil
		},
	}

	client := llm.NewFallbackClient(primary, fallback, 1*time.Hour)

	client.Complete(context.Background(), llm.CompletionRequest{})
	client.Complete(context.Background(), llm.CompletionRequest{})

	if primaryCalls.Load() != 1 {
		t.Errorf("primary called %d times, want 1", primaryCalls.Load())
	}
}

func TestFallbackClient_ResetsCooldown(t *testing.T) {
	primaryCalls := atomic.Int32{}
	primary := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			count := primaryCalls.Add(1)
			if count == 1 {
				return llm.CompletionResponse{}, &llm.QuotaError{StatusCode: 429, Message: "rate limited"}
			}
			return llm.CompletionResponse{Content: "primary-recovered"}, nil
		},
	}
	fallback := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-fallback"}, nil
		},
	}

	client := llm.NewFallbackClient(primary, fallback, 50*time.Millisecond)

	client.Complete(context.Background(), llm.CompletionRequest{})
	time.Sleep(100 * time.Millisecond)

	resp, _ := client.Complete(context.Background(), llm.CompletionRequest{})
	if resp.Content != "primary-recovered" {
		t.Errorf("Content = %q, want %q", resp.Content, "primary-recovered")
	}
}

func TestFallbackClient_PropagatesNonQuotaErrors(t *testing.T) {
	primary := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{}, fmt.Errorf("network error")
		},
	}
	fallback := &mockClient{
		completeFunc: func(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
			return llm.CompletionResponse{Content: "from-fallback"}, nil
		},
	}

	client := llm.NewFallbackClient(primary, fallback, 60*time.Second)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from-fallback" {
		t.Errorf("Content = %q, want %q", resp.Content, "from-fallback")
	}
}
