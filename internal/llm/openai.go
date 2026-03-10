package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const openaiAPIURL = "https://api.openai.com/v1/chat/completions"

// OpenAIClient communicates with the OpenAI Chat Completions API.
type OpenAIClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// NewOpenAIClient creates a client configured with the given API key.
func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    openaiAPIURL,
	}
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

type openaiRequest struct {
	Model     string          `json:"model"`
	Messages  []openaiMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Model   string         `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openaiChoice struct {
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// Complete sends a completion request to the OpenAI Chat Completions API
// and returns the parsed response. The system prompt is prepended as a
// system-role message per OpenAI conventions.
func (c *OpenAIClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	msgs := make([]openaiMessage, 0, len(req.Messages)+1)

	// OpenAI uses a system message in the messages array
	if req.System != "" {
		msgs = append(msgs, openaiMessage{
			Role:    string(RoleSystem),
			Content: req.System,
		})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, openaiMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	body := openaiRequest{
		Model:     req.Model,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
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
		return CompletionResponse{}, fmt.Errorf(
			"openai API error (status %d): %s",
			resp.StatusCode, string(respBody),
		)
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("openai returned no choices")
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
