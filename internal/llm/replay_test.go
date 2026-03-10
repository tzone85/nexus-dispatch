package llm_test

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestReplayClient_ReturnsResponses(t *testing.T) {
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "response 1", Model: "test"},
		llm.CompletionResponse{Content: "response 2", Model: "test"},
	)

	resp1, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if resp1.Content != "response 1" {
		t.Fatalf("expected 'response 1', got %s", resp1.Content)
	}

	resp2, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if resp2.Content != "response 2" {
		t.Fatalf("expected 'response 2', got %s", resp2.Content)
	}
}

func TestReplayClient_ExhaustedReturnsError(t *testing.T) {
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "only one"},
	)

	_, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}

	_, err = client.Complete(context.Background(), llm.CompletionRequest{})
	if err == nil {
		t.Fatal("expected error when exhausted")
	}
}

func TestReplayClient_RecordsCalls(t *testing.T) {
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: "ok"},
	)

	req := llm.CompletionRequest{
		Model:  "claude-opus",
		System: "You are a tech lead",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Plan this feature"},
		},
	}
	_, err := client.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", client.CallCount())
	}
	recorded := client.CallAt(0)
	if recorded.Model != "claude-opus" {
		t.Fatalf("expected model claude-opus, got %s", recorded.Model)
	}
	if recorded.System != "You are a tech lead" {
		t.Fatalf("expected system prompt, got %s", recorded.System)
	}
}

func TestReplayClient_EmptyReturnsError(t *testing.T) {
	client := llm.NewReplayClient()
	_, err := client.Complete(context.Background(), llm.CompletionRequest{})
	if err == nil {
		t.Fatal("expected error with no responses")
	}
}

func TestReplayClient_ImplementsClientInterface(t *testing.T) {
	var _ llm.Client = llm.NewReplayClient()
}
