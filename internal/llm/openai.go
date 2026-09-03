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

const openaiAPIURL = "https://api.openai.com/v1/chat/completions"

// DefaultHTTPTimeout bounds a single cloud-provider request (Anthropic,
// OpenAI) when no explicit timeout is configured. Without it a stalled
// connection hangs the pipeline stage forever.
const DefaultHTTPTimeout = 120 * time.Second

// OpenAIClient communicates with the OpenAI Chat Completions API.
type OpenAIClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// OpenAIOption configures an OpenAIClient.
type OpenAIOption func(*OpenAIClient)

// WithOpenAITimeout sets the per-request HTTP timeout (default 120 s).
func WithOpenAITimeout(d time.Duration) OpenAIOption {
	return func(c *OpenAIClient) {
		c.httpClient = &http.Client{Timeout: d}
	}
}

// NewOpenAIClient creates a client configured with the given API key.
func NewOpenAIClient(apiKey string, opts ...OpenAIOption) *OpenAIClient {
	c := &OpenAIClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: DefaultHTTPTimeout},
		baseURL:    openaiAPIURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithBaseURL returns a copy of the client with a custom base URL,
// useful for testing with httptest servers.
func (c *OpenAIClient) WithBaseURL(url string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     c.apiKey,
		httpClient: c.httpClient,
		baseURL:    url,
	}
}

// Timeout reports the configured per-request HTTP timeout.
func (c *OpenAIClient) Timeout() time.Duration { return c.httpClient.Timeout }

// Complete sends a completion request to the OpenAI Chat Completions API
// and returns the parsed response. The system prompt is prepended as a
// system-role message per OpenAI conventions; tools, assistant tool_calls and
// tool-result messages use the native OpenAI shape.
func (c *OpenAIClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	body := oaiChatRequest{
		Model:      req.Model,
		Messages:   buildOAIMessages(req),
		MaxTokens:  req.MaxTokens,
		Tools:      buildOAITools(req.Tools),
		ToolChoice: oaiToolChoice(req),
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
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

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
		return CompletionResponse{}, newAPIError("openai", resp, respBody)
	}

	return parseOAIResponse("openai", respBody)
}
