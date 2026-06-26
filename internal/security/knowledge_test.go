package security

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"critical": SeverityCritical,
		"HIGH":     SeverityHigh,
		"Medium":   SeverityMedium,
		"low":      SeverityLow,
		"info":     SeverityInfo,
		"warning":  SeverityMedium, // common scanner synonym
		"error":    SeverityHigh,   // common scanner synonym
		"unknown":  SeverityInfo,   // safe default
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSeverity_AtLeast(t *testing.T) {
	if !SeverityCritical.AtLeast(SeverityHigh) {
		t.Error("critical should be >= high")
	}
	if SeverityLow.AtLeast(SeverityHigh) {
		t.Error("low should not be >= high")
	}
	if !SeverityHigh.AtLeast(SeverityHigh) {
		t.Error("high should be >= high (inclusive)")
	}
}

func TestDedupeFindings(t *testing.T) {
	in := []Finding{
		{Tool: "gosec", RuleID: "G101", File: "a.go", Line: 10, Severity: SeverityHigh},
		{Tool: "gosec", RuleID: "G101", File: "a.go", Line: 10, Severity: SeverityHigh}, // dup
		{Tool: "gosec", RuleID: "G101", File: "a.go", Line: 11, Severity: SeverityHigh}, // diff line
		{Tool: "semgrep", RuleID: "G101", File: "a.go", Line: 10, Severity: SeverityHigh}, // diff tool
	}
	out := DedupeFindings(in)
	if len(out) != 3 {
		t.Errorf("expected 3 unique findings, got %d", len(out))
	}
}

func TestBaselineKnowledgeBase_CoversOWASPTop10(t *testing.T) {
	kb := BaselineKnowledgeBase()
	// All ten OWASP 2021 categories must be present.
	for _, id := range []string{
		"A01:2021", "A02:2021", "A03:2021", "A04:2021", "A05:2021",
		"A06:2021", "A07:2021", "A08:2021", "A09:2021", "A10:2021",
	} {
		if !kb.Has(id) {
			t.Errorf("baseline KB missing OWASP category %s", id)
		}
	}
	if len(kb.Rules) < 10 {
		t.Errorf("baseline KB should have >=10 rules, got %d", len(kb.Rules))
	}
	// Every baseline rule must carry detection + remediation guidance.
	for _, r := range kb.Rules {
		if strings.TrimSpace(r.Detection) == "" || strings.TrimSpace(r.Remediation) == "" {
			t.Errorf("rule %s missing detection/remediation guidance", r.ID)
		}
		if r.Source != RuleBaseline {
			t.Errorf("rule %s should be baseline-sourced, got %s", r.ID, r.Source)
		}
	}
}

func TestKnowledgeBase_Add_DedupAndImmutable(t *testing.T) {
	kb := BaselineKnowledgeBase()
	before := len(kb.Rules)

	learned := VulnRule{ID: "CWE-9999", Title: "Test vuln", Detection: "d", Remediation: "r", Severity: SeverityHigh, Source: RuleLearned, AddedAt: "2026-06-26T00:00:00Z"}
	kb2 := kb.Add(learned)

	if len(kb.Rules) != before {
		t.Error("Add must not mutate the receiver (immutability)")
	}
	if len(kb2.Rules) != before+1 {
		t.Errorf("Add should append one rule, got %d", len(kb2.Rules))
	}
	if kb2.Version != kb.Version+1 {
		t.Errorf("Add should bump version: got %d want %d", kb2.Version, kb.Version+1)
	}
	// Adding the same ID again is a no-op (dedup).
	kb3 := kb2.Add(learned)
	if len(kb3.Rules) != len(kb2.Rules) {
		t.Error("Add of an existing ID must be a no-op")
	}
}

func TestKnowledgeBase_RulesFor_LanguageFilter(t *testing.T) {
	kb := BaselineKnowledgeBase().Add(VulnRule{
		ID: "GO-ONLY-1", Title: "Go specific", Detection: "d", Remediation: "r",
		Severity: SeverityMedium, Languages: []string{"go"}, Source: RuleLearned, AddedAt: "t",
	})
	goRules := kb.RulesFor([]string{"go"})
	pyRules := kb.RulesFor([]string{"python"})

	hasGoOnly := func(rs []VulnRule) bool {
		for _, r := range rs {
			if r.ID == "GO-ONLY-1" {
				return true
			}
		}
		return false
	}
	if !hasGoOnly(goRules) {
		t.Error("go-only rule should appear for go")
	}
	if hasGoOnly(pyRules) {
		t.Error("go-only rule should NOT appear for python")
	}
	// Language-agnostic baseline rules appear for every language.
	if len(pyRules) < 10 {
		t.Errorf("language-agnostic baseline rules should apply to python too, got %d", len(pyRules))
	}
}

func TestKnowledgeBase_SaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knowledge.json")

	orig := BaselineKnowledgeBase().Add(VulnRule{
		ID: "CWE-1234", Title: "Roundtrip", Detection: "d", Remediation: "r",
		Severity: SeverityCritical, Source: RuleLearned, AddedAt: "2026-06-26T00:00:00Z",
	})
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadKnowledgeBase(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Version != orig.Version {
		t.Errorf("version mismatch: got %d want %d", loaded.Version, orig.Version)
	}
	if len(loaded.Rules) != len(orig.Rules) {
		t.Errorf("rule count mismatch: got %d want %d", len(loaded.Rules), len(orig.Rules))
	}
	if !loaded.Has("CWE-1234") {
		t.Error("learned rule lost in round trip")
	}
}

func TestLoadKnowledgeBase_MissingFileReturnsBaseline(t *testing.T) {
	kb, err := LoadKnowledgeBase(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should return baseline, not error: %v", err)
	}
	if !kb.Has("A01:2021") {
		t.Error("missing-file fallback should be the baseline KB")
	}
}

func TestKnowledgeBase_Checklist_RendersForPrompt(t *testing.T) {
	kb := BaselineKnowledgeBase()
	md := kb.Checklist([]string{"go"})
	if !strings.Contains(md, "A03:2021") {
		t.Error("checklist should reference OWASP injection category")
	}
	// Should be non-trivial markdown the LLM can act on.
	if len(md) < 200 {
		t.Errorf("checklist too short to be useful: %d bytes", len(md))
	}
}
