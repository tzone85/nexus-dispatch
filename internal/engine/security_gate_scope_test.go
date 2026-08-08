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
