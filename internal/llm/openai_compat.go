package llm

import (
	"encoding/json"
	"fmt"
)

// The OpenAI Chat Completions wire format, shared by the OpenAI client and the
// Ollama client (Ollama exposes an OpenAI-compatible /v1/chat/completions).

type oaiChatRequest struct {
	Model       string           `json:"model"`
	Messages    []oaiChatMessage `json:"messages"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	Stream      bool             `json:"stream"`
	Tools       []oaiTool        `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
	// Options carries Ollama-specific runtime options (num_ctx, temperature);
	// the OpenAI API ignores unknown fields, Ollama honours them.
	Options *ollamaOptions `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumCtx      int      `json:"num_ctx,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type oaiChatMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiChatResponse struct {
	Choices []oaiChoice `json:"choices"`
	Model   string      `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type oaiChoice struct {
	Message      oaiChatMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// buildOAIMessages converts the provider-neutral messages (plus the optional
// system prompt) into the OpenAI shape, carrying tool_calls on assistant turns
// and tool_call_id on tool-result turns.
func buildOAIMessages(req CompletionRequest) []oaiChatMessage {
	msgs := make([]oaiChatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, oaiChatMessage{Role: string(RoleSystem), Content: req.System})
	}
	for _, m := range req.Messages {
		msg := oaiChatMessage{Role: string(m.Role), Content: m.Content}
		if m.Role == RoleTool && m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]oaiToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				msg.ToolCalls[i] = oaiToolCall{ID: tc.ID, Type: "function"}
				msg.ToolCalls[i].Function.Name = tc.Name
				msg.ToolCalls[i].Function.Arguments = string(tc.Arguments)
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// buildOAITools maps tool definitions to the OpenAI "function" tool shape.
func buildOAITools(defs []ToolDefinition) []oaiTool {
	if len(defs) == 0 {
		return nil
	}
	tools := make([]oaiTool, 0, len(defs))
	for _, td := range defs {
		tools = append(tools, oaiTool{Type: "function", Function: oaiFunction(td)})
	}
	return tools
}

// oaiToolChoice maps the neutral ToolChoice to the OpenAI vocabulary. Only
// emitted when tools are present; an empty choice lets the API default (auto).
func oaiToolChoice(req CompletionRequest) string {
	if len(req.Tools) == 0 {
		return ""
	}
	switch req.ToolChoice {
	case "required", "any":
		return "required"
	case "none":
		return "none"
	case "auto":
		return "auto"
	}
	return ""
}

// parseOAIResponse decodes an OpenAI-shaped response body. provider labels
// the error messages.
func parseOAIResponse(provider string, body []byte) (CompletionResponse, error) {
	var apiResp oaiChatResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("%s returned no choices", provider)
	}
	choice := apiResp.Choices[0]

	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return CompletionResponse{
		Content:    choice.Message.Content,
		Model:      apiResp.Model,
		StopReason: choice.FinishReason,
		ToolCalls:  toolCalls,
		Usage: Usage{
			InputTokens:  apiResp.Usage.PromptTokens,
			OutputTokens: apiResp.Usage.CompletionTokens,
		},
	}, nil
}
