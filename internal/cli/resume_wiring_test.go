package cli

import (
	"os"
	"strings"
	"testing"
)

// TestResume_WiresTechLeadFixer guards against a dead-wire regression: the
// post-merge integration-build feature (WithMonTechLeadFixer + TechLeadFixer)
// was fully implemented and unit-tested, but runResume never wired the fixer
// into the monitor, so the stage never ran in production. The option's own
// wiring test could not catch this. This test scans the resume source to
// confirm the fixer is actually constructed and attached.
func TestResume_WiresTechLeadFixer(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)

	for _, want := range []string{"NewTechLeadFixer(", "WithMonTechLeadFixer("} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the post-merge integration fixer: missing %q", want)
		}
	}
}

// TestResume_WiresSecurityGate guards the per-story security gate against the
// dead-wire class: the gate scans + reviews each story before merge, but only if
// runResume constructs and attaches it.
func TestResume_WiresSecurityGate(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)

	for _, want := range []string{"NewSecurityGate(", "SetSecurityGate("} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the security gate: missing %q", want)
		}
	}
}

// TestResume_WiresNotifier guards the notifications feature against the same
// dead-wire class: the notifier only fires if runResume hooks it onto the
// event store's OnAppend and drains it on exit.
func TestResume_WiresNotifier(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)

	for _, want := range []string{"notify.New(", "notifier.HandleEvent", "notifier.Close()"} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the notifier: missing %q", want)
		}
	}
}

// TestResume_WiresBudgetGuard guards billing.budget_usd enforcement against
// the dead-wire class: the guard only runs if runResume constructs it from
// billing config and attaches it to the monitor.
func TestResume_WiresBudgetGuard(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)

	for _, want := range []string{"NewBudgetGuard(", "SetBudgetGuard("} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the budget guard: missing %q", want)
		}
	}
}

// TestResume_WiresApprovalQueue: the human approval queue only gates the
// pipeline if runResume loads it from the event log, attaches it to the
// monitor, and reconciles rejected approvals before dispatch.
func TestResume_WiresApprovalQueue(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)
	for _, want := range []string{
		"approvals.Load(s.Events)",
		"monitor.SetApprovalQueue(approvalQueue)",
		"engine.ReconcileRejectedApprovals(approvalQueue, s.Events, s.Proj, reqID)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("resume.go must wire the approval queue: missing %q", want)
		}
	}
}

// TestResume_WiresWatchdogAutoApprove: without this the watchdog would
// auto-answer permission prompts of unsandboxed host agents.
func TestResume_WiresWatchdogAutoApprove(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	if !strings.Contains(string(src), "AutoApprovePrompts: s.Config.AutoApprovePrompts") {
		t.Error("resume.go must pass config.AutoApprovePrompts into the WatchdogConfig")
	}
}

// TestResume_WiresReviewerDiffCap: review.max_diff_bytes is dead config unless
// the constructed reviewer receives it.
func TestResume_WiresReviewerDiffCap(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	if !strings.Contains(string(src), "WithMaxDiffBytes(s.Config.Review.MaxDiffBytes)") {
		t.Error("resume.go must apply review.max_diff_bytes to the reviewer")
	}
}

// TestResume_CompletedSetMatchesMonitor: a manual resume must use the same
// "done" rule as the monitor's auto-resume (merged/split only). Counting
// pr_submitted as complete dispatched dependents against a base branch that
// lacked their parent's changes.
func TestResume_CompletedSetMatchesMonitor(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatalf("read resume.go: %v", err)
	}
	code := string(src)
	if !strings.Contains(code, "completed := engine.CompletedStories(stories)") {
		t.Error("resume.go must build the completed set with engine.CompletedStories")
	}
	if strings.Contains(code, `story.Status == "pr_submitted"`) {
		t.Error("resume.go must not treat pr_submitted as completed")
	}
}
