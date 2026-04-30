package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// inlineToolCall is the shape models commonly emit when they don't use the
// structured tool_calls API: a JSON object with `name` and `arguments`.
type inlineToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// extractInlineToolCalls scans text content for tool-call JSON objects and
// returns them as structured llm.ToolCall values.
//
// Live-test discovery (LB8): qwen2.5-coder and similar local models often
// emit one or more `{"name": "...", "arguments": {...}}` objects in the
// response content field rather than populating the OpenAI/Anthropic
// `tool_calls` field. NXD's runtime expected the structured form and
// terminated early with "model finished without tool calls".
//
// The parser uses depth-aware brace matching so nested arguments don't
// break extraction. Each balanced JSON object that successfully decodes
// into the inlineToolCall shape (with non-empty Name) becomes a ToolCall.
//
// Returns an empty slice when no tool calls are found.
func extractInlineToolCalls(content string) []llm.ToolCall {
	if !strings.Contains(content, "\"name\"") {
		return nil
	}
	// Strip code fences first — models often wrap the JSON.
	content = stripFences(content)

	var calls []llm.ToolCall
	i := 0
	for i < len(content) {
		// Find next opening brace.
		next := strings.IndexByte(content[i:], '{')
		if next == -1 {
			break
		}
		start := i + next
		end := matchBalancedBrace(content, start)
		if end == -1 {
			break
		}
		candidate := content[start : end+1]
		var t inlineToolCall
		if err := json.Unmarshal([]byte(candidate), &t); err == nil && t.Name != "" {
			args := t.Arguments
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			calls = append(calls, llm.ToolCall{
				ID:        fmt.Sprintf("inline-%d", len(calls)+1),
				Name:      t.Name,
				Arguments: args,
			})
		}
		i = end + 1
	}
	return calls
}

// stripFences removes ```...``` code fences when they wrap the entire
// content or the tool-call JSON section.
func stripFences(s string) string {
	for {
		idx := strings.Index(s, "```")
		if idx == -1 {
			return s
		}
		rest := s[idx+3:]
		// Skip optional language tag.
		if nl := strings.IndexByte(rest, '\n'); nl != -1 {
			rest = rest[nl+1:]
		}
		end := strings.Index(rest, "```")
		if end == -1 {
			// Unterminated fence; return what we have before the open fence
			// plus the inner content unmodified.
			return s[:idx] + rest
		}
		s = s[:idx] + rest[:end] + rest[end+3:]
	}
}

// matchBalancedBrace returns the index of the closing `}` matching the `{`
// at start. Skips braces inside JSON string literals. Returns -1 when no
// match is found.
func matchBalancedBrace(s string, start int) int {
	if start >= len(s) || s[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
