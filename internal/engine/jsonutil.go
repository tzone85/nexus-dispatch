package engine

import (
	"encoding/json"
	"fmt"
	"strings"
)

// stripJSONComments removes `// ...` line comments and trailing commas from
// LLM-generated JSON. Strict JSON forbids both, but small models commonly
// emit them. Comments inside string literals are preserved.
//
// This is a forgiving pre-processor — it makes a best effort, then the
// caller still validates with json.Valid.
func stripJSONComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inString := false
	escape := false
	i := 0
	for i < len(s) {
		c := s[i]
		if inString {
			b.WriteByte(c)
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			i++
			continue
		}
		// Strip // comments to end of line.
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			j := i + 2
			for j < len(s) && s[j] != '\n' {
				j++
			}
			i = j
			continue
		}
		// Strip /* ... */ block comments.
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			j := i + 2
			for j+1 < len(s) && !(s[j] == '*' && s[j+1] == '/') {
				j++
			}
			i = j + 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	out := b.String()
	// Trailing commas before } or ] are illegal in JSON; remove them.
	out = trailingCommaRe.ReplaceAllString(out, "$1")
	return out
}

// trailingCommaRe matches `,` followed by optional whitespace and a closing
// bracket. Used by stripJSONComments to clean up LLM-emitted trailing commas.
var trailingCommaRe = mustCompileTrailingCommaRe()

func mustCompileTrailingCommaRe() trailingCommaPattern {
	return trailingCommaPattern{}
}

// Tiny inline matcher to avoid pulling in regexp at the very top of the
// engine package's init for one pattern. We just look for `,<ws>}` or
// `,<ws>]` and rewrite. Implemented as a small struct with a method to
// keep the API symmetric with regexp.
type trailingCommaPattern struct{}

func (trailingCommaPattern) ReplaceAllString(in, _ string) string {
	var b strings.Builder
	b.Grow(len(in))
	i := 0
	for i < len(in) {
		if in[i] == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				// skip the comma
				i++
				continue
			}
		}
		b.WriteByte(in[i])
		i++
	}
	return b.String()
}

// extractJSON strips markdown code fences and conversational preambles
// from LLM responses so the content can be passed to json.Unmarshal.
// Handles three common LLM output patterns:
//  1. Pure JSON: returned as-is
//  2. Code-fenced JSON (```json ... ```): fence stripped, optional preamble OK
//  3. Preamble + JSON (e.g. "Here you go: [...]"): preamble stripped
//
// Also strips `//` line comments and `/* */` block comments which small
// models routinely insert despite "JSON only" instructions, and removes
// trailing commas before } or ].
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
		return stripJSONComments(strings.TrimSpace(inner))
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
			// Try after stripping LLM-style line comments / trailing commas.
			cleaned := stripJSONComments(candidate)
			if json.Valid([]byte(cleaned)) {
				return cleaned
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

// FlexibleStringSlice unmarshals either a JSON array of strings, a single
// string (split on commas), or null into a []string. Live-test discovery:
// small models routinely emit owned_files / depends_on as a single string
// like "src/foo.go, src/bar.go" instead of the requested array, blowing up
// strict json.Unmarshal.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Null / empty → empty slice.
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == "" {
		*f = nil
		return nil
	}

	// Try array of strings (the canonical shape).
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*f = arr
		return nil
	}

	// Try single string — split on commas.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			*f = nil
			return nil
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		*f = out
		return nil
	}

	return fmt.Errorf("FlexibleStringSlice: cannot decode %s", string(data))
}
