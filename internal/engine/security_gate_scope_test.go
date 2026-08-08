package engine

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/security"
)

func TestChangedFilesFromDiff(t *testing.T) {
	diff := "diff --git a/app/Support/X.php b/app/Support/X.php\n" +
		"--- /dev/null\n+++ b/app/Support/X.php\n@@ -0,0 +1 @@\n+<?php\n" +
		"diff --git a/tests/x_test.php b/tests/x_test.php\n" +
		"--- /dev/null\n+++ b/tests/x_test.php\n@@ -0,0 +1 @@\n+<?php\n"
	got := changedFilesFromDiff(diff)
	for _, want := range []string{"app/Support/X.php", "X.php", "tests/x_test.php", "x_test.php"} {
		if !got[want] {
			t.Errorf("expected changed file %q in %v", want, got)
		}
	}
	if got["package.json"] {
		t.Error("package.json was never changed; must not appear")
	}
}

func TestScopeToChanged_DropsPreexistingVulns(t *testing.T) {
	// The 2026-08-08 gauntlet: a PHP-validator story was blocked by 88 critical
	// npm-audit findings in a pre-existing package.json it never touched.
	findings := []security.Finding{
		{Tool: "npm-audit", File: "package.json", Severity: security.SeverityCritical, Title: "vuln dep"},
		{Tool: "gosec", File: "app/Support/X.php", Severity: security.SeverityHigh, Title: "real"},
		{Tool: "llm", File: "", Severity: security.SeverityHigh, Title: "diff-scoped"},
	}
	changed := map[string]bool{"app/Support/X.php": true, "X.php": true}
	kept := scopeToChanged(findings, changed)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept (changed file + unattributable), got %d: %+v", len(kept), kept)
	}
	for _, f := range kept {
		if f.File == "package.json" {
			t.Error("pre-existing package.json finding must be scoped out of the per-story gate")
		}
	}
}

func TestScopeToChanged_EmptyChangedKeepsAll(t *testing.T) {
	findings := []security.Finding{{File: "a", Severity: security.SeverityHigh}}
	if got := scopeToChanged(findings, nil); len(got) != 1 {
		t.Fatalf("empty changed set must keep all findings, got %d", len(got))
	}
}

// TestBlockingFindings_LLMAdvisoryByDefault locks fix #9: an LLM-only critical
// with no scanner corroboration is advisory (does not block). The gauntlet
// caught the local model hallucinating "[CRITICAL] path traversal in
// app/Support/.gitkeep/file.txt" — a path that never existed — pausing a merge
// on a nonexistent vuln.
func TestBlockingFindings_LLMAdvisoryByDefault(t *testing.T) {
	g := &SecurityGate{gateSeverity: security.SeverityCritical, llmBlocks: false}
	findings := []security.Finding{
		{Source: "llm", Tool: "llm", File: "app/Support/.gitkeep/file.txt", Severity: security.SeverityCritical, Title: "path traversal"},
	}
	if b := g.blockingFindings(findings); len(b) != 0 {
		t.Fatalf("LLM-only critical must be advisory (0 blockers), got %d: %+v", len(b), b)
	}
}

// TestBlockingFindings_ScannerAlwaysBlocks proves a deterministic scanner
// finding at/above severity always blocks, regardless of the LLM policy.
func TestBlockingFindings_ScannerAlwaysBlocks(t *testing.T) {
	g := &SecurityGate{gateSeverity: security.SeverityCritical, llmBlocks: false}
	findings := []security.Finding{
		{Source: "scanner", Tool: "gitleaks", File: "config.env", Severity: security.SeverityCritical, Title: "AWS key"},
	}
	if b := g.blockingFindings(findings); len(b) != 1 {
		t.Fatalf("scanner critical must block, got %d blockers", len(b))
	}
}

// TestBlockingFindings_LLMCorroboratedBlocks proves an LLM finding on a file a
// scanner also flagged is NOT advisory — corroboration restores blocking.
func TestBlockingFindings_LLMCorroboratedBlocks(t *testing.T) {
	g := &SecurityGate{gateSeverity: security.SeverityCritical, llmBlocks: false}
	findings := []security.Finding{
		{Source: "scanner", Tool: "semgrep", File: "db.go", Severity: security.SeverityHigh, Title: "sqli"},
		{Source: "llm", Tool: "llm", File: "db.go", Severity: security.SeverityCritical, Title: "sqli exploit"},
	}
	b := g.blockingFindings(findings)
	// scanner high (below critical gate) is filtered by severity; the LLM
	// critical is corroborated by the scanner's same-file finding, so it blocks.
	if len(b) != 1 || b[0].Source != "llm" {
		t.Fatalf("corroborated LLM critical must block, got %+v", b)
	}
}

// TestBlockingFindings_LLMBlocksWhenOptedIn proves the strict-mode switch:
// llmBlocks=true lets an LLM-only critical block again.
func TestBlockingFindings_LLMBlocksWhenOptedIn(t *testing.T) {
	g := &SecurityGate{gateSeverity: security.SeverityCritical, llmBlocks: true}
	findings := []security.Finding{
		{Source: "llm", Tool: "llm", File: "x.go", Severity: security.SeverityCritical, Title: "hmm"},
	}
	if b := g.blockingFindings(findings); len(b) != 1 {
		t.Fatalf("llmBlocks=true must let LLM-only critical block, got %d", len(b))
	}
}
