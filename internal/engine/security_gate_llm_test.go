package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/security"
)

const llmCriticalFinding = `[{"severity":"critical","title":"SQL injection in user filter","file":"db/users.go","line":88,"rule_id":"CWE-89","detail":"query concatenates request input; use parameterised queries"}]`

// ScanRepo with an LLM client merges the threat-model review's findings with
// the scanner findings — this drives llmReview + callLLM + parseLLMFindings.
func TestSecurityGate_ScanRepo_MergesLLMFindings(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	scannerFinding := security.Finding{Tool: "gosec", RuleID: "G101", Severity: security.SeverityHigh, File: "a.go", Line: 1, Title: "hardcoded cred", Source: "scanner"}
	client := llm.NewReplayClient(llm.CompletionResponse{Content: llmCriticalFinding})
	g := newTestSecurityGate(t, client, kbPath, security.SeverityCritical, false, fakeScan(scannerFinding))

	report, err := g.ScanRepo(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}
	if report.Total() != 2 {
		t.Fatalf("want scanner + llm findings merged (2), got %d: %+v", report.Total(), report.Findings)
	}
	var llmF *security.Finding
	for i := range report.Findings {
		if report.Findings[i].Source == "llm" {
			llmF = &report.Findings[i]
		}
	}
	if llmF == nil {
		t.Fatal("LLM finding missing from report")
	}
	if llmF.Tool != "llm" || llmF.RuleID != "CWE-89" || llmF.Severity != security.SeverityCritical || llmF.Line != 88 {
		t.Errorf("LLM finding mis-parsed: %+v", *llmF)
	}
}

// The LLM reviewing a story diff can block the story on its own — no scanner
// finding needed. Drives llmReviewDiff end-to-end through the block path.
func TestSecurityGate_ReviewStory_LLMDiffFindingBlocks(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	client := llm.NewReplayClient(llm.CompletionResponse{Content: llmCriticalFinding})
	g := newTestSecurityGate(t, client, kbPath, security.SeverityCritical, false, fakeScan())

	passed, summary, err := g.ReviewStory(context.Background(), "s-1", "add user filter", "+ query := \"SELECT * FROM users WHERE name='\" + name + \"'\"", t.TempDir())
	if err != nil {
		t.Fatalf("ReviewStory: %v", err)
	}
	if passed {
		t.Fatal("critical LLM finding must block the story")
	}
	if !strings.Contains(summary, "SQL injection") {
		t.Errorf("block summary must name the finding: %q", summary)
	}
	// The diff must have been sent to the model as data.
	if client.CallCount() != 1 || !strings.Contains(client.CallAt(0).Messages[0].Content, "<diff>") {
		t.Error("diff review prompt must wrap the diff in <diff> data tags")
	}
}

// An LLM failure must degrade to scanners-only, never abort the scan — the
// deterministic findings still make it into the report.
func TestSecurityGate_LLMFailureIsNonFatal(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	exhausted := llm.NewReplayClient() // zero responses ⇒ every call errors
	scannerFinding := security.Finding{Tool: "gitleaks", RuleID: "aws", Severity: security.SeverityCritical, File: "x.env", Line: 3, Title: "AWS key", Source: "scanner"}
	g := newTestSecurityGate(t, exhausted, kbPath, security.SeverityCritical, false, fakeScan(scannerFinding))

	report, err := g.ScanRepo(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("LLM failure must not abort the scan: %v", err)
	}
	if report.Total() != 1 || report.Findings[0].Source != "scanner" {
		t.Errorf("scanner findings must survive an LLM failure: %+v", report.Findings)
	}
}

// A prose-wrapped or garbage LLM response yields no findings rather than a
// crash or a phantom block.
func TestSecurityGate_ReviewStory_GarbageLLMResponsePasses(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	client := llm.NewReplayClient(llm.CompletionResponse{Content: "I could not review this code, sorry!"})
	g := newTestSecurityGate(t, client, kbPath, security.SeverityCritical, false, fakeScan())

	passed, _, err := g.ReviewStory(context.Background(), "s-1", "t", "+ x", t.TempDir())
	if err != nil {
		t.Fatalf("ReviewStory: %v", err)
	}
	if !passed {
		t.Error("no parseable findings ⇒ story passes")
	}
}

// A corrupt knowledge base must abort both entry points loudly — silently
// falling back to the baseline would hide that learned rules were lost.
func TestSecurityGate_CorruptKBIsALoudError(t *testing.T) {
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	if err := os.WriteFile(kbPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityCritical, false, fakeScan())

	if _, err := g.ScanRepo(context.Background(), t.TempDir()); err == nil {
		t.Error("ScanRepo must surface a corrupt KB")
	}
	if _, _, err := g.ReviewStory(context.Background(), "s-1", "t", "+x", t.TempDir()); err == nil {
		t.Error("ReviewStory must surface a corrupt KB")
	}
}

// A KB that cannot be persisted must not break the scan — upskilling is
// best-effort; the findings and the report still stand.
func TestSecurityGate_UpskillPersistFailureIsNonFatal(t *testing.T) {
	// A read-only KB file: LoadKnowledgeBase succeeds, Save's WriteFile fails.
	kbPath := filepath.Join(t.TempDir(), "kb.json")
	baseline, _ := json.Marshal(security.BaselineKnowledgeBase())
	if err := os.WriteFile(kbPath, baseline, 0o400); err != nil {
		t.Fatal(err)
	}
	novel := security.Finding{Tool: "semgrep", RuleID: "custom.novel", Severity: security.SeverityHigh, File: "a.go", Line: 1, Title: "novel class", Source: "scanner"}
	g := newTestSecurityGate(t, nil, kbPath, security.SeverityCritical, true, fakeScan(novel))

	report, err := g.ScanRepo(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("persist failure must not abort the scan: %v", err)
	}
	if report.Total() != 1 {
		t.Errorf("findings must survive a persist failure: %+v", report.Findings)
	}
}

func TestVulnClassID_FallbackChain(t *testing.T) {
	cases := []struct {
		name string
		f    security.Finding
		want string
	}{
		{"cwe-from-ruleid", security.Finding{RuleID: "CWE-89"}, "CWE-89"},
		{"cwe-from-detail", security.Finding{RuleID: "G304", Detail: "path traversal CWE-22 via input"}, "CWE-22"},
		{"category-when-no-cwe", security.Finding{RuleID: "", Category: "Insecure Design"}, "Insecure Design"},
		{"tool-rule-when-nothing-else", security.Finding{Tool: "semgrep", RuleID: "custom.rule"}, "semgrep:custom.rule"},
		{"empty-when-unclassifiable", security.Finding{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vulnClassID(tc.f); got != tc.want {
				t.Errorf("vulnClassID(%+v) = %q, want %q", tc.f, got, tc.want)
			}
		})
	}
}

func TestCweOf_ExtractsAndBounds(t *testing.T) {
	cases := []struct {
		in   security.Finding
		want string
	}{
		{security.Finding{RuleID: "CWE-798"}, "CWE-798"},
		{security.Finding{Detail: "maps to CWE-89: injection"}, "CWE-89"},
		{security.Finding{Category: "A03 CWE-79 XSS"}, "CWE-79"},
		{security.Finding{Detail: "CWE- with no digits"}, ""},
		{security.Finding{Title: "CWE-1 in title is ignored (title not scanned)"}, ""},
	}
	for _, tc := range cases {
		if got := cweOf(tc.in); got != tc.want {
			t.Errorf("cweOf(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseLLMFindings_WrappedAndInvalid(t *testing.T) {
	if got := parseLLMFindings([]byte("")); got != nil {
		t.Errorf("empty response ⇒ nil, got %v", got)
	}
	if got := parseLLMFindings([]byte("no json here at all")); got != nil {
		t.Errorf("prose-only response ⇒ nil, got %v", got)
	}
	// Fence-wrapped array must still parse (extractJSON handles the wrapping).
	wrapped := "Here are my findings:\n```json\n" + llmCriticalFinding + "\n```\nLet me know."
	got := parseLLMFindings([]byte(wrapped))
	if len(got) != 1 || got[0].RuleID != "CWE-89" || got[0].Source != "llm" {
		t.Errorf("fence-wrapped findings must parse: %+v", got)
	}
	// A JSON object (not array) is a shape mismatch ⇒ nil, logged, no crash.
	if got := parseLLMFindings([]byte(`{"severity":"high"}`)); got != nil {
		t.Errorf("non-array JSON ⇒ nil, got %v", got)
	}
}
