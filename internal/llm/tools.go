package llm

import (
	"encoding/json"
	"fmt"
)

// ToolDefinition describes a tool the model can call.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// ToolCall represents a single function call from the model.
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult returns the outcome of executing a tool call.
type ToolCallResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ValidateToolCall checks that the arguments satisfy the schema's required fields.
// This is a lightweight check — validates required fields are present and arguments
// are valid JSON, but does not do full JSON Schema validation.
func ValidateToolCall(def ToolDefinition, call ToolCall) error {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}

	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return fmt.Errorf("invalid schema JSON: %w", err)
	}

	for _, field := range schema.Required {
		if _, ok := args[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}

	return nil
}
