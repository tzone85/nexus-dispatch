package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// approvalFixture is a monitor with a real (file-backed) event store, a
// sqlite projection and an approval queue over the same store, plus one
// seeded requirement/story.
type approvalFixture struct {
	m     *Monitor
	q     *approvals.Queue
	es    state.EventStore
	ps    state.ProjectionStore
	req   string
	story string
}

func newApprovalFixture(t *testing.T, mutate func(*config.Config)) *approvalFixture {
	t.Helper()
	es, ps := newControllerTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-APR", "s-apr")
	cfg := config.DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	q, err := approvals.Load(es)
	if err != nil {
		t.Fatal(err)
	}
	m.SetApprovalQueue(q)
	return &approvalFixture{m: m, q: q, es: es, ps: ps, req: "REQ-APR", story: "s-apr"}
}

func (f *approvalFixture) storyStatus(t *testing.T) string {
	t.Helper()
	st, err := f.ps.GetStory(f.story)
	if err != nil {
		t.Fatal(err)
	}
	return st.Status
}

func (f *approvalFixture) reqStatus(t *testing.T) string {
	t.Helper()
	req, err := f.ps.GetRequirement(f.req)
	if err != nil {
		t.Fatal(err)
	}
	return req.Status
}

func (f *approvalFixture) events(t *testing.T, typ state.EventType) []state.Event {
	t.Helper()
	evts, err := f.es.List(state.EventFilter{Type: typ})
	if err != nil {
		t.Fatal(err)
	}
	return evts
}

func (f *approvalFixture) lastPauseReason(t *testing.T) string {
	t.Helper()
	pauses := f.events(t, state.EventReqPaused)
	if len(pauses) == 0 {
		t.Fatal("expected a REQ_PAUSED event")
	}
	reason, _ := state.DecodePayload(pauses[len(pauses)-1].Payload)["reason"].(string)
	return reason
}

func TestSetApprovalQueue_WiresField(t *testing.T) {
	f := newApprovalFixture(t, nil)
	if f.m.approvals != f.q {
		t.Fatal("SetApprovalQueue must store the queue on the monitor")
	}
	f.m.Configure(WithMonApprovalQueue(nil))
	if f.m.approvals != nil {
		t.Fatal("WithMonApprovalQueue(nil) must clear the queue")
	}
}

func TestPauseOnSecurityFinding(t *testing.T) {
	t.Run("required: records approval and pauses pointing at the queue", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		f.m.pauseOnSecurityFinding(f.story, f.req, "critical: SQL injection")

		pending := f.q.PendingForStory(f.req, f.story, approvals.KindSecurityFinding)
		if len(pending) != 1 {
			t.Fatalf("expected 1 pending security_finding approval, got %d", len(pending))
		}
		if pending[0].Details != "critical: SQL injection" {
			t.Fatalf("details = %q", pending[0].Details)
		}
		if f.reqStatus(t) != "paused" {
			t.Fatalf("requirement status = %q, want paused", f.reqStatus(t))
		}
		reason := f.lastPauseReason(t)
		if !strings.Contains(reason, pending[0].ID) || !strings.Contains(reason, "nxd approvals list") {
			t.Fatalf("pause reason must name the approval and the CLI, got %q", reason)
		}
		// A retried pipeline stage must not duplicate the request.
		f.m.pauseOnSecurityFinding(f.story, f.req, "critical: SQL injection")
		if got := len(f.q.PendingForStory(f.req, f.story, approvals.KindSecurityFinding)); got != 1 {
			t.Fatalf("second call created a duplicate: %d pending", got)
		}
	})

	t.Run("kind not required: pauses without an approval", func(t *testing.T) {
		f := newApprovalFixture(t, func(c *config.Config) { c.Approvals.RequireFor = nil })
		f.m.pauseOnSecurityFinding(f.story, f.req, "high: XSS")
		if got := len(f.q.Pending(f.req)); got != 0 {
			t.Fatalf("expected no approvals, got %d", got)
		}
		if f.reqStatus(t) != "paused" {
			t.Fatal("requirement must still pause")
		}
		if reason := f.lastPauseReason(t); strings.Contains(reason, "approval") {
			t.Fatalf("reason must be the plain security message, got %q", reason)
		}
	})

	t.Run("nil queue: pauses with the plain reason", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		f.m.SetApprovalQueue(nil)
		f.m.pauseOnSecurityFinding(f.story, f.req, "high: XSS")
		if f.reqStatus(t) != "paused" {
			t.Fatal("requirement must pause")
		}
		if got := len(f.events(t, state.EventApprovalRequested)); got != 0 {
			t.Fatalf("nil queue must not record approvals, got %d", got)
		}
	})
}

func TestMergeGate(t *testing.T) {
	t.Run("nil queue never blocks", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		f.m.SetApprovalQueue(nil)
		if f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("no queue → merge proceeds")
		}
	})

	t.Run("no approvals: merge proceeds and merge kind is opt-in", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		if f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("nothing pending → merge proceeds")
		}
		if got := len(f.q.All(f.req)); got != 0 {
			t.Fatalf("merge kind is not required by default, got %d items", got)
		}
	})

	t.Run("pending blocks: merge_ready + pause", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		it, asked, err := RequestConflictApproval(f.q, f.req, f.story, []string{"main.go"})
		if err != nil || !asked {
			t.Fatalf("request: asked=%v err=%v", asked, err)
		}
		if !f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("pending approval must block the merge")
		}
		if f.storyStatus(t) != "merge_ready" {
			t.Fatalf("story status = %q, want merge_ready", f.storyStatus(t))
		}
		ready := f.events(t, state.EventStoryMergeReady)
		if len(ready) != 1 || ready[0].AttemptID != "a1" {
			t.Fatalf("expected one attempt-stamped STORY_MERGE_READY, got %+v", ready)
		}
		if f.reqStatus(t) != "paused" {
			t.Fatal("requirement must pause while the approval is pending")
		}
		reason := f.lastPauseReason(t)
		if !strings.Contains(reason, it.ID) || !strings.Contains(reason, "nxd merge s-apr") {
			t.Fatalf("reason must name the item and the follow-up command, got %q", reason)
		}
	})

	t.Run("approved proceeds", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		it, _, _ := RequestConflictApproval(f.q, f.req, f.story, []string{"main.go"})
		if _, err := f.q.Resolve(it.ID, approvals.StatusApproved, "alice", ""); err != nil {
			t.Fatal(err)
		}
		if f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("approved item must not block")
		}
		if got := len(f.events(t, state.EventReqPaused)); got != 0 {
			t.Fatalf("approved must not pause, got %d REQ_PAUSED", got)
		}
	})

	t.Run("rejected resets to draft once", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		it, _, _ := RequestConflictApproval(f.q, f.req, f.story, []string{"main.go"})
		if _, err := f.q.Resolve(it.ID, approvals.StatusRejected, "bob", "keep theirs"); err != nil {
			t.Fatal(err)
		}
		if !f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("rejected approval must stop the merge")
		}
		if f.storyStatus(t) != "draft" {
			t.Fatalf("story status = %q, want draft", f.storyStatus(t))
		}
		failed := f.events(t, state.EventStoryReviewFailed)
		if len(failed) != 1 {
			t.Fatalf("expected 1 STORY_REVIEW_FAILED, got %d", len(failed))
		}
		reason, _ := state.DecodePayload(failed[0].Payload)["reason"].(string)
		if !strings.Contains(reason, it.ID) || !strings.Contains(reason, "bob") || !strings.Contains(reason, "keep theirs") {
			t.Fatalf("reset feedback must carry id, decider and note, got %q", reason)
		}
		if got := len(f.events(t, state.EventReqPaused)); got != 0 {
			t.Fatalf("rejection resets, it must not pause; got %d REQ_PAUSED", got)
		}
		// The next attempt of the same story must not be reset again for the
		// same (already handled) decision.
		if f.m.mergeGate(f.story, "a2", f.req, "nxd/s-apr") {
			t.Fatal("a handled rejection must not block the next attempt")
		}
		if got := len(f.events(t, state.EventStoryReviewFailed)); got != 1 {
			t.Fatalf("handled rejection reset the story again: %d STORY_REVIEW_FAILED", got)
		}
	})

	t.Run("merge kind required: every merge asks first", func(t *testing.T) {
		f := newApprovalFixture(t, func(c *config.Config) {
			c.Approvals.RequireFor = append(c.Approvals.RequireFor, string(approvals.KindMerge))
		})
		if !f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("merge approval must block until decided")
		}
		pending := f.q.PendingForStory(f.req, f.story, approvals.KindMerge)
		if len(pending) != 1 || !strings.Contains(pending[0].Summary, "nxd/s-apr") {
			t.Fatalf("expected one merge approval naming the branch, got %+v", pending)
		}
		if _, err := f.q.Resolve(pending[0].ID, approvals.StatusApproved, "alice", ""); err != nil {
			t.Fatal(err)
		}
		if f.m.mergeGate(f.story, "a1", f.req, "nxd/s-apr") {
			t.Fatal("approved merge must proceed")
		}
		if got := len(f.q.PendingForStory(f.req, f.story, approvals.KindMerge)); got != 0 {
			t.Fatalf("approved merge must not be re-requested, got %d pending", got)
		}
		// A reset after the decision starts a new attempt → ask again.
		f.m.resetStoryToDraftFor(f.story, "a1", "test", "retry")
		if !f.m.mergeGate(f.story, "a2", f.req, "nxd/s-apr") {
			t.Fatal("a new attempt needs its own merge approval")
		}
		if got := len(f.q.PendingForStory(f.req, f.story, approvals.KindMerge)); got != 1 {
			t.Fatalf("expected a fresh merge approval, got %d pending", got)
		}
	})
}

func TestReconcileRejectedApprovals(t *testing.T) {
	t.Run("nil queue", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		if got := ReconcileRejectedApprovals(nil, f.es, f.ps, f.req); got != nil {
			t.Fatalf("nil queue → nil, got %v", got)
		}
	})

	t.Run("awaiting-merge story with a rejection is reset", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		qaPassed := state.NewEvent(state.EventStoryQAPassed, "monitor", f.story, nil)
		if err := f.ps.Project(qaPassed); err != nil {
			t.Fatal(err)
		}
		if f.storyStatus(t) != "pr_submitted" {
			t.Fatalf("precondition: status = %q", f.storyStatus(t))
		}
		it, _, _ := RequestIntegrationApproval(f.q, f.req, f.story, "build broke")
		if _, err := f.q.Resolve(it.ID, approvals.StatusRejected, "bob", ""); err != nil {
			t.Fatal(err)
		}

		reset := ReconcileRejectedApprovals(f.q, f.es, f.ps, f.req)
		if len(reset) != 1 || reset[0] != f.story {
			t.Fatalf("reset = %v, want [%s]", reset, f.story)
		}
		if f.storyStatus(t) != "draft" {
			t.Fatalf("status = %q, want draft", f.storyStatus(t))
		}
		resets := f.events(t, state.EventStoryReset)
		if len(resets) != 1 || state.DecodePayload(resets[0].Payload)["approval_id"] != it.ID {
			t.Fatalf("expected one STORY_RESET carrying the approval id, got %+v", resets)
		}
		// Idempotent: the reset marks the rejection handled.
		if again := ReconcileRejectedApprovals(f.q, f.es, f.ps, f.req); len(again) != 0 {
			t.Fatalf("second reconcile reset %v again", again)
		}
	})

	t.Run("pending or approved items and non-merge statuses are untouched", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		// Story is draft (not awaiting merge) with a rejection → untouched.
		it, _, _ := RequestIntegrationApproval(f.q, f.req, f.story, "build broke")
		if _, err := f.q.Resolve(it.ID, approvals.StatusRejected, "bob", ""); err != nil {
			t.Fatal(err)
		}
		if got := ReconcileRejectedApprovals(f.q, f.es, f.ps, f.req); len(got) != 0 {
			t.Fatalf("draft story must not be reset, got %v", got)
		}
		// Awaiting merge but only pending → untouched.
		f2 := newApprovalFixture(t, nil)
		if err := f2.ps.Project(state.NewEvent(state.EventStoryQAPassed, "monitor", f2.story, nil)); err != nil {
			t.Fatal(err)
		}
		RequestIntegrationApproval(f2.q, f2.req, f2.story, "build broke")
		if got := ReconcileRejectedApprovals(f2.q, f2.es, f2.ps, f2.req); len(got) != 0 {
			t.Fatalf("pending approval must not reset, got %v", got)
		}
		if f2.storyStatus(t) != "pr_submitted" {
			t.Fatalf("status changed to %q", f2.storyStatus(t))
		}
	})
}

func TestHandleMergeFailure(t *testing.T) {
	tooLarge := fmt.Errorf("rebase onto main: %w", fmt.Errorf("resolve big.go: %w",
		&ConflictTooLargeError{File: "big.go", Size: 999999, Limit: 1000}))
	escalated := fmt.Errorf("rebase onto main: %w", fmt.Errorf("resolve a.go: %w",
		&ConflictEscalatedError{File: "a.go", Reason: "output failed validation"}))

	t.Run("post-rebase QA already handled", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, fmt.Errorf("wrap: %w", errPostRebaseQA)); got.String() != "failed" {
			t.Fatalf("outcome = %s, want failed", got)
		}
		if n := len(f.events(t, state.EventStoryReviewFailed)) + len(f.events(t, state.EventReqPaused)); n != 0 {
			t.Fatalf("errPostRebaseQA must emit nothing more, got %d events", n)
		}
	})

	t.Run("capacity error pauses without approval", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		capErr := fmt.Errorf(`ollama API error (status 503): {"error":"server overloaded, please retry shortly"}`)
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, capErr); got.String() != "paused" {
			t.Fatalf("outcome = %s, want paused", got)
		}
		if f.reqStatus(t) != "paused" || len(f.q.All(f.req)) != 0 {
			t.Fatal("capacity error must pause and not record an approval")
		}
	})

	t.Run("fatal API error pauses even when wrapped in an escalation", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		fatal := &ConflictEscalatedError{File: "a.go", Reason: "auth", Err: &llm.APIError{StatusCode: 401, Provider: "anthropic"}}
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, fmt.Errorf("resolve: %w", fatal)); got.String() != "paused" {
			t.Fatalf("outcome = %s, want paused", got)
		}
		if !strings.Contains(f.lastPauseReason(t), "fatal API error") {
			t.Fatalf("reason = %q", f.lastPauseReason(t))
		}
		if len(f.q.All(f.req)) != 0 {
			t.Fatal("a fatal API error is not a conflict decision — no approval")
		}
	})

	for name, err := range map[string]error{"too large": tooLarge, "escalated": escalated} {
		t.Run("conflict "+name+" → approval + pause", func(t *testing.T) {
			f := newApprovalFixture(t, nil)
			if got := f.m.handleMergeFailure(f.story, "a1", f.req, err); got.String() != "paused" {
				t.Fatalf("outcome = %s, want paused", got)
			}
			pending := f.q.PendingForStory(f.req, f.story, approvals.KindConflictResolution)
			if len(pending) != 1 {
				t.Fatalf("expected 1 conflict_resolution approval, got %d", len(pending))
			}
			if !strings.Contains(pending[0].Summary, ".go") {
				t.Fatalf("summary must name the file, got %q", pending[0].Summary)
			}
			reason := f.lastPauseReason(t)
			if !strings.Contains(reason, pending[0].ID) || !strings.Contains(reason, "nxd approvals list") {
				t.Fatalf("reason must point at the queue, got %q", reason)
			}
			if got := len(f.events(t, state.EventStoryReviewFailed)); got != 0 {
				t.Fatalf("escalated conflict must not be retried, got %d STORY_REVIEW_FAILED", got)
			}
		})
	}

	t.Run("conflict escalation without the approval kind falls back to reset", func(t *testing.T) {
		f := newApprovalFixture(t, func(c *config.Config) { c.Approvals.RequireFor = []string{"security_finding"} })
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, tooLarge); got.String() != "failed" {
			t.Fatalf("outcome = %s, want failed", got)
		}
		if f.storyStatus(t) != "draft" || len(f.q.All(f.req)) != 0 {
			t.Fatal("must reset to draft without recording an approval")
		}
	})

	t.Run("conflict escalation with nil queue still pauses", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		f.m.SetApprovalQueue(nil)
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, escalated); got.String() != "paused" {
			t.Fatalf("outcome = %s, want paused", got)
		}
		if !strings.Contains(f.lastPauseReason(t), "nxd approvals list") {
			t.Fatalf("reason = %q", f.lastPauseReason(t))
		}
	})

	t.Run("plain error resets with feedback", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		if got := f.m.handleMergeFailure(f.story, "a1", f.req, errors.New("push rejected")); got.String() != "failed" {
			t.Fatalf("outcome = %s, want failed", got)
		}
		failed := f.events(t, state.EventStoryReviewFailed)
		if len(failed) != 1 || failed[0].AttemptID != "a1" {
			t.Fatalf("expected one attempt-stamped STORY_REVIEW_FAILED, got %+v", failed)
		}
		if reason, _ := state.DecodePayload(failed[0].Payload)["reason"].(string); !strings.Contains(reason, "push rejected") {
			t.Fatalf("reason = %q", reason)
		}
	})
}

func TestCheckIntegration_RecordsApproval(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		f := newApprovalFixture(t, nil)
		client := &recordingFixClient{called: make(chan struct{})}
		f.m.Configure(WithMonTechLeadFixer(NewTechLeadFixer(client, "model", 256, f.es, f.ps)))
		f.m.integrationBuild = func(string) error { return errors.New("main.go:3: undefined: Foo") }

		if !f.m.checkIntegration(context.Background(), f.story, "a1", t.TempDir()) {
			t.Fatal("red mainline must pause")
		}
		<-client.called
		pending := f.q.PendingForStory(f.req, f.story, approvals.KindIntegrationFailure)
		if len(pending) != 1 || pending[0].Details != "main.go:3: undefined: Foo" {
			t.Fatalf("expected one integration_failure approval with the build error, got %+v", pending)
		}
		reason := f.lastPauseReason(t)
		if !strings.Contains(reason, pending[0].ID) || !strings.Contains(reason, "nxd approvals list") {
			t.Fatalf("reason must point at the queue, got %q", reason)
		}
	})

	t.Run("not required", func(t *testing.T) {
		f := newApprovalFixture(t, func(c *config.Config) { c.Approvals.RequireFor = nil })
		client := &recordingFixClient{called: make(chan struct{})}
		f.m.Configure(WithMonTechLeadFixer(NewTechLeadFixer(client, "model", 256, f.es, f.ps)))
		f.m.integrationBuild = func(string) error { return errors.New("boom") }
		if !f.m.checkIntegration(context.Background(), f.story, "a1", t.TempDir()) {
			t.Fatal("red mainline must pause")
		}
		<-client.called
		if got := len(f.q.All(f.req)); got != 0 {
			t.Fatalf("expected no approvals, got %d", got)
		}
		if !strings.Contains(f.lastPauseReason(t), "fix the base branch") {
			t.Fatalf("reason = %q", f.lastPauseReason(t))
		}
	})
}
