package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestGoogleClient_Complete_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API key is in URL query param
		key := r.URL.Query().Get("key")
		if key != "test-key" {
			t.Errorf("expected query param key=test-key, got %q", key)
		}

		// Verify Content-Type header
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type 'application/json', got %q", got)
		}

		// Verify request body structure
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		// Verify system instruction is sent
		sysInstr, ok := reqBody["systemInstruction"].(map[string]any)
		if !ok {
			t.Fatal("expected systemInstruction in request")
		}
		parts, ok := sysInstr["parts"].([]any)
		if !ok || len(parts) == 0 {
			t.Fatal("expected parts in systemInstruction")
		}
		firstPart := parts[0].(map[string]any)
		if firstPart["text"] != "You are a helpful assistant" {
			t.Errorf("expected system text 'You are a helpful assistant', got %v", firstPart["text"])
		}

		// Verify contents
		contents, ok := reqBody["contents"].([]any)
		if !ok || len(contents) == 0 {
			t.Fatal("expected contents in request")
		}
		firstContent := contents[0].(map[string]any)
		if firstContent["role"] != "user" {
			t.Errorf("expected role 'user', got %v", firstContent["role"])
		}

		// Verify URL path contains model name
		if !strings.Contains(r.URL.Path, "gemma-4-26b-a4b-it") {
			t.Errorf("expected model in URL path, got %q", r.URL.Path)
		}

		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{"text": "Hello! How can I help you?"},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     25,
				"candidatesTokenCount": 10,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewGoogleClient("test-key", llm.WithGoogleBaseURL(server.URL))

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:     "gemma-4-26b-a4b-it",
		MaxTokens: 1024,
		System:    "You are a helpful assistant",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.Model != "gemma-4-26b-a4b-it" {
		t.Errorf("Model = %q, want %q", resp.Model, "gemma-4-26b-a4b-it")
	}
	if resp.StopReason != "STOP" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "STOP")
	}
	if resp.Usage.InputTokens != 25 {
		t.Errorf("InputTokens = %d, want 25", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 10 {
		t.Errorf("OutputTokens = %d, want 10", resp.Usage.OutputTokens)
	}
}

func TestGoogleClient_Complete_ToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var reqBody map[string]any
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}

		// Verify tools are sent in request body
		tools, ok := reqBody["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Fatal("expected tools in request body")
		}

		toolObj := tools[0].(map[string]any)
		decls, ok := toolObj["functionDeclarations"].([]any)
		if !ok || len(decls) == 0 {
			t.Fatal("expected functionDeclarations in tools")
		}

		decl := decls[0].(map[string]any)
		if decl["name"] != "create_story" {
			t.Errorf("expected function name 'create_story', got %v", decl["name"])
		}
		if decl["description"] != "Create a user story" {
			t.Errorf("expected function description 'Create a user story', got %v", decl["description"])
		}

		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"role": "model",
						"parts": []map[string]any{
							{
								"functionCall": map[string]any{
									"name": "create_story",
									"args": map[string]any{
										"title":      "Auth Module",
										"complexity": 3,
									},
								},
							},
						},
					},
					"finishReason": "STOP",
				},
			},
			"usageMetadata": map[string]any{
				"promptTokenCount":     50,
				"candidatesTokenCount": 30,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewGoogleClient("test-key", llm.WithGoogleBaseURL(server.URL))

	toolDef := llm.ToolDefinition{
		Name:        "create_story",
		Description: "Create a user story",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"complexity":{"type":"integer"}},"required":["title"]}`),
	}

	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "gemma-4-26b-a4b-it",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Decompose this requirement into stories"},
		},
		Tools: []llm.ToolDefinition{toolDef},
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

	var args map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("unmarshal tool call arguments: %v", err)
	}
	if args["title"] != "Auth Module" {
		t.Errorf("expected title 'Auth Module', got %v", args["title"])
	}
	complexity, ok := args["complexity"].(float64)
	if !ok || complexity != 3 {
		t.Errorf("expected complexity 3, got %v", args["complexity"])
	}
	if resp.Usage.InputTokens != 50 {
		t.Errorf("InputTokens = %d, want 50", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 30 {
		t.Errorf("OutputTokens = %d, want 30", resp.Usage.OutputTokens)
	}
}

func TestGoogleClient_Complete_QuotaExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded for quota metric","status":"RESOURCE_EXHAUSTED"}}`))
	}))
	defer server.Close()

	client := llm.NewGoogleClient("test-key", llm.WithGoogleBaseURL(server.URL))

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model: "gemma-4-26b-a4b-it",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error for 429 status")
	}
	if !llm.IsQuotaError(err) {
		t.Errorf("expected IsQuotaError to return true, got false; error: %v", err)
	}

	var quotaErr *llm.QuotaError
	if ok := err.(*llm.QuotaError); ok == nil {
		t.Fatal("expected error to be *QuotaError")
	} else {
		quotaErr = ok
	}
	if quotaErr.StatusCode != 429 {
		t.Errorf("QuotaError.StatusCode = %d, want 429", quotaErr.StatusCode)
	}
	if !strings.Contains(quotaErr.Error(), "quota exhausted") {
		t.Errorf("expected 'quota exhausted' in error message, got %q", quotaErr.Error())
	}
}

func TestGoogleClient_Complete_ForbiddenQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":403,"message":"API key not valid","status":"PERMISSION_DENIED"}}`))
	}))
	defer server.Close()

	client := llm.NewGoogleClient("bad-key", llm.WithGoogleBaseURL(server.URL))

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma-4-26b-a4b-it",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 403 status")
	}
	if !llm.IsQuotaError(err) {
		t.Errorf("expected IsQuotaError to return true for 403, got false; error: %v", err)
	}
}

func TestGoogleClient_Complete_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":500,"message":"Internal error"}}`))
	}))
	defer server.Close()

	client := llm.NewGoogleClient("test-key", llm.WithGoogleBaseURL(server.URL))

	_, err := client.Complete(context.Background(), llm.CompletionRequest{
		Model:    "gemma-4-26b-a4b-it",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if llm.IsQuotaError(err) {
		t.Error("expected IsQuotaError to return false for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got %q", err.Error())
	}
}

func TestGoogleClient_ImplementsClientInterface(t *testing.T) {
	var _ llm.Client = llm.NewGoogleClient("test-key")
}
