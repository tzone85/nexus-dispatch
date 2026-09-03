package state

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// truncatedKey is set to true in a payload whose strings were cut to fit the
// per-event size cap.
const truncatedKey = "truncated"

// stringRef addresses one string leaf inside a decoded payload so it can be
// rewritten in place.
type stringRef struct {
	set func(string)
	val string
}

// collectStrings walks a decoded JSON value and returns every string leaf
// with a setter that replaces it in its container.
func collectStrings(v any) []stringRef {
	var out []stringRef
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			k := k
			switch c := child.(type) {
			case string:
				out = append(out, stringRef{val: c, set: func(s string) { node[k] = s }})
			default:
				out = append(out, collectStrings(c)...)
			}
		}
	case []any:
		for i, child := range node {
			i := i
			switch c := child.(type) {
			case string:
				out = append(out, stringRef{val: c, set: func(s string) { node[i] = s }})
			default:
				out = append(out, collectStrings(c)...)
			}
		}
	}
	return out
}

// TruncatePayload trims the longest string values in payload (recursively)
// until its JSON encoding fits within budget bytes, marking the payload with
// "truncated": true. The map is modified in place and returned. It returns
// false when nothing needed trimming — or when nothing could be trimmed,
// in which case the caller must still write the event (never drop it).
func TruncatePayload(payload map[string]any, budget int) (map[string]any, bool) {
	if payload == nil {
		return nil, false
	}
	enc, err := json.Marshal(payload)
	if err != nil || len(enc) <= budget {
		return payload, false
	}
	truncated := false
	for attempt := 0; attempt < 64 && len(enc) > budget; attempt++ {
		refs := collectStrings(payload)
		sort.Slice(refs, func(i, j int) bool { return len(refs[i].val) > len(refs[j].val) })
		if len(refs) == 0 {
			break
		}
		longest := refs[0]
		excess := len(enc) - budget
		cut := excess
		if half := len(longest.val) / 2; cut < half {
			cut = half // guarantee geometric progress
		}
		newVal, ok := cutString(longest.val, cut)
		if !ok {
			break
		}
		longest.set(newVal)
		if !truncated {
			payload[truncatedKey] = true
			truncated = true
		}
		enc, err = json.Marshal(payload)
		if err != nil {
			return payload, truncated
		}
	}
	return payload, truncated
}

// cutString removes at least cut bytes from the end of s (respecting UTF-8
// boundaries) and appends a "…[truncated N bytes]" marker that names the
// total number of original bytes removed across successive cuts. It returns
// false when s is already too short to shrink further.
func cutString(s string, cut int) (string, bool) {
	orig, prior := stripMarker(s)
	if len(orig) <= 16 {
		return s, false
	}
	keep := len(orig) - cut
	if keep < 16 {
		keep = 16
	}
	for keep > 0 && !utf8.RuneStart(orig[keep]) {
		keep--
	}
	removed := len(orig) - keep + prior
	return fmt.Sprintf("%s…[truncated %d bytes]", orig[:keep], removed), true
}

// stripMarker splits a previously-truncated string into its content and the
// byte count recorded in its marker, so repeated cuts report a cumulative
// total instead of nesting markers.
func stripMarker(s string) (content string, removed int) {
	const open = "…[truncated "
	i := strings.LastIndex(s, open)
	if i < 0 || !strings.HasSuffix(s, " bytes]") {
		return s, 0
	}
	var n int
	if _, err := fmt.Sscanf(s[i:], "…[truncated %d bytes]", &n); err != nil {
		return s, 0
	}
	return s[:i], n
}
