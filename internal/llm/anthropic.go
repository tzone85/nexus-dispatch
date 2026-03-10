package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// AnthropicClient communicates with the Anthropic Messages API.
type AnthropicClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// NewAnthropicClient creates a client configured with the given API key.
func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    anthropicAPIURL,
	}
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

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Complete sends a completion request to the Anthropic Messages API
// and returns the parsed response.
func (c *AnthropicClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, anthropicMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	body := anthropicRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  msgs,
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
		return CompletionResponse{}, fmt.Errorf(
			"anthropic API error (status %d): %s",
			resp.StatusCode, string(respBody),
		)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	content := ""
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return CompletionResponse{
		Content:    content,
		Model:      apiResp.Model,
		StopReason: apiResp.StopReason,
		Usage: Usage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}, nil
}
