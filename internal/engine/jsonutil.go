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
