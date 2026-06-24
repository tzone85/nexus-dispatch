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

func TestLooksLikeResolverChatter(t *testing.T) {
	// Prose-only replies that destroy files when written verbatim (no fence to
	// extract, so extraction returns the prose itself).
	chatter := []string{
		"Conflict resolved. Kept both sides:\n\nNo selector collisions — separate class namespaces, all functionality retained.",
		"Resolved file content:",
		"Permission denied by harness. Cannot write the file myself. Resolved content below:",
		"Working tree is `master` (no `src/`), so I can't write the file here.",
		"Both sides merged — HEAD tests + branch tests. Write blocked on permission, so resolved content below:",
		"Want me to apply this on branch nxd/abc and run the tests?",
	}
	for _, c := range chatter {
		if !looksLikeResolverChatter(c) {
			t.Errorf("expected chatter to be flagged:\n%q", c)
		}
	}

	// Real merged source must NOT be flagged.
	code := []string{
		"package main\n\nfunc main() {}\n",
		"import { useState } from 'react';\nexport const App = () => null;",
		".ui-button { color: red; }\n.app-layout { display: flex; }",
		"{\n  \"name\": \"frontend\",\n  \"private\": true\n}",
	}
	for _, c := range code {
		if looksLikeResolverChatter(c) {
			t.Errorf("real source wrongly flagged as chatter:\n%q", c)
		}
	}
}
