package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestAnthropicClient_Complete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("expected x-api-key 'test-key', got %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("expected anthropic-version '2023-06-01', got %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got %q", got)
		}

		// Verify request body
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if reqBody["model"] != "claude-opus-4" {
			t.Errorf("expected model 'claude-opus-4', got %v", reqBody["model"])
		}
		if reqBody["system"] != "You are a tech lead" {
			t.Errorf("expected system prompt, got %v", reqBody["system"])
		}

		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Here is the plan"},
			},
			"model":       "claude-opus-4",
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewAnthropicClient("test-key").WithBaseURL(server.URL)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:     "claude-opus-4",
		MaxTokens: 8000,
		System:    "You are a tech lead",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Plan this feature"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Content != "Here is the plan" {
		t.Fatalf("expected 'Here is the plan', got %q", resp.Content)
	}
	if resp.Model != "claude-opus-4" {
		t.Fatalf("expected model 'claude-opus-4', got %q", resp.Model)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("expected stop_reason 'end_turn', got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 100 {
		t.Fatalf("expected 100 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Fatalf("expected 50 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

func TestAnthropicClient_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": "rate limited"}`))
	}))
	defer server.Close()

	client := llm.NewAnthropicClient("test-key").WithBaseURL(server.URL)
	_, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for 429 status")
	}
}

func TestAnthropicClient_ImplementsClientInterface(t *testing.T) {
	var _ llm.Client = llm.NewAnthropicClient("key")
}

func TestAnthropicClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Intentionally slow -- but context should cancel before response
		select {}
	}))
	defer server.Close()

	client := llm.NewAnthropicClient("test-key").WithBaseURL(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.Complete(ctx, llm.CompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}
