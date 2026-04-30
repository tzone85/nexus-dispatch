package engine

import (
	"encoding/json"
	"strings"
)

// extractJSON strips markdown code fences and conversational preambles
// from LLM responses so the content can be passed to json.Unmarshal.
// Handles three common LLM output patterns:
//  1. Pure JSON: returned as-is
//  2. Code-fenced JSON (```json ... ```): fence stripped, optional preamble OK
//  3. Preamble + JSON (e.g. "Here you go: [...]"): preamble stripped
func extractJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Pattern 2: code fence (anywhere — covers preamble + fenced cases)
	if fenceStart := strings.Index(s, "```"); fenceStart != -1 {
		inner := s[fenceStart+3:]
		// Skip optional language tag (everything up to first newline)
		if nl := strings.Index(inner, "\n"); nl != -1 {
			inner = inner[nl+1:]
		}
		// Strip closing fence
		if fenceEnd := strings.Index(inner, "```"); fenceEnd != -1 {
			inner = inner[:fenceEnd]
		}
		return strings.TrimSpace(inner)
	}

	// Pattern 3: depth-aware bracket matching (handles nested objects/arrays
	// and brackets inside JSON string values).
	//
	// We scan forward from the first delimiter, tracking open/close depth and
	// skipping brackets that appear inside JSON string literals (between
	// unescaped double-quotes). This avoids two classes of bugs with the
	// naive LastIndexByte approach:
	//   1. Stray closing brackets after the JSON payload get included.
	//   2. Opening brackets in the preamble (e.g. "Score [10/10]:") shift
	//      `first` past the real JSON start.
	//
	// To handle case 2 we try every candidate opening bracket position until
	// depth-scanning succeeds in finding a balanced match.
	firstObj := strings.Index(s, "{")
	firstArr := strings.Index(s, "[")
	first := firstObj
	openCh, closeCh := byte('{'), byte('}')
	if firstArr != -1 && (firstObj == -1 || firstArr < firstObj) {
		first = firstArr
		openCh, closeCh = '[', ']'
	}
	if first == -1 {
		return s // no JSON markers — return as-is
	}

	// Try each occurrence of openCh starting from first. We need this loop
	// because the preamble may contain the same bracket character (e.g. "[10/10]:").
	// A successful depth scan (end != -1) means we found a balanced JSON token.
	for start := first; start < len(s); {
		if s[start] != openCh {
			next := strings.IndexByte(s[start:], openCh)
			if next == -1 {
				break
			}
			start += next
		}

		depth := 0
		inString := false
		end := -1
		for i := start; i < len(s); i++ {
			ch := s[i]
			if ch == '\\' && inString {
				i++ // skip escaped character
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if ch == openCh {
				depth++
			} else if ch == closeCh {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end != -1 {
			candidate := strings.TrimSpace(s[start : end+1])
			// Accept only if it is structurally valid JSON; this skips
			// non-JSON balanced tokens in the preamble (e.g. "[10/10]").
			if json.Valid([]byte(candidate)) {
				return candidate
			}
		}
		// This candidate didn't balance or wasn't valid JSON — try the next
		// openCh occurrence.
		next := strings.IndexByte(s[start+1:], openCh)
		if next == -1 {
			break
		}
		start = start + 1 + next
	}

	return s // unmatched brackets — return as-is
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
