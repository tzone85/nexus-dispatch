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
	if !strings.Contains(code, "plannedStories = engine.DispatchableStories(stories, plannedStories)") {
		t.Error("resume.go must filter open-PR stories with engine.DispatchableStories before DispatchWave")
	}
	if strings.Contains(code, `story.Status == "pr_submitted"`) {
		t.Error("resume.go must not treat pr_submitted as completed")
	}
}

// TestResume_WiresRoleLLMOptions: every LLM client built by req/resume/plan/
// estimate must carry the role's ModelConfig (google_model, num_ctx,
// fallback_cooldown_s) and models.ollama_host — a provider-name-only call
// silently drops them.
func TestResume_WiresRoleLLMOptions(t *testing.T) {
	for file, wants := range map[string][]string{
		"resume.go":   {"buildLLMClientFor(llmOptsFor(s.Config.Models.Junior, s.Config.Models))", "buildLLMClientFor(llmOptsFor(s.Config.Models.Senior, s.Config.Models), godmode)"},
		"req.go":      {"buildLLMClientFor(llmOptsFor(s.Config.Models.TechLead, s.Config.Models), godmode)"},
		"plan.go":     {"buildLLMClientFor(llmOptsFor(cfg.Models.TechLead, cfg.Models), cfg.Planning.Godmode)"},
		"estimate.go": {"buildLLMClientFor(llmOptsFor(s.Config.Models.TechLead, s.Config.Models))"},
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(src), want) {
				t.Errorf("%s must build its LLM client with role options: missing %q", file, want)
			}
		}
	}
}
