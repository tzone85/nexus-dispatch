package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/security"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newSecurityTestStores builds real event+projection stores in a temp dir.
func newSecurityTestStores(t *testing.T) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return es, ps
}

func countEvents(t *testing.T, es state.EventStore, typ state.EventType) int {
	t.Helper()
	evts, err := es.List(state.EventFilter{Type: typ})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return len(evts)
}

// fakeScan returns a seam that yields canned findings.
func fakeScan(findings ...security.Finding) func(context.Context, string) ([]security.Finding, []security.ScannerKind, []security.ScannerKind) {
	return func(context.Context, string) ([]security.Finding, []security.ScannerKind, []security.ScannerKind) {
		return findings, []security.ScannerKind{security.ScannerGosec}, []security.ScannerKind{security.ScannerSemgrep}
	}
}

func newTestSecurityGate(t *testing.T, client llm.Client, kbPath string, gateSev security.Severity, autoLearn bool, scan func(context.Context, string) ([]security.Finding, []security.ScannerKind, []security.ScannerKind)) *SecurityGate {
	es, ps := newSecurityTestStores(t)
	g := NewSecurityGate(client, "test-model", 1000, kbPath, gateSev, autoLearn, es, ps)
	g.scan = scan
	return g
}

func TestSecurityGate_ScanRepo_AggregatesAndEmits(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	crit := security.Finding{Tool: "gitleaks", RuleID: "aws", Severity: security.SeverityCritical, File: "x.env", Line: 1, Title: "AWS key", Source: "scanner"}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityHigh, false, fakeScan(crit))

	report, err := g.ScanRepo(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	if report.Total() != 1 {
		t.Errorf("expected 1 finding, got %d", report.Total())
	}
	if countEvents(t, g.eventStore, state.EventSecurityScanCompleted) != 1 {
		t.Error("expected SECURITY_SCAN_COMPLETED event")
	}
}

func TestSecurityGate_ReviewStory_BlocksOnCritical(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	crit := security.Finding{Tool: "gosec", RuleID: "G101", Severity: security.SeverityCritical, File: "a.go", Line: 2, Title: "hardcoded creds", Source: "scanner"}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityHigh, false, fakeScan(crit))

	passed, summary, err := g.ReviewStory(context.Background(), "s-1", "add auth", "diff", t.TempDir())
	if err != nil {
		t.Fatalf("ReviewStory: %v", err)
	}
	if passed {
		t.Error("expected gate to BLOCK on a critical finding")
	}
	if summary == "" {
		t.Error("expected a non-empty summary describing the block")
	}
	if countEvents(t, g.eventStore, state.EventStorySecurityFailed) != 1 {
		t.Error("expected STORY_SECURITY_FAILED event")
	}
}

func TestSecurityGate_ReviewStory_PassesBelowThreshold(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	low := security.Finding{Tool: "gosec", RuleID: "G104", Severity: security.SeverityLow, File: "a.go", Line: 9, Title: "unchecked error", Source: "scanner"}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityHigh, false, fakeScan(low))

	passed, _, err := g.ReviewStory(context.Background(), "s-2", "tidy", "diff", t.TempDir())
	if err != nil {
		t.Fatalf("ReviewStory: %v", err)
	}
	if !passed {
		t.Error("a low-severity finding should NOT block (threshold is high)")
	}
	if countEvents(t, g.eventStore, state.EventStorySecurityPassed) != 1 {
		t.Error("expected STORY_SECURITY_PASSED event")
	}
}

func TestSecurityGate_SelfUpskills_OnNewVulnClass(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	// A high finding carrying a CWE the baseline KB does not have.
	novel := security.Finding{
		Tool: "semgrep", RuleID: "ssti", Severity: security.SeverityHigh,
		File: "render.py", Line: 4, Title: "Server-side template injection",
		Detail: "CWE-1336", Category: "Injection", Source: "scanner",
	}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityHigh, true, fakeScan(novel))

	baseVersion := security.BaselineKnowledgeBase().Version
	if _, err := g.ScanRepo(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	kb, err := security.LoadKnowledgeBase(kbPath)
	if err != nil {
		t.Fatalf("load KB: %v", err)
	}
	if kb.Version <= baseVersion {
		t.Errorf("KB version should grow after learning (got %d, base %d)", kb.Version, baseVersion)
	}
	if !kb.Has("CWE-1336") {
		t.Error("agent should have learned the new vuln class CWE-1336")
	}
	if countEvents(t, g.eventStore, state.EventSecurityRuleLearned) < 1 {
		t.Error("expected SECURITY_RULE_LEARNED event")
	}
}

func TestSecurityGate_DoesNotRelearnKnownClass(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	// CWE-89 (SQLi) is already in the baseline → must NOT bump the version.
	known := security.Finding{Tool: "gosec", RuleID: "G201", Severity: security.SeverityHigh, File: "db.go", Line: 1, Title: "SQLi", Detail: "CWE-89", Source: "scanner"}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityHigh, true, fakeScan(known))

	if _, err := g.ScanRepo(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	kb, _ := security.LoadKnowledgeBase(kbPath)
	if kb.Version != security.BaselineKnowledgeBase().Version {
		t.Errorf("known vuln class should not grow KB: got v%d", kb.Version)
	}
}

func TestParseLLMFindings(t *testing.T) {
	raw := []byte("Here are the issues:\n```json\n" + `[
	  {"severity":"high","title":"Missing authz check","file":"handler.go","line":12,"rule_id":"A01:2021","detail":"IDOR"},
	  {"severity":"medium","title":"Verbose error","file":"api.go","line":40}
	]` + "\n```\n")
	got := parseLLMFindings(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 LLM findings, got %d", len(got))
	}
	if got[0].Severity != security.SeverityHigh || got[0].File != "handler.go" {
		t.Errorf("bad first finding: %+v", got[0])
	}
	if got[0].Source != "llm" {
		t.Errorf("LLM findings must be tagged source=llm, got %q", got[0].Source)
	}
}
