package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

// --- Test 9: FunctionCallingActivates ---
// Prove: HasToolSupport returns true for gemma4 models via Ollama.

func TestWiring_FunctionCallingActivates(t *testing.T) {
	// gemma4:26b via ollama should support tools
	if !llm.HasToolSupport("ollama", "gemma4:26b") {
		t.Error("expected tool support for gemma4:26b via ollama")
	}
	// gemma4:31b should too
	if !llm.HasToolSupport("ollama", "gemma4:31b") {
		t.Error("expected tool support for gemma4:31b")
	}
	// google+ollama composite should too
	if !llm.HasToolSupport("google+ollama", "gemma4:26b") {
		t.Error("expected tool support for google+ollama")
	}
}

// --- Test 10: FallbackOnQuotaError ---
// Prove: FallbackClient switches to fallback when primary returns QuotaError.

func TestWiring_FallbackOnQuotaError(t *testing.T) {
	quotaErr := &llm.QuotaError{StatusCode: 429, Message: "rate limited"}
	primary := llm.NewErrorClient(quotaErr)
	fallback := llm.NewReplayClient(llm.CompletionResponse{
		Content: "fallback-response",
		Model:   "gemma4:26b",
	})

	client := llm.NewFallbackClient(primary, fallback, 1*time.Hour)

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma4:26b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("expected no error from fallback, got: %v", err)
	}
	if resp.Content != "fallback-response" {
		t.Errorf("expected fallback response, got %q", resp.Content)
	}
	if fallback.CallCount() != 1 {
		t.Errorf("expected fallback called once, got %d", fallback.CallCount())
	}
}

// --- Test 11: GoogleProviderConstructs ---
// Prove: google+ollama config creates a working FallbackClient.
// The primary (Google) is called first; fallback (Ollama) is only used on error.

func TestWiring_GoogleProviderConstructs(t *testing.T) {
	primaryCalled := false

	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalled = true
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role":  "model",
						"parts": []map[string]any{{"text": "from-google"}},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     10,
				"candidatesTokenCount": 5,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer googleServer.Close()

	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"model":      "gemma4:26b",
			"message":    map[string]any{"role": "assistant", "content": "from-ollama"},
			"done":       true,
			"eval_count": 5,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollamaServer.Close()

	googleClient := llm.NewGoogleClient("test-key", llm.WithGoogleBaseURL(googleServer.URL))
	ollamaClient := llm.NewOllamaClient("gemma4:26b", llm.WithOllamaBaseURL(ollamaServer.URL))

	fallbackClient := llm.NewFallbackClient(googleClient, ollamaClient, 60*time.Second)

	resp, err := fallbackClient.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma-4-26b-a4b-it",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !primaryCalled {
		t.Error("expected primary (Google) to be called first")
	}
	if resp.Content != "from-google" {
		t.Errorf("expected response from primary, got %q", resp.Content)
	}
}

// --- Test 12: ToolCompatDegrades ---
// Prove: non-Gemma model returns false from HasToolSupport, and InjectToolSchema
// adds tool definitions to the prompt text as a fallback.

func TestWiring_ToolCompatDegrades(t *testing.T) {
	// deepseek doesn't support native tools
	if llm.HasToolSupport("ollama", "deepseek-coder-v2:latest") {
		t.Error("deepseek should NOT have native tool support")
	}

	// InjectToolSchema should add tool definitions to prompt
	tools := []llm.ToolDefinition{{
		Name:        "create_story",
		Description: "test",
		Parameters:  json.RawMessage(`{}`),
	}}
	result := llm.InjectToolSchema("base prompt", tools)
	if !strings.Contains(result, "create_story") {
		t.Error("expected tool schema injected into prompt")
	}
	if !strings.Contains(result, "base prompt") {
		t.Error("expected original prompt preserved")
	}
}

// --- Test 13: UpdateCheckStaleness ---
// Prove: IsStale correctly identifies stale vs fresh cache.

func TestWiring_UpdateCheckStaleness(t *testing.T) {
	fresh := update.CheckResult{CheckedAt: time.Now()}
	if update.IsStale(fresh, 48) {
		t.Error("just-checked cache should not be stale")
	}

	stale := update.CheckResult{CheckedAt: time.Now().Add(-49 * time.Hour)}
	if !update.IsStale(stale, 48) {
		t.Error("49-hour-old cache should be stale")
	}

	empty := update.CheckResult{}
	if !update.IsStale(empty, 48) {
		t.Error("never-checked cache should be stale")
	}
}
