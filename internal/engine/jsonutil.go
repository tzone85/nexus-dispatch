package engine

import (
	"encoding/json"
	"strings"
)

// extractJSON strips markdown code fences from LLM responses so that the
// content can be passed to json.Unmarshal. Many models wrap JSON output in
// ```json ... ``` even when instructed not to.
func extractJSON(raw string) string {
	s := strings.TrimSpace(raw)

	// Strip ```json ... ``` or ``` ... ```
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (with optional language tag)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	// If the response still doesn't start with JSON, find the first
	// JSON array or object marker. LLMs (especially when plugins inject
	// preamble text) may prepend non-JSON content before the actual payload.
	if len(s) > 0 && s[0] != '[' && s[0] != '{' {
		// Look for a code fence that contains JSON
		if fenceIdx := strings.Index(s, "```json"); fenceIdx != -1 {
			inner := s[fenceIdx+7:]
			if endIdx := strings.Index(inner, "```"); endIdx != -1 {
				return strings.TrimSpace(inner[:endIdx])
			}
		}
		if fenceIdx := strings.Index(s, "```\n"); fenceIdx != -1 {
			inner := s[fenceIdx+4:]
			if endIdx := strings.Index(inner, "```"); endIdx != -1 {
				return strings.TrimSpace(inner[:endIdx])
			}
		}

		// Fallback: find the first [ or { and extract from there
		arrIdx := strings.Index(s, "[")
		objIdx := strings.Index(s, "{")
		startIdx := -1
		if arrIdx >= 0 && (objIdx < 0 || arrIdx < objIdx) {
			startIdx = arrIdx
		} else if objIdx >= 0 {
			startIdx = objIdx
		}
		if startIdx >= 0 {
			s = s[startIdx:]
			// Find the matching closing bracket
			if s[0] == '[' {
				if endIdx := strings.LastIndex(s, "]"); endIdx >= 0 {
					s = s[:endIdx+1]
				}
			} else {
				if endIdx := strings.LastIndex(s, "}"); endIdx >= 0 {
					s = s[:endIdx+1]
				}
			}
			s = strings.TrimSpace(s)
		}
	}

	return s
}

// FlexibleString unmarshals either a JSON string or an array of strings into a
// single string. LLMs inconsistently return acceptance_criteria as either form.
type FlexibleString string

func (f *FlexibleString) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexibleString(s)
		return nil
	}

	// Try array of strings
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = FlexibleString(strings.Join(arr, "\n"))
		return nil
	}

	// Fallback: store raw
	*f = FlexibleString(string(data))
	return nil
}
