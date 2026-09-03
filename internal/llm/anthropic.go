package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// AnthropicClient communicates with the Anthropic Messages API.
type AnthropicClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// AnthropicOption configures an AnthropicClient.
type AnthropicOption func(*AnthropicClient)

// WithAnthropicTimeout sets the per-request HTTP timeout (default 120 s).
func WithAnthropicTimeout(d time.Duration) AnthropicOption {
	return func(c *AnthropicClient) {
		c.httpClient = &http.Client{Timeout: d}
	}
}

// NewAnthropicClient creates a client configured with the given API key.
func NewAnthropicClient(apiKey string, opts ...AnthropicOption) *AnthropicClient {
	c := &AnthropicClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: DefaultHTTPTimeout},
		baseURL:    anthropicAPIURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithBaseURL returns a copy of the client with a custom base URL,
// useful for testing with httptest servers.
func (c *AnthropicClient) WithBaseURL(url string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:     c.apiKey,
		httpClient: c.httpClient,
		baseURL:    url,
	}
}

// Timeout reports the configured per-request HTTP timeout.
func (c *AnthropicClient) Timeout() time.Duration { return c.httpClient.Timeout }

type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Temperature *float64             `json:"temperature,omitempty"`
	Tools       []anthropicTool      `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
}

// anthropicMessage carries either a plain string or a list of content blocks.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"` // "auto" | "any" | "tool"
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// anthropicContent is one content block in a request or response. The
// populated fields depend on Type: text → Text; tool_use → ID/Name/Input;
// tool_result → ToolUseID/Content.
type anthropicContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

// buildAnthropicMessages converts neutral messages to the Messages API shape:
// assistant tool calls become tool_use blocks and tool results become
// tool_result blocks inside a USER message (the API has no "tool" role).
// Consecutive tool results are merged into one user turn because the API
// requires roles to alternate.
func buildAnthropicMessages(msgs []Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.Role == RoleTool:
			block := anthropicContent{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if n := len(out); n > 0 && out[n-1].Role == "user" {
				if blocks, ok := out[n-1].Content.([]anthropicContent); ok && len(blocks) > 0 && blocks[0].Type == "tool_result" {
					out[n-1].Content = append(blocks, block)
					continue
				}
			}
			out = append(out, anthropicMessage{Role: "user", Content: []anthropicContent{block}})
		case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
			blocks := make([]anthropicContent, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, anthropicContent{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, anthropicContent{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		case m.Role == RoleSystem:
			// System prompts travel in the top-level "system" field; a stray
			// system message is sent as user text rather than rejected by the API.
			out = append(out, anthropicMessage{Role: "user", Content: m.Content})
		default:
			out = append(out, anthropicMessage{Role: string(m.Role), Content: m.Content})
		}
	}
	return out
}

// buildAnthropicTools maps tool definitions to the Messages API tool shape.
func buildAnthropicTools(defs []ToolDefinition) []anthropicTool {
	if len(defs) == 0 {
		return nil
	}
	tools := make([]anthropicTool, 0, len(defs))
	for _, td := range defs {
		schema := td.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, anthropicTool{Name: td.Name, Description: td.Description, InputSchema: schema})
	}
	return tools
}

// anthropicToolChoiceFor maps the neutral ToolChoice ("required" → any).
func anthropicToolChoiceFor(req CompletionRequest) *anthropicToolChoice {
	if len(req.Tools) == 0 {
		return nil
	}
	switch req.ToolChoice {
	case "required", "any":
		return &anthropicToolChoice{Type: "any"}
	case "auto":
		return &anthropicToolChoice{Type: "auto"}
	}
	return nil
}

// Complete sends a completion request to the Anthropic Messages API
// and returns the parsed response, including any tool_use blocks as ToolCalls.
func (c *AnthropicClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	body := anthropicRequest{
		Model:      req.Model,
		MaxTokens:  req.MaxTokens,
		System:     req.System,
		Messages:   buildAnthropicMessages(req.Messages),
		Tools:      buildAnthropicTools(req.Tools),
		ToolChoice: anthropicToolChoiceFor(req),
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, newAPIError("anthropic", resp, respBody)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	out := CompletionResponse{
		Model:      apiResp.Model,
		StopReason: apiResp.StopReason,
		Usage: Usage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}
	for _, block := range apiResp.Content {
		switch block.Type {
		case "text":
			out.Content += block.Text
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Arguments: block.Input})
		}
	}
	return out, nil
}
