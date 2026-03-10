package llm

import "context"

// Role represents the role of a message sender in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a single message in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest contains parameters for an LLM completion call.
type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature,omitempty"`
	System      string    `json:"system,omitempty"` // System prompt (Anthropic-style)
}

// CompletionResponse holds the result of a completion call.
type CompletionResponse struct {
	Content    string `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      Usage  `json:"usage"`
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
