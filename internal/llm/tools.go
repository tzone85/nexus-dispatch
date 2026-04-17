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

// ValidateToolCall checks that the arguments satisfy the schema's required fields
// and that argument types match the declared schema types. Returns an error for
// missing required fields, type mismatches, or malformed JSON.
func ValidateToolCall(def ToolDefinition, call ToolCall) error {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}

	var schema struct {
		Required   []string                       `json:"required"`
		Properties map[string]toolPropertySchema   `json:"properties"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return fmt.Errorf("invalid schema JSON: %w", err)
	}

	for _, field := range schema.Required {
		if _, ok := args[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}

	// Validate types for fields that declare a type in the schema.
	for name, prop := range schema.Properties {
		raw, ok := args[name]
		if !ok {
			continue // optional fields that are absent are fine
		}
		if err := validateJSONType(name, raw, prop.Type); err != nil {
			return err
		}
	}

	return nil
}

// toolPropertySchema captures the type declaration from a JSON Schema property.
type toolPropertySchema struct {
	Type string `json:"type"`
}

// validateJSONType checks that a raw JSON value matches the declared schema type.
func validateJSONType(name string, raw json.RawMessage, schemaType string) error {
	if schemaType == "" {
		return nil // no type constraint declared
	}

	trimmed := string(raw)
	if len(trimmed) == 0 {
		return fmt.Errorf("field %q is empty", name)
	}

	switch schemaType {
	case "string":
		if trimmed[0] != '"' {
			return fmt.Errorf("field %q: expected string, got %s", name, trimmed)
		}
	case "number", "integer":
		if trimmed[0] == '"' || trimmed[0] == '{' || trimmed[0] == '[' || trimmed == "true" || trimmed == "false" || trimmed == "null" {
			return fmt.Errorf("field %q: expected %s, got %s", name, schemaType, trimmed)
		}
	case "boolean":
		if trimmed != "true" && trimmed != "false" {
			return fmt.Errorf("field %q: expected boolean, got %s", name, trimmed)
		}
	case "object":
		if trimmed[0] != '{' {
			return fmt.Errorf("field %q: expected object, got %s", name, trimmed)
		}
	case "array":
		if trimmed[0] != '[' {
			return fmt.Errorf("field %q: expected array, got %s", name, trimmed)
		}
	}

	return nil
}
