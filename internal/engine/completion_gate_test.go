package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// fakeFixClient records how many fix-agent invocations the gate made and
// returns a canned response so applyFix succeeds without spawning a real agent.
type fakeFixClient struct {
	mu    sync.Mutex
	calls int
	last  llm.CompletionRequest
}

func (c *fakeFixClient) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.last = req
	return llm.CompletionResponse{Content: "applied the fix"}, nil
}

func (c *fakeFixClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func green() VerificationResult {
	return VerificationResult{BuildPasses: true, TestsPassing: 3, TestsTotal: 3}
}

func red() VerificationResult {
	return VerificationResult{BuildPasses: false, Gaps: []VerificationGap{
		{Category: "build", Severity: "critical", File: "main.go", Detail: "does not compile"},
	}}
}

// scriptedVerify returns each result in sequence, repeating the last forever.
func scriptedVerify(results ...VerificationResult) (verifyFunc, *int) {
	calls := 0
	fn := func(_ context.Context, _ string, _ int) VerificationResult {
		r := results[min(calls, len(results)-1)]
		calls++
		return r
	}
	return fn, &calls
}

func newTestGate(t *testing.T, client llm.Client, maxCycles int, verify verifyFunc) (*CompletionGate, string) {
	t.Helper()
	repoDir := t.TempDir()
	g := NewCompletionGate(client, "test-model", 1000, maxCycles, "main", nil, nil)
	g.verify = verify
	g.pull = func(_, _ string) {} // no-op pull in tests
	return g, repoDir
}

func TestCompletionGate_GreenFirstPass_NoFix(t *testing.T) {
	client := &fakeFixClient{}
	verify, vCalls := scriptedVerify(green())
	g, repoDir := newTestGate(t, client, 2, verify)

	passed := g.Run(context.Background(), "REQ-1", repoDir)

	if !passed {
		t.Fatal("expected gate to pass on a green first verification")
	}
	if client.callCount() != 0 {
		t.Errorf("expected no fix-agent calls on green, got %d", client.callCount())
	}
	if *vCalls != 1 {
		t.Errorf("expected exactly 1 verification, got %d", *vCalls)
	}
}

func TestCompletionGate_RedThenGreen_AutoFixes(t *testing.T) {
	client := &fakeFixClient{}
	verify, vCalls := scriptedVerify(red(), green())
	g, repoDir := newTestGate(t, client, 2, verify)

	passed := g.Run(context.Background(), "REQ-2", repoDir)

	if !passed {
		t.Fatal("expected gate to pass after one successful auto-fix")
	}
	if client.callCount() != 1 {
		t.Errorf("expected exactly 1 fix-agent call, got %d", client.callCount())
	}
	if *vCalls != 2 {
		t.Errorf("expected 2 verifications (initial + post-fix), got %d", *vCalls)
	}
}

func TestCompletionGate_StaysRed_Blocks(t *testing.T) {
	client := &fakeFixClient{}
	verify, _ := scriptedVerify(red()) // always red
	g, repoDir := newTestGate(t, client, 2, verify)

	passed := g.Run(context.Background(), "REQ-3", repoDir)

	if passed {
		t.Fatal("expected gate to block when verification never goes green")
	}
	if client.callCount() != 2 {
		t.Errorf("expected fix-agent invoked maxCycles=2 times, got %d", client.callCount())
	}
}

func TestCompletionGate_NilClient_DegradesToHardGate(t *testing.T) {
	verify, _ := scriptedVerify(red())
	g, repoDir := newTestGate(t, nil, 2, verify) // no godmode client wired

	passed := g.Run(context.Background(), "REQ-4", repoDir)

	if passed {
		t.Fatal("expected hard gate to block on red with no auto-fix client")
	}
}

func TestCompletionGate_WritesGapsFileOnRed(t *testing.T) {
	client := &fakeFixClient{}
	verify, _ := scriptedVerify(red())
	g, repoDir := newTestGate(t, client, 1, verify)

	g.Run(context.Background(), "REQ-5", repoDir)

	if _, err := os.Stat(filepath.Join(repoDir, ".nxd-fix-gaps.md")); err != nil {
		t.Errorf("expected .nxd-fix-gaps.md to be written for operator transparency: %v", err)
	}
}

// writeGoModule writes a minimal buildable/unbuildable Go module into dir.
func writeGoModule(t *testing.T, dir, mainBody string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gatecheck\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainBody), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
}

// TestCompletionGate_RealVerify_BlocksBrokenGoModule drives the gate's REAL
// default verification (an actual `go build`) — not the scripted seam — against
// a module that does not compile, with no auto-fix client. The gate must block.
// This proves the real RunVerificationLoop → ShouldRunFixCycle → gate-decision
// path integrates on a real filesystem.
func TestCompletionGate_RealVerify_BlocksBrokenGoModule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-build verification in -short mode")
	}
	repoDir := t.TempDir()
	writeGoModule(t, repoDir, "package main\n\nfunc main() {\n\tvar x int = \"not an int\"\n\t_ = x\n}\n")

	// nil client ⇒ hard gate (verify once, no auto-fix). Real verify seam.
	g := NewCompletionGate(nil, "", 0, 0, "main", nil, nil)
	g.pull = func(_, _ string) {}

	if g.Run(context.Background(), "REQ-REAL-RED", repoDir) {
		t.Fatal("expected gate to BLOCK a composed mainline that does not compile")
	}
}

// TestCompletionGate_RealVerify_PassesHealthyGoModule is the positive control:
// a module that builds and has no failing tests passes the real verification.
func TestCompletionGate_RealVerify_PassesHealthyGoModule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-build verification in -short mode")
	}
	repoDir := t.TempDir()
	writeGoModule(t, repoDir, "package main\n\nfunc main() {\n\tprintln(\"ok\")\n}\n")
	// README present so the doc gap (medium) is moot; build is the gating signal.
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# gatecheck\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}

	g := NewCompletionGate(nil, "", 0, 0, "main", nil, nil)
	g.pull = func(_, _ string) {}

	if !g.Run(context.Background(), "REQ-REAL-GREEN", repoDir) {
		t.Fatal("expected gate to PASS a composed mainline that builds cleanly")
	}
}

// TestEmitRequirementOutcome_Blocked proves the monitor's terminal-event helper
// drives a real event + projection store: emitting REQ_BLOCKED transitions the
// requirement to "blocked" status (the gate's negative outcome), not "completed".
func TestReqBlockedEvent_ProjectsBlockedStatus(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	defer ps.Close()

	if err := ps.Project(state.NewEvent(state.EventReqSubmitted, "test", "", map[string]any{"id": "REQ-G1", "title": "Gate"})); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}

	emitEventOrLog(es, ps,
		state.NewEvent(state.EventReqBlocked, "monitor", "", map[string]any{"id": "REQ-G1"}))

	req, err := ps.GetRequirement("REQ-G1")
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Status != "blocked" {
		t.Errorf("expected status 'blocked' after REQ_BLOCKED, got %q", req.Status)
	}
}
