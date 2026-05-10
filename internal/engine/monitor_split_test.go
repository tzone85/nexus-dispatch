package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestEmitSplitFailed_AppendsEvent covers the 0% helper invoked when
// a split action partially completes and aborts. The dashboard's
// recovery panel relies on STORY_SPLIT_FAILED events being emitted —
// silent abort would leave operators with no signal.
func TestEmitSplitFailed_AppendsEvent(t *testing.T) {
	m := minimalMonitor(t)

	m.emitSplitFailed("PARENT-1", "child append failed", []string{"PARENT-1-a"}, errors.New("disk full"))

	evts, _ := m.eventStore.List(state.EventFilter{Type: "STORY_SPLIT_FAILED"})
	if len(evts) != 1 {
		t.Fatalf("expected 1 STORY_SPLIT_FAILED event, got %d", len(evts))
	}
	payload := state.DecodePayload(evts[0].Payload)
	if payload["parent_story_id"] != "PARENT-1" {
		t.Errorf("parent_story_id = %v, want PARENT-1", payload["parent_story_id"])
	}
	if payload["reason"] != "child append failed" {
		t.Errorf("reason = %v, want child append failed", payload["reason"])
	}
	if payload["error"] != "disk full" {
		t.Errorf("error = %v, want 'disk full'", payload["error"])
	}
	createdRaw, ok := payload["created_children"].([]any)
	if !ok || len(createdRaw) != 1 || createdRaw[0] != "PARENT-1-a" {
		t.Errorf("created_children wrong shape: %v", payload["created_children"])
	}
}

// TestEmitSplitFailed_NilCauseOmitsErrorField guards the nil-cause
// branch — the payload should not include an empty 'error' field
// when no underlying error is available (e.g. validation failures
// that produce a reason but no go error).
func TestEmitSplitFailed_NilCauseOmitsErrorField(t *testing.T) {
	m := minimalMonitor(t)

	m.emitSplitFailed("PARENT-2", "validation rejected", nil, nil)

	evts, _ := m.eventStore.List(state.EventFilter{Type: "STORY_SPLIT_FAILED"})
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	payload := state.DecodePayload(evts[0].Payload)
	if _, present := payload["error"]; present {
		t.Errorf("error field should be omitted on nil cause; got %v", payload["error"])
	}
}

// TestCaptureStoryDiff_RealRepoReturnsStat covers the happy path:
// captureStoryDiff runs `git diff main...<branch> --stat` against a
// real tempdir git repo and returns trimmed output. Without this
// test, the function was 0% — every regression in the diff command
// or trim logic would silently produce empty diffs in the executor's
// review prompts.
func TestCaptureStoryDiff_RealRepoReturnsStat(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "--initial-branch=main")
	runGit("config", "user.email", "test@nxd")
	runGit("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-qm", "base")
	runGit("checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\nadded\n"), 0o644); err != nil {
		t.Fatalf("write update: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-qm", "update")

	got := captureStoryDiff(dir, "feature")
	if got == "" {
		t.Fatal("captureStoryDiff returned empty for non-empty diff")
	}
	// `git diff main...feature --stat` always names the changed file
	// in its output. Locking down that contract here.
	if !contains(got, "f.txt") {
		t.Errorf("diff output should mention f.txt, got: %q", got)
	}
}

// TestCaptureStoryDiff_BadBranchReturnsEmpty covers the error-tolerant
// path — git fails (branch doesn't exist) and captureStoryDiff
// returns "" rather than propagating the error. The reviewer prompt
// can tolerate an empty diff but not a panic.
func TestCaptureStoryDiff_BadBranchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		_ = cmd.Run()
	}
	runGit("init", "-q", "--initial-branch=main")

	got := captureStoryDiff(dir, "no-such-branch")
	if got != "" {
		t.Errorf("expected empty diff for missing branch, got %q", got)
	}
}

// TestCaptureStoryDiff_NonRepoReturnsEmpty covers the alternative
// failure mode: dir isn't a git repo at all.
func TestCaptureStoryDiff_NonRepoReturnsEmpty(t *testing.T) {
	got := captureStoryDiff(t.TempDir(), "feature")
	if got != "" {
		t.Errorf("expected empty diff outside a git repo, got %q", got)
	}
}

// TestExecuteSplitAction_NilSplitConfigResetsToDraft covers the guard
// that the manager's "split" verdict carried no actual SplitConfig.
// Without the guard, executeSplitAction would emit STORY_SPLIT with
// zero children — the dispatcher would deadlock waiting for replaced
// stories that never exist.
func TestExecuteSplitAction_NilSplitConfigResetsToDraft(t *testing.T) {
	m := minimalMonitor(t)

	rc := &RunContext{ReqID: "R", DAG: graph.New()}
	m.executeSplitAction(context.Background(), "STORY-NIL-CFG", ManagerAction{
		Diagnosis: "split with no children",
		// SplitConfig deliberately nil
	}, rc, PlannedStory{ID: "STORY-NIL-CFG"})

	// resetStoryToDraft emits STORY_REVIEW_FAILED — that's the
	// observable signal the guard fired.
	failed, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(failed) == 0 {
		t.Error("expected STORY_REVIEW_FAILED reset on nil SplitConfig")
	}
	// And no STORY_SPLIT must have been emitted.
	splits, _ := m.eventStore.List(state.EventFilter{Type: state.EventStorySplit})
	if len(splits) != 0 {
		t.Errorf("expected 0 STORY_SPLIT events, got %d", len(splits))
	}
}

// TestExecuteSplitAction_EmptyChildrenResetsToDraft mirrors the
// nil-config branch but for an empty Children slice — same intent
// (no actual split work to do), same expected reset.
func TestExecuteSplitAction_EmptyChildrenResetsToDraft(t *testing.T) {
	m := minimalMonitor(t)

	rc := &RunContext{ReqID: "R", DAG: graph.New()}
	m.executeSplitAction(context.Background(), "STORY-EMPTY", ManagerAction{
		Diagnosis:   "empty children",
		SplitConfig: &SplitConfig{Children: nil},
	}, rc, PlannedStory{ID: "STORY-EMPTY"})

	failed, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(failed) == 0 {
		t.Error("expected STORY_REVIEW_FAILED reset on empty children")
	}
}

// TestExecuteSplitAction_StoryNotFoundResets covers the projStore
// lookup failure: split was requested on a story the projection
// can't resolve. The handler must reset rather than crash.
func TestExecuteSplitAction_StoryNotFoundResets(t *testing.T) {
	m := minimalMonitor(t)

	rc := &RunContext{ReqID: "R", DAG: graph.New()}
	m.executeSplitAction(context.Background(), "STORY-MISSING", ManagerAction{
		Diagnosis: "split missing story",
		SplitConfig: &SplitConfig{
			Children: []SplitChildConfig{{Suffix: "a", Title: "Child A", Complexity: 1}},
		},
	}, rc, PlannedStory{ID: "STORY-MISSING"})

	failed, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(failed) == 0 {
		t.Error("expected STORY_REVIEW_FAILED reset when story missing in projection")
	}
}

// TestExecuteSplitAction_HappyPathEmitsChildren covers the success
// path: a real story in the projection + valid SplitConfig + DAG
// with the parent → child STORY_CREATED events for each child plus
// STORY_SPLIT for the parent. The DAG also gets mutated.
func TestExecuteSplitAction_HappyPathEmitsChildren(t *testing.T) {
	m := minimalMonitor(t)

	// Seed parent in projection (StoryCreated event projected).
	parentEvt := state.NewEvent(state.EventStoryCreated, "test", "STORY-P", map[string]any{
		"id": "STORY-P", "req_id": "R", "title": "Parent", "complexity": 5,
	})
	if err := m.projStore.Project(parentEvt); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	dag := graph.New()
	dag.AddNode("STORY-P")

	rc := &RunContext{
		ReqID:          "R",
		DAG:            dag,
		PlannedStories: []PlannedStory{{ID: "STORY-P", Complexity: 5}},
	}

	m.executeSplitAction(context.Background(), "STORY-P", ManagerAction{
		Diagnosis: "split parent",
		SplitConfig: &SplitConfig{
			Children: []SplitChildConfig{
				{Suffix: "a", Title: "Child A", Complexity: 2},
				{Suffix: "b", Title: "Child B", Complexity: 2},
			},
		},
	}, rc, PlannedStory{ID: "STORY-P", Complexity: 5})

	// Both child STORY_CREATED events must have landed.
	created, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryCreated})
	if len(created) != 2 {
		t.Errorf("expected 2 STORY_CREATED events for children, got %d", len(created))
	}

	// And the parent's STORY_SPLIT event.
	splits, _ := m.eventStore.List(state.EventFilter{Type: state.EventStorySplit})
	if len(splits) != 1 {
		t.Errorf("expected 1 STORY_SPLIT for parent, got %d", len(splits))
	}

	// DAG mutation: at least the 2 children must be present after
	// the split. (ApplySplit's exact behavior re: parent retention is
	// an implementation detail — only the children-added contract
	// matters here.)
	if rc.DAG.NodeCount() < 2 {
		t.Errorf("DAG should have at least 2 children after split, got %d nodes", rc.DAG.NodeCount())
	}
}

// TestHandleTechLeadEscalation_NoPlannerPausesRequirement covers the
// safety-valve branch: tier-3 escalation hit the monitor but no
// Planner was wired (e.g. dry-run mode or misconfiguration). The
// monitor must pause the requirement rather than crash on a nil
// planner.RePlan call.
func TestHandleTechLeadEscalation_NoPlannerPausesRequirement(t *testing.T) {
	m := minimalMonitor(t)
	m.planner = nil // explicit, even though minimalMonitor leaves it zero

	// Seed a parent requirement so pauseRequirement can find it.
	reqEvt := state.NewEvent(state.EventReqSubmitted, "test", "", map[string]any{
		"id": "REQ-T", "title": "test", "description": "test",
	})
	if err := m.projStore.Project(reqEvt); err != nil {
		t.Fatalf("seed req: %v", err)
	}
	storyEvt := state.NewEvent(state.EventStoryCreated, "test", "STORY-TL", map[string]any{
		"id": "STORY-TL", "req_id": "REQ-T", "title": "Stuck", "complexity": 5,
	})
	if err := m.projStore.Project(storyEvt); err != nil {
		t.Fatalf("seed story: %v", err)
	}

	rc := &RunContext{ReqID: "REQ-T", DAG: graph.New()}
	m.handleTechLeadEscalation(context.Background(), PlannedStory{ID: "STORY-TL"}, "/tmp", rc)

	paused, _ := m.eventStore.List(state.EventFilter{Type: state.EventReqPaused})
	if len(paused) == 0 {
		t.Error("expected REQ_PAUSED when no planner wired for tech-lead escalation")
	}
}

// contains is a small helper avoiding strings.Contains import duplication.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
