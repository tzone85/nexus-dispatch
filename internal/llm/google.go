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

const googleDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GoogleClient communicates with the Google AI Studio (Generative Language) API.
type GoogleClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// GoogleOption configures a GoogleClient.
type GoogleOption func(*GoogleClient)

// WithGoogleBaseURL sets a custom base URL, useful for testing with httptest servers.
func WithGoogleBaseURL(url string) GoogleOption {
	return func(c *GoogleClient) { c.baseURL = url }
}

// NewGoogleClient creates a client configured with the given API key.
func NewGoogleClient(apiKey string, opts ...GoogleOption) *GoogleClient {
	c := &GoogleClient{
		apiKey:     apiKey,
		baseURL:    googleDefaultBaseURL,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- Google AI request types ---

type googleRequest struct {
	Contents          []googleContent         `json:"contents"`
	SystemInstruction *googleContent          `json:"systemInstruction,omitempty"`
	Tools             []googleTool            `json:"tools,omitempty"`
	GenerationConfig  *googleGenerationConfig `json:"generationConfig,omitempty"`
}

type googleContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *googleFunctionCall `json:"functionCall,omitempty"`
	FunctionResp *googleFunctionResp `json:"functionResponse,omitempty"`
}

type googleFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type googleFunctionResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type googleTool struct {
	FunctionDeclarations []googleFuncDecl `json:"functionDeclarations"`
}

type googleFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type googleGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

// --- Google AI response types ---

type googleResponse struct {
	Candidates    []googleCandidate `json:"candidates"`
	UsageMetadata *googleUsage      `json:"usageMetadata"`
}

type googleCandidate struct {
	Content      googleContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type googleUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

// Complete sends a completion request to the Google AI Studio generateContent API
// and returns the parsed response.
func (c *GoogleClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = "gemma-4-26b-a4b-it"
	}

	gReq := buildGoogleRequest(req)

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.baseURL, model, c.apiKey)

	body, err := json.Marshal(gReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("google AI request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode == http.StatusTooManyRequests || httpResp.StatusCode == http.StatusForbidden {
		return CompletionResponse{}, &QuotaError{
			StatusCode: httpResp.StatusCode,
			Message:    string(respBody),
		}
	}

	if httpResp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("google AI error (HTTP %d): %s", httpResp.StatusCode, string(respBody))
	}

	var gResp googleResponse
	if err := json.Unmarshal(respBody, &gResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}

	return parseGoogleResponse(gResp, model)
}

// buildGoogleRequest converts a CompletionRequest into the Google AI request format.
func buildGoogleRequest(req CompletionRequest) googleRequest {
	gReq := googleRequest{}

	if req.System != "" {
		gReq.SystemInstruction = &googleContent{
			Parts: []googlePart{{Text: req.System}},
		}
	}

	for _, msg := range req.Messages {
		gReq.Contents = append(gReq.Contents, convertMessage(msg))
	}

	if len(req.Tools) > 0 {
		decls := make([]googleFuncDecl, 0, len(req.Tools))
		for _, td := range req.Tools {
			decls = append(decls, googleFuncDecl{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			})
		}
		gReq.Tools = []googleTool{{FunctionDeclarations: decls}}
	}

	if req.MaxTokens > 0 || req.Temperature > 0 {
		gReq.GenerationConfig = &googleGenerationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
		}
	}

	return gReq
}

// convertMessage maps an llm.Message to a googleContent for the API request.
func convertMessage(msg Message) googleContent {
	switch msg.Role {
	case RoleTool:
		respData := parseFunctionResponseData(msg.Content)
		return googleContent{
			Role: "function",
			Parts: []googlePart{{
				FunctionResp: &googleFunctionResp{
					Name:     msg.ToolCallID,
					Response: respData,
				},
			}},
		}
	case RoleAssistant:
		return googleContent{
			Role:  "model",
			Parts: []googlePart{{Text: msg.Content}},
		}
	default: // RoleUser, RoleSystem
		return googleContent{
			Role:  "user",
			Parts: []googlePart{{Text: msg.Content}},
		}
	}
}

// parseFunctionResponseData attempts to parse content as JSON; falls back to wrapping
// the raw string in a {"result": ...} envelope.
func parseFunctionResponseData(content string) map[string]any {
	var respData map[string]any
	if err := json.Unmarshal([]byte(content), &respData); err != nil || respData == nil {
		respData = map[string]any{"result": content}
	}
	return respData
}

// parseGoogleResponse extracts text and tool calls from the API response.
func parseGoogleResponse(gResp googleResponse, model string) (CompletionResponse, error) {
	if len(gResp.Candidates) == 0 {
		return CompletionResponse{}, fmt.Errorf("no candidates in response")
	}

	candidate := gResp.Candidates[0]
	resp := CompletionResponse{
		Model:      model,
		StopReason: candidate.FinishReason,
	}

	if gResp.UsageMetadata != nil {
		resp.Usage = Usage{
			InputTokens:  gResp.UsageMetadata.PromptTokenCount,
			OutputTokens: gResp.UsageMetadata.CandidatesTokenCount,
		}
	}

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			resp.Content += part.Text
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				Name:      part.FunctionCall.Name,
				Arguments: args,
			})
		}
	}

	return resp, nil
}
