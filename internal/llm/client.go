package llm

import "context"

// Role represents the role of a message sender in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// CompletionRequest contains parameters for an LLM completion call.
type CompletionRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	MaxTokens   int              `json:"max_tokens"`
	Temperature float64          `json:"temperature,omitempty"`
	System      string           `json:"system,omitempty"` // System prompt (Anthropic-style)
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  string           `json:"tool_choice,omitempty"`
}

// CompletionResponse holds the result of a completion call.
type CompletionResponse struct {
	Content    string     `json:"content"`
	Model      string     `json:"model"`
	StopReason string     `json:"stop_reason"`
	Usage      Usage      `json:"usage"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// Usage tracks token consumption for a completion call.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Client defines the interface for LLM API interactions.
type Client interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// MaxResponseContentLen is the maximum number of characters allowed in an LLM
// response content field. Responses exceeding this limit are truncated to
// prevent context window exhaustion from unexpectedly large outputs.
const MaxResponseContentLen = 200_000

// TruncateContent truncates s to maxLen characters if it exceeds the limit,
// appending a truncation notice. Returns s unchanged if within limits.
func TruncateContent(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated: response exceeded limit]"
}
