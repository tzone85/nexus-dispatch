package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestApprovalsCmd_Registers(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "approvals" {
			found = true
			names := map[string]bool{}
			for _, sub := range c.Commands() {
				names[sub.Name()] = true
			}
			for _, want := range []string{"list", "approve", "reject"} {
				if !names[want] {
					t.Errorf("approvals is missing subcommand %s", want)
				}
			}
		}
	}
	if !found {
		t.Fatal("approvals command not registered on root")
	}
	// The plan-approval command is untouched.
	if newApproveCmd().Use != "approve <req-id>" {
		t.Errorf("nxd approve changed: %q", newApproveCmd().Use)
	}
}

// execApprovals runs `nxd approvals <args>` with --config pointing at cfgPath.
// The flag is persistent on root in production; tests attach it to the
// approvals command so subcommands inherit it the same way.
func execApprovals(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	cmd := newApprovalsCmd()
	cmd.PersistentFlags().String("config", "", "")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func seedApproval(t *testing.T, env *testEnv, reqID, storyID string, kind approvals.Kind, summary string) approvals.Item {
	t.Helper()
	q, err := approvals.Load(env.Events)
	if err != nil {
		t.Fatal(err)
	}
	it, err := q.Request(reqID, storyID, kind, summary, "details")
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestApprovalsList_EmptyAndTable(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execApprovals(t, env.Config, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No approvals found.") {
		t.Errorf("out = %q", out)
	}

	a := seedApproval(t, env, "req-1", "s1", approvals.KindConflictResolution, "conflict in a.go")
	b := seedApproval(t, env, "req-2", "s2", approvals.KindSecurityFinding, "gitleaks hit")

	out, err = execApprovals(t, env.Config, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"Approvals (2)", a.ID, b.ID, "conflict_resolution", "security_finding", "conflict in a.go", "nxd approvals approve|reject"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}

	out, err = execApprovals(t, env.Config, "list", "--req", "req-2")
	if err != nil {
		t.Fatalf("list --req: %v", err)
	}
	if strings.Contains(out, a.ID) || !strings.Contains(out, b.ID) {
		t.Errorf("--req filter failed:\n%s", out)
	}
}

func TestApprovalsList_JSON(t *testing.T) {
	env := setupTestEnv(t)
	out, err := execApprovals(t, env.Config, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty JSON = %q", out)
	}
	it := seedApproval(t, env, "req-1", "s1", approvals.KindMerge, "merge me")
	out, err = execApprovals(t, env.Config, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var items []approvals.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(items) != 1 || items[0].ID != it.ID || items[0].Kind != approvals.KindMerge || items[0].Status != approvals.StatusPending {
		t.Errorf("items = %+v", items)
	}
}

func TestApprovalsApproveAndReject(t *testing.T) {
	env := setupTestEnv(t)
	t.Setenv("NXD_USER", "thando")
	a := seedApproval(t, env, "req-1", "s1", approvals.KindIntegrationFailure, "build red")
	b := seedApproval(t, env, "req-1", "s2", approvals.KindSecurityFinding, "finding")

	out, err := execApprovals(t, env.Config, "approve", a.ID, "--note", "looks fine")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !strings.Contains(out, "Approved "+a.ID) || !strings.Contains(out, "nxd resume req-1") {
		t.Errorf("approve output = %q", out)
	}
	out, err = execApprovals(t, env.Config, "reject", b.ID)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if !strings.Contains(out, "Rejected "+b.ID) {
		t.Errorf("reject output = %q", out)
	}

	// Decisions are persisted as events and visible to a fresh queue.
	q, err := approvals.Load(env.Events)
	if err != nil {
		t.Fatal(err)
	}
	gotA, _ := q.Get(a.ID)
	gotB, _ := q.Get(b.ID)
	if gotA.Status != approvals.StatusApproved || gotA.DecidedBy != "thando" || gotA.Note != "looks fine" {
		t.Errorf("a = %+v", gotA)
	}
	if gotB.Status != approvals.StatusRejected || gotB.DecidedBy != "thando" {
		t.Errorf("b = %+v", gotB)
	}
	resolved, _ := env.Events.List(state.EventFilter{Type: state.EventApprovalResolved})
	if len(resolved) != 2 {
		t.Errorf("APPROVAL_RESOLVED events = %d, want 2", len(resolved))
	}

	// --all shows resolved items with their notes; default hides them.
	out, _ = execApprovals(t, env.Config, "list")
	if !strings.Contains(out, "No approvals found.") {
		t.Errorf("resolved items must not be listed by default:\n%s", out)
	}
	out, _ = execApprovals(t, env.Config, "list", "--all")
	if !strings.Contains(out, "approved") || !strings.Contains(out, "rejected") || !strings.Contains(out, "note: looks fine (thando)") {
		t.Errorf("--all output:\n%s", out)
	}
}

func TestApprovalsDecide_Errors(t *testing.T) {
	env := setupTestEnv(t)
	if _, err := execApprovals(t, env.Config, "approve", "does-not-exist"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing item: %v", err)
	}
	a := seedApproval(t, env, "req-1", "s1", approvals.KindMerge, "m")
	if _, err := execApprovals(t, env.Config, "approve", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := execApprovals(t, env.Config, "reject", a.ID); err == nil || !strings.Contains(err.Error(), "already resolved") {
		t.Errorf("double decision: %v", err)
	}
	if _, err := execApprovals(t, "/nonexistent/nxd.yaml", "list"); err == nil {
		t.Error("bad config path must error")
	}
	if _, err := execApprovals(t, "/nonexistent/nxd.yaml", "approve", "x"); err == nil {
		t.Error("bad config path must error")
	}
}

func TestRenderApprovals_JSONNilBecomesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	if err := renderApprovals(&buf, nil, true); err != nil || strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("out=%q err=%v", buf.String(), err)
	}
	buf.Reset()
	_ = renderApprovals(&buf, []approvals.Item{{ID: "01X", Kind: approvals.KindMerge, Status: approvals.StatusPending, ReqID: "r", Summary: "s"}}, false)
	if !strings.Contains(buf.String(), "01X") || !strings.Contains(buf.String(), " -  ") {
		t.Errorf("empty story must render as -:\n%s", buf.String())
	}
}

func TestCurrentUser(t *testing.T) {
	t.Setenv("NXD_USER", "  ops  ")
	if got := currentUser(); got != "ops" {
		t.Errorf("NXD_USER = %q", got)
	}
	os.Unsetenv("NXD_USER")
	if got := currentUser(); got == "" {
		t.Error("fallback must not be empty")
	}
}
