package engine

import (
	"strings"
	"testing"
)

func TestExtractResolvedFileContent(t *testing.T) {
	// The exact shape that corrupted a real client build's package.json: the
	// model returned conversational preamble + a fenced block + postamble.
	chatty := "Resolved. Kept `@playwright/test` (HEAD) — needed by test:e2e.\n\n" +
		"Write blocked on permission. File content to apply:\n\n" +
		"```json\n{\n  \"name\": \"isiqalo-pos-frontend\",\n  \"private\": true\n}\n```\n\n" +
		"Grant write to apply, or paste yourself."
	got := extractResolvedFileContent(chatty)
	want := "{\n  \"name\": \"isiqalo-pos-frontend\",\n  \"private\": true\n}"
	if got != want {
		t.Fatalf("expected only the fenced JSON, got:\n%q", got)
	}
	if strings.Contains(got, "Resolved.") || strings.Contains(got, "Grant write") || strings.Contains(got, "```") {
		t.Fatalf("extracted content still contains chatter/fences:\n%q", got)
	}

	// A clean response (no fence) passes through unchanged.
	clean := "package main\n\nfunc main() {}\n"
	if g := extractResolvedFileContent(clean); strings.TrimSpace(g) != strings.TrimSpace(clean) {
		t.Fatalf("clean content altered: %q", g)
	}

	// A response that is ONLY a fenced block (no chatter) still yields its body.
	fenced := "```go\npackage x\n```"
	if g := extractResolvedFileContent(fenced); g != "package x" {
		t.Fatalf("fenced-only extraction failed: %q", g)
	}
}
