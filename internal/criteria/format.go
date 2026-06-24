package criteria

// This file turns a story's acceptance-criteria string — often authored by the
// Tech-Lead LLM as a single run-on technical blob — into a list of discrete,
// human-readable items. The goal is that a person clicking through a story in
// the dashboard or CLI can read the criteria and understand the intent, rather
// than parsing a wall of text.
//
// All functions here are pure (no I/O), so the segmentation rules are pinned by
// unit tests rather than discovered at render time.

import "strings"

// abbreviations are lowercased tokens (sans trailing period) after which a
// "period + space" must NOT be treated as a sentence boundary. Without this,
// "e.g. encoding/json" would split mid-thought.
var abbreviations = map[string]bool{
	"e.g": true, "i.e": true, "etc": true, "vs": true, "cf": true,
	"al": true, "approx": true, "no": true, "inc": true, "ltd": true,
	"mr": true, "ms": true, "mrs": true, "dr": true, "fig": true,
	"st": true, "jr": true, "sr": true,
}

// Format splits a raw acceptance-criteria string into clean, readable items.
//
//   - Multi-line input (or input with list markers) is split per line, with any
//     leading bullet/number marker stripped.
//   - Single-paragraph input is segmented into sentences, guarding against
//     abbreviations (e.g.) and intra-identifier periods (WorldState.copy()).
//
// Empty or whitespace-only input returns nil.
func Format(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	var lines []string
	if strings.Contains(trimmed, "\n") {
		lines = strings.Split(trimmed, "\n")
	} else {
		lines = splitSentences(trimmed)
	}

	items := make([]string, 0, len(lines))
	for _, line := range lines {
		item := strings.TrimSpace(stripMarker(line))
		if item != "" {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

// FormatMarkdown renders the formatted items as a dash-bulleted markdown list,
// or "" when there are no items.
func FormatMarkdown(raw string) string {
	items := Format(raw)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- ")
		b.WriteString(item)
	}
	return b.String()
}

// splitSentences segments a single paragraph on terminal punctuation followed by
// whitespace, keeping the punctuation with its sentence and skipping false
// boundaries (abbreviations, identifier periods with no trailing space).
func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		// A boundary requires whitespace immediately after the punctuation.
		if i+1 >= len(s) || !isSpace(s[i+1]) {
			continue
		}
		if c == '.' && isAbbreviation(s[start:i]) {
			continue
		}
		out = append(out, strings.TrimSpace(s[start:i+1]))
		// Advance past the run of whitespace to the next sentence.
		j := i + 1
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		start = j
		i = j - 1
	}
	if start < len(s) {
		if tail := strings.TrimSpace(s[start:]); tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

// isAbbreviation reports whether the word ending the segment (the token
// immediately before the period) is a known abbreviation.
func isAbbreviation(segment string) bool {
	end := len(segment)
	wordStart := end
	for wordStart > 0 && !isSpace(segment[wordStart-1]) {
		wordStart--
	}
	word := strings.ToLower(segment[wordStart:end])
	return abbreviations[word]
}

// stripMarker removes a single leading list marker: "-", "*", "•", "–", or an
// ordinal like "1." / "2)".
func stripMarker(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return s
	}
	switch s[0] {
	case '-', '*':
		return strings.TrimSpace(s[1:])
	}
	// Unicode bullet/dash glyphs.
	for _, glyph := range []string{"•", "–", "—", "‣", "·"} {
		if strings.HasPrefix(s, glyph) {
			return strings.TrimSpace(s[len(glyph):])
		}
	}
	// Ordinal marker: digits followed by '.' or ')'.
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(s) && (s[digits] == '.' || s[digits] == ')') {
		return strings.TrimSpace(s[digits+1:])
	}
	return s
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}
