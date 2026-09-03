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
	ollamaDefaultBaseURL      = "http://localhost:11434"
	ollamaDefaultTimeout      = 15 * time.Minute
	ollamaDefaultRetryBackoff = 500 * time.Millisecond
	ollamaMaxAttempts         = 3
)

// OllamaClient communicates with an Ollama instance via its
// OpenAI-compatible chat completions endpoint.
type OllamaClient struct {
	model        string
	baseURL      string
	httpClient   *http.Client
	numCtx       int
	retryBackoff time.Duration
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
// Default: 15 minutes (local models can be slow).
func WithOllamaTimeout(d time.Duration) OllamaOption {
	return func(c *OllamaClient) {
		c.httpClient = &http.Client{Timeout: d}
	}
}

// WithOllamaNumCtx sets the default context window (options.num_ctx) sent
// with every request; a per-request CompletionRequest.ContextLength wins.
// 0 (default) leaves the model's own default.
func WithOllamaNumCtx(n int) OllamaOption {
	return func(c *OllamaClient) {
		c.numCtx = n
	}
}

// WithOllamaRetryBackoff sets the base back-off between transient-failure
// retries (attempt n waits base << (n-1)). Default 500 ms; 0 disables waiting.
func WithOllamaRetryBackoff(d time.Duration) OllamaOption {
	return func(c *OllamaClient) {
		c.retryBackoff = d
	}
}

// NewOllamaClient creates a client that talks to a local Ollama instance.
// The model parameter specifies which Ollama model to use by default.
func NewOllamaClient(model string, opts ...OllamaOption) *OllamaClient {
	c := &OllamaClient{
		model:        model,
		baseURL:      ollamaDefaultBaseURL,
		httpClient:   &http.Client{Timeout: ollamaDefaultTimeout},
		retryBackoff: ollamaDefaultRetryBackoff,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// buildRequest assembles the wire request, including Ollama runtime options
// (num_ctx from the request or the client default, temperature when set).
func (c *OllamaClient) buildRequest(model string, req CompletionRequest) oaiChatRequest {
	body := oaiChatRequest{
		Model:      model,
		Messages:   buildOAIMessages(req),
		MaxTokens:  req.MaxTokens,
		Stream:     false,
		Tools:      buildOAITools(req.Tools),
		ToolChoice: oaiToolChoice(req),
	}
	numCtx := req.ContextLength
	if numCtx <= 0 {
		numCtx = c.numCtx
	}
	var temp *float64
	if req.Temperature > 0 {
		t := req.Temperature
		temp = &t
		body.Temperature = &t
	}
	if numCtx > 0 || temp != nil {
		body.Options = &ollamaOptions{NumCtx: numCtx, Temperature: temp}
	}
	return body
}

// Complete sends a non-streaming completion request to the Ollama
// OpenAI-compatible endpoint and returns the parsed response.
func (c *OllamaClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	jsonBody, err := json.Marshal(c.buildRequest(model, req))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	resp, respBody, err := c.doWithRetry(ctx, jsonBody)
	if err != nil {
		return CompletionResponse{}, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return CompletionResponse{}, fmt.Errorf(
			"ollama model %q not found: pull it with 'ollama pull %s': %w",
			model, model, newAPIError("ollama", resp, respBody),
		)
	}
	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, newAPIError("ollama", resp, respBody)
	}

	return parseOAIResponse("ollama", respBody)
}

// doWithRetry posts jsonBody with bounded exponential back-off on transport
// errors and 5xx responses (S3-7). 4xx (including 404 model-not-found) is not
// retried. The back-off honours ctx: cancellation returns promptly instead of
// sleeping through the wait.
func (c *OllamaClient) doWithRetry(ctx context.Context, jsonBody []byte) (*http.Response, []byte, error) {
	endpoint := c.baseURL + "/v1/chat/completions"
	for attempt := 1; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if isConnectionRefused(err) {
				return nil, nil, fmt.Errorf(
					"ollama connection refused at %s: is Ollama running? (start with 'ollama serve'): %w",
					c.baseURL, err,
				)
			}
			if attempt < ollamaMaxAttempts && c.waitBackoff(ctx, attempt) {
				continue
			}
			return nil, nil, fmt.Errorf("ollama http request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode >= 500 && attempt < ollamaMaxAttempts && c.waitBackoff(ctx, attempt) {
			continue
		}
		return resp, respBody, nil
	}
}

// waitBackoff sleeps for the attempt's back-off unless ctx is done first.
// Returns true when the caller should retry, false when ctx was cancelled.
func (c *OllamaClient) waitBackoff(ctx context.Context, attempt int) bool {
	if ctx.Err() != nil {
		return false
	}
	backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
	if backoff <= 0 {
		return true
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isConnectionRefused checks whether the error indicates a TCP connection
// refusal, which typically means Ollama is not running.
func isConnectionRefused(err error) bool {
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "dial tcp")
}
