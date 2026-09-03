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

// knownToolNames returns the set of tool names in defs.
func knownToolNames(defs []llm.ToolDefinition) map[string]bool {
	known := make(map[string]bool, len(defs))
	for _, d := range defs {
		known[d.Name] = true
	}
	return known
}

// extractInlineToolCalls recovers tool calls that a model emitted as JSON in
// its text content instead of the structured tool_calls field (LB8: qwen2.5-
// coder and similar local models do this routinely).
//
// It is deliberately strict, because model text often ECHOES JSON it just
// read from the repository (a README example, a config file, a test fixture)
// and executing that would turn file contents into tool invocations:
//
//   - The caller only invokes this when the response carried NO structured
//     tool calls.
//   - The entire trimmed message must be tool-call JSON: a single object, a
//     JSON array of objects, or one or more objects separated only by
//     whitespace — optionally wrapped in exactly one ``` fence that spans the
//     whole message. Any prose before or after ⇒ nothing is extracted.
//   - Every object must decode to {name, arguments} with a name in known
//     (the registered tool definitions). One unknown or malformed object
//     rejects the whole message.
//
// Returns nil when the message does not qualify.
func extractInlineToolCalls(content string, known map[string]bool) []llm.ToolCall {
	body, ok := unwrapSingleFence(strings.TrimSpace(content))
	if !ok || body == "" || !strings.Contains(body, "\"name\"") {
		return nil
	}
	objects, ok := splitJSONObjects(body)
	if !ok {
		return nil
	}
	calls := make([]llm.ToolCall, 0, len(objects))
	for _, candidate := range objects {
		var t inlineToolCall
		if err := json.Unmarshal([]byte(candidate), &t); err != nil || t.Name == "" || !known[t.Name] {
			return nil
		}
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
	return calls
}

// unwrapSingleFence returns the inner text when s is exactly one fenced code
// block (```[lang]\n ... ```), s itself when it has no fence, and ok=false
// when fences appear anywhere else (prose + fence, multiple fences).
func unwrapSingleFence(s string) (string, bool) {
	if !strings.Contains(s, "```") {
		return s, true
	}
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") || len(s) < 6 {
		return "", false
	}
	inner := s[3 : len(s)-3]
	// Drop an optional language tag on the opening line.
	if nl := strings.IndexByte(inner, '\n'); nl != -1 {
		inner = inner[nl+1:]
	} else {
		return "", false // ``` json ``` on one line is not a block
	}
	if strings.Contains(inner, "```") {
		return "", false
	}
	return strings.TrimSpace(inner), true
}

// splitJSONObjects splits body into top-level JSON objects. body must be
// either a JSON array of objects or a sequence of objects separated only by
// whitespace; any other character between/around them fails.
func splitJSONObjects(body string) ([]string, bool) {
	if strings.HasPrefix(body, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(body), &arr); err != nil || len(arr) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(arr))
		for _, raw := range arr {
			s := strings.TrimSpace(string(raw))
			if !strings.HasPrefix(s, "{") {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	var out []string
	i := 0
	for i < len(body) {
		if isJSONSpace(body[i]) {
			i++
			continue
		}
		if body[i] != '{' {
			return nil, false
		}
		end := matchBalancedBrace(body, i)
		if end == -1 {
			return nil, false
		}
		out = append(out, body[i:end+1])
		i = end + 1
	}
	return out, len(out) > 0
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' }

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
