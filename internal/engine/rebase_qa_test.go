package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// scriptedRunner is a CommandRunner whose result is fixed; it records calls
// so tests can prove QA did (not) run.
type scriptedRunner struct {
	calls  int
	output string
	err    error
}

func (r *scriptedRunner) Run(context.Context, string, string, ...string) (string, error) {
	r.calls++
	return r.output, r.err
}

func newRebaseQAMonitor(t *testing.T, runner CommandRunner) (*Monitor, state.EventStore, state.ProjectionStore) {
	t.Helper()
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-RB", "s-rb")
	cfg := config.DefaultConfig()
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	var qa *QA
	if runner != nil {
		qa = NewQA(QAConfig{TestCommand: "go test ./..."}, runner, es, ps)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, qa, nil, cfg, es, ps)
	return m, es, ps
}

func resolverEvent(storyID string) state.Event {
	return state.NewEvent(state.EventStoryProgress, "conflict-resolver", storyID, map[string]any{
		"action": "conflicts_resolved", "files": []string{"a.go"}, "rounds": 1,
	})
}

func TestConflictsResolvedCount(t *testing.T) {
	es, _ := capacityTestStores(t)
	if n := conflictsResolvedCount(es, "s-1"); n != 0 {
		t.Fatalf("empty store → 0, got %d", n)
	}
	es.Append(state.NewEvent(state.EventStoryProgress, "agent", "s-1", map[string]any{"iteration": 1}))
	es.Append(resolverEvent("s-1"))
	es.Append(resolverEvent("s-other"))
	if n := conflictsResolvedCount(es, "s-1"); n != 1 {
		t.Fatalf("only resolver events for the story count, got %d", n)
	}
}

// TestPostRebaseGate_CleanRebaseSkipsQA: no conflicts were resolved during
// the rebase → the tree is what QA already approved → no re-run.
func TestPostRebaseGate_CleanRebaseSkipsQA(t *testing.T) {
	runner := &scriptedRunner{output: "ok"}
	m, es, _ := newRebaseQAMonitor(t, runner)
	before := conflictsResolvedCount(es, "s-rb")

	if err := m.postRebaseGate(context.Background(), "s-rb", "s-rb-a1", t.TempDir(), before); err != nil {
		t.Fatalf("clean rebase must pass the gate, got %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("QA must not re-run after a clean rebase, ran %d commands", runner.calls)
	}
}

// TestPostRebaseGate_ResolvedConflictsRerunQA_Pass: the resolver rewrote
// files → QA re-runs on the rebased tree; green → merge may proceed.
func TestPostRebaseGate_ResolvedConflictsRerunQA_Pass(t *testing.T) {
	runner := &scriptedRunner{output: "ok"}
	m, es, _ := newRebaseQAMonitor(t, runner)
	before := conflictsResolvedCount(es, "s-rb")
	es.Append(resolverEvent("s-rb")) // what RebaseWithResolution emits

	if err := m.postRebaseGate(context.Background(), "s-rb", "s-rb-a1", t.TempDir(), before); err != nil {
		t.Fatalf("green post-rebase QA must pass the gate, got %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("QA must re-run exactly once after conflict resolution, ran %d", runner.calls)
	}
	stages, _ := es.List(state.EventFilter{Type: state.EventStageCompleted, StoryID: "s-rb"})
	if len(stages) != 1 {
		t.Fatalf("expected 1 STAGE_COMPLETED, got %d", len(stages))
	}
	p := state.DecodePayload(stages[0].Payload)
	if p["stage"] != "qa_post_rebase" || p["outcome"] != "success" {
		t.Fatalf("unexpected stage payload %v", p)
	}
}

// TestPostRebaseGate_ResolvedConflictsRerunQA_Fail: a red rebased tree must
// not merge — the story is reset with QA feedback and the gate errors.
func TestPostRebaseGate_ResolvedConflictsRerunQA_Fail(t *testing.T) {
	runner := &scriptedRunner{output: "FAIL: TestX", err: errors.New("exit 1")}
	m, es, ps := newRebaseQAMonitor(t, runner)
	before := conflictsResolvedCount(es, "s-rb")
	es.Append(resolverEvent("s-rb"))

	err := m.postRebaseGate(context.Background(), "s-rb", "s-rb-a1", t.TempDir(), before)
	if !errors.Is(err, errPostRebaseQA) {
		t.Fatalf("expected errPostRebaseQA, got %v", err)
	}
	qaFailed, _ := es.List(state.EventFilter{Type: state.EventStoryQAFailed, StoryID: "s-rb", AttemptID: "s-rb-a1"})
	if len(qaFailed) == 0 {
		t.Fatal("expected an attempt-stamped STORY_QA_FAILED with feedback")
	}
	var found bool
	for _, e := range qaFailed {
		p := state.DecodePayload(e.Payload)
		if p["source"] == "post_rebase_qa" {
			found = true
			if fb, _ := p["feedback"].(string); fb == "" {
				t.Fatal("feedback must be populated")
			}
		}
	}
	if !found {
		t.Fatal("expected the monitor's post_rebase_qa feedback event")
	}
	reviewFailed, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: "s-rb", AttemptID: "s-rb-a1"})
	if len(reviewFailed) != 1 {
		t.Fatalf("story must be reset to draft (1 STORY_REVIEW_FAILED), got %d", len(reviewFailed))
	}
	story, _ := ps.GetStory("s-rb")
	if story.Status != "draft" {
		t.Fatalf("story status = %q, want draft", story.Status)
	}
}

func TestPostRebaseGate_NoQAConfiguredIsNoop(t *testing.T) {
	m, es, _ := newRebaseQAMonitor(t, nil)
	es.Append(resolverEvent("s-rb"))
	if err := m.postRebaseGate(context.Background(), "s-rb", "", t.TempDir(), 0); err != nil {
		t.Fatalf("no QA wired → nothing to re-run, got %v", err)
	}
}

func TestQAFailureFeedback(t *testing.T) {
	res := QAResult{Checks: []QACheckResult{
		{Name: "build", Passed: true, Output: "ok"},
		{Name: "test", Passed: false, Output: "--- FAIL: TestFoo"},
	}}
	fb := qaFailureFeedback(res, "after rebase")
	if !contains(fb, "[test] --- FAIL: TestFoo") || contains(fb, "[build]") {
		t.Fatalf("feedback must list only failed checks, got:\n%s", fb)
	}
	if !contains(fb, "after rebase") {
		t.Fatalf("feedback must carry the context, got:\n%s", fb)
	}
}
