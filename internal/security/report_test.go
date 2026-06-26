package security

import (
	"strings"
	"testing"
)

func sampleReport() Report {
	return Report{
		RepoDir:     "/repo",
		Languages:   []string{"go"},
		ScannersRun: []ScannerKind{ScannerGosec, ScannerGitleaks},
		Skipped:     []ScannerKind{ScannerSemgrep},
		KBVersion:   1,
		Findings: []Finding{
			{Tool: "gitleaks", RuleID: "aws", Severity: SeverityCritical, File: "x.env", Line: 1, Title: "AWS key"},
			{Tool: "gosec", RuleID: "G201", Severity: SeverityHigh, File: "db.go", Line: 9, Title: "SQLi"},
			{Tool: "gosec", RuleID: "G104", Severity: SeverityLow, File: "a.go", Line: 3, Title: "unchecked error"},
		},
	}
}

func TestReport_CountsAndMax(t *testing.T) {
	r := sampleReport()
	if r.Total() != 3 {
		t.Errorf("total = %d, want 3", r.Total())
	}
	c := r.Counts()
	if c[SeverityCritical] != 1 || c[SeverityHigh] != 1 || c[SeverityLow] != 1 {
		t.Errorf("counts wrong: %+v", c)
	}
	if r.MaxSeverity() != SeverityCritical {
		t.Errorf("max severity = %v, want critical", r.MaxSeverity())
	}
}

func TestReport_HasAtLeast(t *testing.T) {
	r := sampleReport()
	if !r.HasAtLeast(SeverityHigh) {
		t.Error("report with a critical should satisfy HasAtLeast(high)")
	}
	empty := Report{}
	if empty.HasAtLeast(SeverityLow) {
		t.Error("empty report should not satisfy HasAtLeast(low)")
	}
}

func TestReport_FormatMarkdown(t *testing.T) {
	md := sampleReport().FormatMarkdown()
	for _, want := range []string{"AWS key", "x.env", "CRITICAL", "gosec", "Skipped"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}
