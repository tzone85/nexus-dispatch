package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ollamaDefaultBaseURL = "http://localhost:11434"
	ollamaDefaultTimeout = 5 * time.Minute
)

// OllamaClient communicates with an Ollama instance via its
// OpenAI-compatible chat completions endpoint.
type OllamaClient struct {
	model      string
	baseURL    string
	httpClient *http.Client
}

// OllamaOption configures an OllamaClient.
type OllamaOption func(*OllamaClient)

// WithOllamaBaseURL sets the Ollama server base URL.
// Default: http://localhost:11434
func WithOllamaBaseURL(url string) OllamaOption {
	return func(c *OllamaClient) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithOllamaTimeout sets the HTTP client timeout for Ollama requests.
// Default: 5 minutes (local models can be slow).
func WithOllamaTimeout(d time.Duration) OllamaOption {
	return func(c *OllamaClient) {
		c.httpClient = &http.Client{Timeout: d}
	}
}

// NewOllamaClient creates a client that talks to a local Ollama instance.
// The model parameter specifies which Ollama model to use by default.
func NewOllamaClient(model string, opts ...OllamaOption) *OllamaClient {
	c := &OllamaClient{
		model:      model,
		baseURL:    ollamaDefaultBaseURL,
		httpClient: &http.Client{Timeout: ollamaDefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type ollamaRequest struct {
	Model     string          `json:"model"`
	Messages  []ollamaMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
	Stream    bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaResponse struct {
	Choices []ollamaChoice `json:"choices"`
	Model   string         `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type ollamaChoice struct {
	Message      ollamaMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// Complete sends a non-streaming completion request to the Ollama
// OpenAI-compatible endpoint and returns the parsed response.
func (c *OllamaClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)

	if req.System != "" {
		msgs = append(msgs, ollamaMessage{
			Role:    string(RoleSystem),
			Content: req.System,
		})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, ollamaMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	body := ollamaRequest{
		Model:     model,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
		Stream:    false,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.baseURL + "/v1/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if isConnectionRefused(err) {
			return CompletionResponse{}, fmt.Errorf(
				"ollama connection refused at %s: is Ollama running? (start with 'ollama serve'): %w",
				c.baseURL, err,
			)
		}
		return CompletionResponse{}, fmt.Errorf("ollama http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return CompletionResponse{}, fmt.Errorf(
			"ollama model %q not found: pull it with 'ollama pull %s'",
			model, model,
		)
	}

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf(
			"ollama API error (status %d): %s",
			resp.StatusCode, string(respBody),
		)
	}

	var apiResp ollamaResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("ollama returned no choices")
	}

	choice := apiResp.Choices[0]

	return CompletionResponse{
		Content:    choice.Message.Content,
		Model:      apiResp.Model,
		StopReason: choice.FinishReason,
		Usage: Usage{
			InputTokens:  apiResp.Usage.PromptTokens,
			OutputTokens: apiResp.Usage.CompletionTokens,
		},
	}, nil
}

// isConnectionRefused checks whether the error indicates a TCP connection
// refusal, which typically means Ollama is not running.
func isConnectionRefused(err error) bool {
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "dial tcp")
}
