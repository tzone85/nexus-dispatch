package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestOllamaClient_CompleteWithSystemPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got %q", got)
		}

		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if reqBody["model"] != "qwen2.5-coder:14b" {
			t.Errorf("expected model 'qwen2.5-coder:14b', got %v", reqBody["model"])
		}

		if reqBody["stream"] != false {
			t.Errorf("expected stream false, got %v", reqBody["stream"])
		}

		messages, ok := reqBody["messages"].([]any)
		if !ok || len(messages) < 2 {
			t.Fatalf("expected at least 2 messages, got %v", reqBody["messages"])
		}

		firstMsg, ok := messages[0].(map[string]any)
		if !ok {
			t.Fatalf("expected first message to be a map, got %T", messages[0])
		}
		if firstMsg["role"] != "system" {
			t.Errorf("expected first message role 'system', got %v", firstMsg["role"])
		}
		if firstMsg["content"] != "You are a code reviewer" {
			t.Errorf("expected system content 'You are a code reviewer', got %v", firstMsg["content"])
		}

		secondMsg := messages[1].(map[string]any)
		if secondMsg["role"] != "user" {
			t.Errorf("expected second message role 'user', got %v", secondMsg["role"])
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Review complete",
					},
					"finish_reason": "stop",
				},
			},
			"model": "qwen2.5-coder:14b",
			"usage": map[string]any{
				"prompt_tokens":     150,
				"completion_tokens": 60,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("qwen2.5-coder:14b", llm.WithOllamaBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:     "qwen2.5-coder:14b",
		MaxTokens: 4000,
		System:    "You are a code reviewer",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Review this code"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Content != "Review complete" {
		t.Errorf("expected 'Review complete', got %q", resp.Content)
	}
	if resp.Model != "qwen2.5-coder:14b" {
		t.Errorf("expected model 'qwen2.5-coder:14b', got %q", resp.Model)
	}
	if resp.StopReason != "stop" {
		t.Errorf("expected stop_reason 'stop', got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 150 {
		t.Errorf("expected 150 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 60 {
		t.Errorf("expected 60 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

func TestOllamaClient_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "model not found"}`))
	}))
	defer server.Close()

	client := llm.NewOllamaClient("nonexistent-model", llm.WithOllamaBaseURL(server.URL))

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "nonexistent-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error for model not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "ollama pull") {
		t.Errorf("expected 'ollama pull' hint in error, got %q", err.Error())
	}
}

func TestOllamaClient_ConnectionRefused(t *testing.T) {
	// Use a port that nothing is listening on
	client := llm.NewOllamaClient("test-model",
		llm.WithOllamaBaseURL("http://127.0.0.1:19999"),
		llm.WithOllamaTimeout(1*time.Second),
	)

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
	if !strings.Contains(err.Error(), "connection refused") && !strings.Contains(err.Error(), "dial tcp") {
		t.Errorf("expected connection error, got %q", err.Error())
	}
}

func TestOllamaClient_CustomBaseURL(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				},
			},
			"model": "test-model",
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("test-model", llm.WithOllamaBaseURL(server.URL))

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if receivedPath != "/v1/chat/completions" {
		t.Errorf("expected path '/v1/chat/completions', got %q", receivedPath)
	}
}

func TestOllamaClient_TimeoutOption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay longer than the client timeout
		time.Sleep(2 * time.Second)
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "slow"},
					"finish_reason": "stop",
				},
			},
			"model": "test",
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("test-model",
		llm.WithOllamaBaseURL(server.URL),
		llm.WithOllamaTimeout(200*time.Millisecond),
	)

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestOllamaClient_ResponseParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Generated code here",
					},
					"finish_reason": "length",
				},
			},
			"model": "deepseek-coder-v2:latest",
			"usage": map[string]any{
				"prompt_tokens":     500,
				"completion_tokens": 1024,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("deepseek-coder-v2:latest", llm.WithOllamaBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:     "deepseek-coder-v2:latest",
		MaxTokens: 1024,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Write a function"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Content != "Generated code here" {
		t.Errorf("expected 'Generated code here', got %q", resp.Content)
	}
	if resp.Model != "deepseek-coder-v2:latest" {
		t.Errorf("expected model 'deepseek-coder-v2:latest', got %q", resp.Model)
	}
	if resp.StopReason != "length" {
		t.Errorf("expected stop_reason 'length', got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 500 {
		t.Errorf("expected 500 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 1024 {
		t.Errorf("expected 1024 output tokens, got %d", resp.Usage.OutputTokens)
	}
}

func TestOllamaClient_DefaultModelFallback(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		receivedModel, _ = reqBody["model"].(string)

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "ok"},
					"finish_reason": "stop",
				},
			},
			"model": receivedModel,
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("default-model", llm.WithOllamaBaseURL(server.URL))

	// Send request with empty Model field — should use the client's default
	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if receivedModel != "default-model" {
		t.Errorf("expected default model 'default-model', got %q", receivedModel)
	}
}

func TestOllamaClient_ImplementsClientInterface(t *testing.T) {
	var _ llm.Client = llm.NewOllamaClient("test")
}

func TestOllamaClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {}
	}))
	defer server.Close()

	client := llm.NewOllamaClient("test", llm.WithOllamaBaseURL(server.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Complete(ctx, llm.CompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestOllamaClient_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{},
			"model":   "test",
			"usage":   map[string]any{"prompt_tokens": 0, "completion_tokens": 0},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("test", llm.WithOllamaBaseURL(server.URL))
	_, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("expected 'no choices' in error, got %q", err.Error())
	}
}

func TestOllamaClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	client := llm.NewOllamaClient("test", llm.WithOllamaBaseURL(server.URL))
	_, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "test"})
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status 500 in error, got %q", err.Error())
	}
}

func TestOllamaClient_CompleteWithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}

		// Verify tools are passed through
		tools, ok := req["tools"]
		if !ok {
			t.Error("expected 'tools' in request body")
		}

		toolSlice, ok := tools.([]any)
		if !ok || len(toolSlice) != 1 {
			t.Fatalf("expected 1 tool, got %v", tools)
		}

		// Verify tool structure
		tool := toolSlice[0].(map[string]any)
		if tool["type"] != "function" {
			t.Errorf("expected tool type 'function', got %v", tool["type"])
		}
		fn := tool["function"].(map[string]any)
		if fn["name"] != "create_story" {
			t.Errorf("expected function name 'create_story', got %v", fn["name"])
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "create_story",
									"arguments": `{"title":"Auth","complexity":3}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 50,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("gemma4:26b", llm.WithOllamaBaseURL(server.URL))

	toolDef := llm.ToolDefinition{
		Name:        "create_story",
		Description: "Create a story",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"complexity":{"type":"integer"}},"required":["title"]}`),
	}

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma4:26b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Decompose this requirement"}},
		Tools:    []llm.ToolDefinition{toolDef},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "create_story" {
		t.Errorf("tool call name = %q, want %q", resp.ToolCalls[0].Name, "create_story")
	}
	if resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("tool call ID = %q, want %q", resp.ToolCalls[0].ID, "call_1")
	}

	// Verify arguments are valid JSON
	var args map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("unmarshal tool call arguments: %v", err)
	}
	if args["title"] != "Auth" {
		t.Errorf("expected title 'Auth', got %v", args["title"])
	}
	if resp.StopReason != "tool_calls" {
		t.Errorf("expected stop_reason 'tool_calls', got %q", resp.StopReason)
	}
}

func TestOllamaClient_CompleteNoTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}

		// Verify tools are NOT in request when none provided
		if _, ok := req["tools"]; ok {
			t.Error("expected no 'tools' in request body when none provided")
		}

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Here is the plan...",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     50,
				"completion_tokens": 30,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("gemma4:26b", llm.WithOllamaBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma4:26b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Here is the plan..." {
		t.Errorf("Content = %q, want %q", resp.Content, "Here is the plan...")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestOllamaClient_CompleteToolResultMessage(t *testing.T) {
	var receivedMessages []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}
		receivedMessages, _ = req["messages"].([]any)

		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Story created successfully.",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     80,
				"completion_tokens": 20,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("gemma4:26b", llm.WithOllamaBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "gemma4:26b",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Create a story"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "create_story", Arguments: json.RawMessage(`{"title":"Auth"}`)},
				},
			},
			{
				Role:       llm.RoleTool,
				Content:    `{"id":"story-1","title":"Auth"}`,
				ToolCallID: "call_1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Story created successfully." {
		t.Errorf("Content = %q, want %q", resp.Content, "Story created successfully.")
	}

	// Verify tool message has tool_call_id
	if len(receivedMessages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(receivedMessages))
	}
	toolMsg, ok := receivedMessages[2].(map[string]any)
	if !ok {
		t.Fatalf("expected tool message to be a map, got %T", receivedMessages[2])
	}
	if toolMsg["role"] != "tool" {
		t.Errorf("expected role 'tool', got %v", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("expected tool_call_id 'call_1', got %v", toolMsg["tool_call_id"])
	}

	// Verify assistant message has tool_calls
	assistantMsg, ok := receivedMessages[1].(map[string]any)
	if !ok {
		t.Fatalf("expected assistant message to be a map, got %T", receivedMessages[1])
	}
	toolCalls, ok := assistantMsg["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		t.Fatalf("expected assistant message to have tool_calls, got %v", assistantMsg["tool_calls"])
	}
}

func TestOllamaClient_CompleteMultipleToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "create_story",
									"arguments": `{"title":"Auth"}`,
								},
							},
							{
								"id":   "call_2",
								"type": "function",
								"function": map[string]any{
									"name":      "create_story",
									"arguments": `{"title":"Dashboard"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 80,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOllamaClient("gemma4:26b", llm.WithOllamaBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma4:26b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Decompose into stories"}},
		Tools: []llm.ToolDefinition{
			{Name: "create_story", Description: "Create a story", Parameters: json.RawMessage(`{}`)},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("first tool call ID = %q, want %q", resp.ToolCalls[0].ID, "call_1")
	}
	if resp.ToolCalls[1].ID != "call_2" {
		t.Errorf("second tool call ID = %q, want %q", resp.ToolCalls[1].ID, "call_2")
	}
}
