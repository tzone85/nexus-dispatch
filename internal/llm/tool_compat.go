package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

var toolSupportedModels = []string{
	"gemma4",
	"gemma-4",
}

var toolSupportedProviders = map[string]bool{
	"anthropic": true,
	"openai":    true,
	"google":    true,
}

// HasToolSupport reports whether the given provider+model combination
// supports native tool calling. Models that don't support it will need
// tool schemas injected into the system prompt instead.
func HasToolSupport(provider, model string) bool {
	baseProvider := provider
	if strings.Contains(provider, "+") {
		parts := strings.SplitN(provider, "+", 2)
		baseProvider = parts[0]
	}

	if toolSupportedProviders[baseProvider] {
		return true
	}

	lower := strings.ToLower(model)
	for _, prefix := range toolSupportedModels {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// InjectToolSchema appends tool definitions to the system prompt so that
// models without native tool calling can still use structured tool output.
// Returns the original prompt unchanged when tools is empty.
func InjectToolSchema(systemPrompt string, tools []ToolDefinition) string {
	if len(tools) == 0 {
		return systemPrompt
	}

	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\n## Available Tools\n\n")
	b.WriteString("You MUST respond with a JSON object containing a \"tool_calls\" array.\n")
	b.WriteString("Each element must have \"name\" (string) and \"arguments\" (object).\n\n")

	for _, tool := range tools {
		b.WriteString(fmt.Sprintf("### %s\n", tool.Name))
		b.WriteString(fmt.Sprintf("%s\n", tool.Description))
		b.WriteString(fmt.Sprintf("Parameters: %s\n\n", string(tool.Parameters)))
	}

	b.WriteString("Respond ONLY with the JSON object. No prose before or after.\n")
	return b.String()
}

type textToolResponse struct {
	ToolCalls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"tool_calls"`
}

// ParseToolCallsFromText extracts tool calls from a plain-text LLM response.
// It handles both raw JSON and JSON wrapped in markdown code fences.
func ParseToolCallsFromText(text string) ([]ToolCall, error) {
	cleaned := strings.TrimSpace(text)

	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			cleaned = strings.Join(lines, "\n")
		}
	}

	var resp textToolResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("parse tool calls from text: %w", err)
	}

	calls := make([]ToolCall, len(resp.ToolCalls))
	for i, tc := range resp.ToolCalls {
		calls[i] = ToolCall{
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return calls, nil
}
