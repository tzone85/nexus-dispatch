package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// captureServer records the decoded JSON request body and replies with respJSON.
func captureServer(t *testing.T, respJSON string) (*httptest.Server, *map[string]any, *string) {
	t.Helper()
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respJSON))
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &path
}

var toolLoopRequest = llm.CompletionRequest{
	Model:       "m",
	MaxTokens:   50,
	Temperature: 0.2,
	System:      "sys",
	Tools: []llm.ToolDefinition{{
		Name:        "read_file",
		Description: "Read a file",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}},
	ToolChoice: "required",
	Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "open it"},
		{Role: llm.RoleAssistant, Content: "sure", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "package a"},
	},
}

const oaiToolCallResponse = `{"model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"",
  "tool_calls":[{"id":"call_2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"b.go\"}"}}]}}],
  "usage":{"prompt_tokens":10,"completion_tokens":4}}`

func TestOpenAIClient_ToolCallingRoundTrip(t *testing.T) {
	srv, body, _ := captureServer(t, oaiToolCallResponse)
	client := llm.NewOpenAIClient("k").WithBaseURL(srv.URL)

	resp, err := client.Complete(context.Background(), toolLoopRequest)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	tools := (*body)["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] != "read_file" || fn["parameters"] == nil {
		t.Errorf("tools not sent in OpenAI shape: %v", tools)
	}
	if (*body)["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", (*body)["tool_choice"])
	}
	if (*body)["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want 0.2", (*body)["temperature"])
	}
	msgs := (*body)["messages"].([]any)
	if len(msgs) != 4 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages = %v", msgs)
	}
	assistant := msgs[2].(map[string]any)
	tc := assistant["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "call_1" || tc["function"].(map[string]any)["arguments"] != `{"path":"a.go"}` {
		t.Errorf("assistant tool_calls not carried: %v", assistant)
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" || toolMsg["content"] != "package a" {
		t.Errorf("tool result not carried: %v", toolMsg)
	}

	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_2" || resp.ToolCalls[0].Name != "read_file" ||
		string(resp.ToolCalls[0].Arguments) != `{"path":"b.go"}` {
		t.Errorf("response tool calls not parsed: %+v", resp.ToolCalls)
	}
	if resp.StopReason != "tool_calls" || resp.Usage.InputTokens != 10 {
		t.Errorf("response metadata: %+v", resp)
	}
}

func TestOpenAIClient_NoToolsOmitsToolFields(t *testing.T) {
	srv, body, _ := captureServer(t, `{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}]}`)
	client := llm.NewOpenAIClient("k").WithBaseURL(srv.URL)
	if _, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tools", "tool_choice", "temperature", "options"} {
		if _, ok := (*body)[k]; ok {
			t.Errorf("%s must be omitted when unset", k)
		}
	}
}

func TestOpenAIClient_TimeoutDefaultsAndOption(t *testing.T) {
	if got := llm.NewOpenAIClient("k").Timeout(); got != llm.DefaultHTTPTimeout || llm.DefaultHTTPTimeout != 120*time.Second {
		t.Errorf("default timeout = %v, want 120s", got)
	}
	if got := llm.NewOpenAIClient("k", llm.WithOpenAITimeout(3*time.Second)).WithBaseURL("http://x").Timeout(); got != 3*time.Second {
		t.Errorf("option timeout = %v, want 3s", got)
	}
}

// hangingServer never answers until the test finishes.
func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return srv
}

func TestOpenAIClient_TimeoutFires(t *testing.T) {
	srv := hangingServer(t)
	client := llm.NewOpenAIClient("k", llm.WithOpenAITimeout(50*time.Millisecond)).WithBaseURL(srv.URL)
	start := time.Now()
	_, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not bound the request")
	}
}

const anthropicToolUseResponse = `{"model":"claude","stop_reason":"tool_use","content":[
  {"type":"text","text":"Reading."},
  {"type":"tool_use","id":"toolu_2","name":"read_file","input":{"path":"b.go"}}],
  "usage":{"input_tokens":11,"output_tokens":5}}`

func TestAnthropicClient_ToolCallingRoundTrip(t *testing.T) {
	srv, body, _ := captureServer(t, anthropicToolUseResponse)
	client := llm.NewAnthropicClient("k").WithBaseURL(srv.URL)

	resp, err := client.Complete(context.Background(), toolLoopRequest)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	tools := (*body)["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "read_file" || tool["input_schema"] == nil {
		t.Errorf("tools not in Messages API shape: %v", tool)
	}
	if tc := (*body)["tool_choice"].(map[string]any); tc["type"] != "any" {
		t.Errorf("tool_choice = %v, want {type: any}", tc)
	}
	if (*body)["system"] != "sys" || (*body)["temperature"] != 0.2 {
		t.Errorf("system/temperature: %v %v", (*body)["system"], (*body)["temperature"])
	}

	msgs := (*body)["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (no tool role), got %v", msgs)
	}
	assistant := msgs[1].(map[string]any)
	blocks := assistant["content"].([]any)
	if assistant["role"] != "assistant" || len(blocks) != 2 {
		t.Fatalf("assistant turn: %v", assistant)
	}
	if blocks[0].(map[string]any)["type"] != "text" || blocks[0].(map[string]any)["text"] != "sure" {
		t.Errorf("assistant text block: %v", blocks[0])
	}
	use := blocks[1].(map[string]any)
	if use["type"] != "tool_use" || use["id"] != "call_1" || use["name"] != "read_file" || use["input"].(map[string]any)["path"] != "a.go" {
		t.Errorf("tool_use block: %v", use)
	}
	result := msgs[2].(map[string]any)
	rb := result["content"].([]any)[0].(map[string]any)
	if result["role"] != "user" || rb["type"] != "tool_result" || rb["tool_use_id"] != "call_1" || rb["content"] != "package a" {
		t.Errorf("tool_result must be a user turn: %v", result)
	}

	if resp.Content != "Reading." || len(resp.ToolCalls) != 1 {
		t.Fatalf("response: %+v", resp)
	}
	if resp.ToolCalls[0].ID != "toolu_2" || resp.ToolCalls[0].Name != "read_file" || string(resp.ToolCalls[0].Arguments) != `{"path":"b.go"}` {
		t.Errorf("tool_use not parsed: %+v", resp.ToolCalls[0])
	}
	if resp.StopReason != "tool_use" || resp.Usage.OutputTokens != 5 {
		t.Errorf("metadata: %+v", resp)
	}
}

func TestAnthropicClient_MessageShapes(t *testing.T) {
	srv, body, _ := captureServer(t, `{"model":"c","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`)
	client := llm.NewAnthropicClient("k").WithBaseURL(srv.URL)
	req := llm.CompletionRequest{
		Model: "c",
		Tools: []llm.ToolDefinition{{Name: "t"}}, // empty schema → placeholder object schema
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "stray system"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "t"}, {ID: "b", Name: "t", Arguments: json.RawMessage(`{"x":1}`)}}},
			{Role: llm.RoleTool, ToolCallID: "a", Content: "ra"},
			{Role: llm.RoleTool, ToolCallID: "b", Content: "rb"},
			{Role: llm.RoleUser, Content: "next"},
		},
	}
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	msgs := (*body)["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("expected stray-system, assistant, merged tool results, user = 4 turns; got %d: %v", len(msgs), msgs)
	}
	if msgs[0].(map[string]any)["role"] != "user" || msgs[0].(map[string]any)["content"] != "stray system" {
		t.Errorf("stray system message must become user text: %v", msgs[0])
	}
	blocks := msgs[1].(map[string]any)["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["input"].(map[string]any) == nil {
		t.Errorf("assistant with empty content must carry only tool_use blocks with object input: %v", blocks)
	}
	merged := msgs[2].(map[string]any)["content"].([]any)
	if len(merged) != 2 || merged[1].(map[string]any)["tool_use_id"] != "b" {
		t.Errorf("consecutive tool results must merge into one user turn: %v", merged)
	}
	if _, ok := (*body)["tool_choice"]; ok {
		t.Error("tool_choice must be omitted when unset")
	}
	schema := (*body)["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("empty schema must default to an object schema: %v", schema)
	}
}

func TestAnthropicClient_ToolChoiceAuto(t *testing.T) {
	srv, body, _ := captureServer(t, `{"model":"c","stop_reason":"end_turn","content":[]}`)
	client := llm.NewAnthropicClient("k").WithBaseURL(srv.URL)
	req := llm.CompletionRequest{Model: "c", ToolChoice: "auto", Tools: []llm.ToolDefinition{{Name: "t", Parameters: json.RawMessage(`{}`)}},
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if tc := (*body)["tool_choice"].(map[string]any); tc["type"] != "auto" {
		t.Errorf("tool_choice = %v", tc)
	}
}

func TestAnthropicClient_TimeoutDefaultsAndOption(t *testing.T) {
	if got := llm.NewAnthropicClient("k").Timeout(); got != 120*time.Second {
		t.Errorf("default timeout = %v, want 120s", got)
	}
	if got := llm.NewAnthropicClient("k", llm.WithAnthropicTimeout(time.Second)).WithBaseURL("http://x").Timeout(); got != time.Second {
		t.Errorf("option timeout = %v", got)
	}
	srv := hangingServer(t)
	client := llm.NewAnthropicClient("k", llm.WithAnthropicTimeout(50*time.Millisecond)).WithBaseURL(srv.URL)
	start := time.Now()
	if _, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "c", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not bound the request")
	}
}

func TestOllamaClient_SendsNumCtxAndTemperature(t *testing.T) {
	okResp := `{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`

	t.Run("client default num_ctx", func(t *testing.T) {
		srv, body, _ := captureServer(t, okResp)
		client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL), llm.WithOllamaNumCtx(8192))
		req := llm.CompletionRequest{Temperature: 0.7, Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}
		if _, err := client.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		opts := (*body)["options"].(map[string]any)
		if opts["num_ctx"] != float64(8192) || opts["temperature"] != 0.7 {
			t.Errorf("options = %v", opts)
		}
		if (*body)["temperature"] != 0.7 {
			t.Errorf("top-level temperature = %v", (*body)["temperature"])
		}
		if (*body)["model"] != "m" {
			t.Errorf("model fallback = %v", (*body)["model"])
		}
	})

	t.Run("request ContextLength overrides client default", func(t *testing.T) {
		srv, body, _ := captureServer(t, okResp)
		client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL), llm.WithOllamaNumCtx(8192))
		req := llm.CompletionRequest{ContextLength: 32768, Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}
		if _, err := client.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		opts := (*body)["options"].(map[string]any)
		if opts["num_ctx"] != float64(32768) {
			t.Errorf("num_ctx = %v, want 32768", opts["num_ctx"])
		}
		if _, ok := opts["temperature"]; ok {
			t.Error("temperature must be omitted when unset")
		}
	})

	t.Run("nothing set omits options", func(t *testing.T) {
		srv, body, _ := captureServer(t, okResp)
		client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL))
		if _, err := client.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := (*body)["options"]; ok {
			t.Error("options must be omitted when nothing is configured")
		}
	})
}

func TestOllamaClient_ToolChoiceForwarded(t *testing.T) {
	srv, body, _ := captureServer(t, oaiToolCallResponse)
	client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL))
	if _, err := client.Complete(context.Background(), toolLoopRequest); err != nil {
		t.Fatal(err)
	}
	if (*body)["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v", (*body)["tool_choice"])
	}
}

func TestOllamaClient_BackoffHonoursContext(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"server busy"}`))
	}))
	defer srv.Close()

	// Base back-off of one minute: without ctx awareness the retry loop would
	// sleep through the cancellation.
	client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL), llm.WithOllamaRetryBackoff(time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := client.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation must return promptly, took %v", elapsed)
	}
	if hits != 1 {
		t.Errorf("expected exactly one attempt before cancellation, got %d", hits)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 {
		t.Errorf("last response must surface as APIError, got %v", err)
	}
}

func TestOllamaClient_RetriesTransient5xxThenSucceeds(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"third time"}}]}`))
	}))
	defer srv.Close()
	client := llm.NewOllamaClient("m", llm.WithOllamaBaseURL(srv.URL), llm.WithOllamaRetryBackoff(0))
	resp, err := client.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err != nil || resp.Content != "third time" || hits != 3 {
		t.Fatalf("resp=%+v err=%v hits=%d", resp, err, hits)
	}
}

func TestGoogleClient_UsesConfiguredModel(t *testing.T) {
	okResp := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	srv, _, path := captureServer(t, okResp)

	client := llm.NewGoogleClient("k", llm.WithGoogleBaseURL(srv.URL), llm.WithGoogleModel("gemma-4-26b-a4b-it"))
	resp, err := client.Complete(context.Background(), llm.CompletionRequest{Model: "gemma4:26b", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*path, "/models/gemma-4-26b-a4b-it:generateContent") || strings.Contains(*path, "gemma4:26b") {
		t.Errorf("configured google_model must be sent, not the Ollama tag: %s", *path)
	}
	if resp.Model != "gemma-4-26b-a4b-it" {
		t.Errorf("response model = %q", resp.Model)
	}

	// Without a configured model the request model is used; without either, the default.
	srv2, _, path2 := captureServer(t, okResp)
	c2 := llm.NewGoogleClient("k", llm.WithGoogleBaseURL(srv2.URL))
	if _, err := c2.Complete(context.Background(), llm.CompletionRequest{Model: "req-model", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*path2, "/models/req-model:") {
		t.Errorf("request model must be used when none configured: %s", *path2)
	}
	if _, err := c2.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*path2, "/models/gemma-4-26b-a4b-it:") {
		t.Errorf("default model expected: %s", *path2)
	}
}

func TestGoogleClient_AssistantFunctionCallsReplayed(t *testing.T) {
	okResp := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	srv, body, _ := captureServer(t, okResp)
	client := llm.NewGoogleClient("k", llm.WithGoogleBaseURL(srv.URL))
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
			{Role: llm.RoleAssistant, Content: "calling", ToolCalls: []llm.ToolCall{
				{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
				{Name: "broken", Arguments: json.RawMessage(`not json`)},
			}},
			{Role: llm.RoleTool, ToolCallID: "read_file", Content: `{"content":"package a"}`},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{Name: "only_call"}}},
		},
	}
	if _, err := client.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	contents := (*body)["contents"].([]any)
	model := contents[1].(map[string]any)
	parts := model["parts"].([]any)
	if model["role"] != "model" || len(parts) != 3 {
		t.Fatalf("model turn must carry text + 2 functionCall parts: %v", model)
	}
	if parts[0].(map[string]any)["text"] != "calling" {
		t.Errorf("text part: %v", parts[0])
	}
	fc := parts[1].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "read_file" || fc["args"].(map[string]any)["path"] != "a.go" {
		t.Errorf("functionCall part: %v", fc)
	}
	broken := parts[2].(map[string]any)["functionCall"].(map[string]any)
	if len(broken["args"].(map[string]any)) != 0 {
		t.Errorf("malformed args must become an empty object: %v", broken)
	}
	fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if fr["name"] != "read_file" {
		t.Errorf("functionResponse: %v", fr)
	}
	onlyCall := contents[3].(map[string]any)["parts"].([]any)
	if len(onlyCall) != 1 || onlyCall[0].(map[string]any)["functionCall"] == nil {
		t.Errorf("assistant turn with only tool calls must not add an empty text part: %v", onlyCall)
	}
}

func TestGoogleClient_TimeoutOption(t *testing.T) {
	srv := hangingServer(t)
	client := llm.NewGoogleClient("k", llm.WithGoogleBaseURL(srv.URL), llm.WithGoogleTimeout(50*time.Millisecond))
	start := time.Now()
	if _, err := client.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not bound the request")
	}
}

func TestOpenAIClient_ToolChoiceVocabulary(t *testing.T) {
	okResp := `{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}]}`
	tools := []llm.ToolDefinition{{Name: "t", Parameters: json.RawMessage(`{}`)}}
	for in, want := range map[string]string{"required": "required", "any": "required", "auto": "auto", "none": "none", "weird": ""} {
		srv, body, _ := captureServer(t, okResp)
		client := llm.NewOpenAIClient("k").WithBaseURL(srv.URL)
		req := llm.CompletionRequest{Model: "m", Tools: tools, ToolChoice: in, Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}
		if _, err := client.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		got, _ := (*body)["tool_choice"].(string)
		if got != want {
			t.Errorf("ToolChoice %q → %q, want %q", in, got, want)
		}
	}
}
