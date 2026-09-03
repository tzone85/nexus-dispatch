package engine_test

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Review and QA events used to be the only pipeline events without an
// attempt_id, so a stale review of attempt 1 could be read as attempt 2's.
// ForAttempt stamps them; the plain receiver stays unstamped (legacy callers).

func seedStampStory(t *testing.T, ps state.ProjectionStore) {
	t.Helper()
	if err := ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	})); err != nil {
		t.Fatal(err)
	}
}

func TestReviewer_ForAttempt_StampsEvents(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()
	seedStampStory(t, ps)

	verdicts := map[string]state.EventType{
		`{"passed": true, "comments": [], "summary": "ok"}`:   state.EventStoryReviewPassed,
		`{"passed": false, "comments": [], "summary": "bad"}`: state.EventStoryReviewFailed,
	}
	base := engine.NewReviewer(nil, "ollama", "sonnet", 4000, es, ps)
	for content, want := range verdicts {
		reviewer := engine.NewReviewer(llm.NewReplayClient(llm.CompletionResponse{Content: content}), "ollama", "sonnet", 4000, es, ps)
		if _, err := reviewer.ForAttempt("s-001-a2").Review(context.Background(), "s-001", "t", "ac", "diff --git a/x b/x\n+x"); err != nil {
			t.Fatal(err)
		}
		stamped, _ := es.List(state.EventFilter{Type: want, StoryID: "s-001", AttemptID: "s-001-a2"})
		if len(stamped) != 1 {
			t.Fatalf("%s: expected 1 event stamped with the attempt, got %d", want, len(stamped))
		}
		if got := state.DecodePayload(stamped[0].Payload)["attempt_id"]; got != "s-001-a2" {
			t.Fatalf("%s: payload attempt_id = %v", want, got)
		}
	}
	// ForAttempt is a copy: the base reviewer stays unstamped.
	if base.ForAttempt("x") == base {
		t.Fatal("ForAttempt must return a copy")
	}
	plain := engine.NewReviewer(llm.NewReplayClient(llm.CompletionResponse{Content: `{"passed": true, "comments": [], "summary": "ok"}`}), "ollama", "sonnet", 4000, es, ps)
	if _, err := plain.Review(context.Background(), "s-001", "t", "ac", "diff --git a/x b/x\n+x"); err != nil {
		t.Fatal(err)
	}
	all, _ := es.List(state.EventFilter{Type: state.EventStoryReviewPassed, StoryID: "s-001"})
	var unstamped int
	for _, e := range all {
		if e.AttemptID == "" {
			unstamped++
		}
	}
	if unstamped != 1 {
		t.Fatalf("expected exactly the plain review to be unstamped, got %d of %d", unstamped, len(all))
	}
}

func TestQA_ForAttempt_StampsEvents(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()
	seedStampStory(t, ps)

	runner := &mockRunner{results: map[string]mockRunResult{"go": {output: "ok"}}}
	qa := engine.NewQA(engine.QAConfig{BuildCommand: "go build ./..."}, runner, es, ps)
	if _, err := qa.ForAttempt("s-001-a3").Run(context.Background(), "s-001", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []state.EventType{state.EventStoryQAStarted, state.EventStoryQAPassed} {
		stamped, _ := es.List(state.EventFilter{Type: typ, StoryID: "s-001", AttemptID: "s-001-a3"})
		if len(stamped) != 1 {
			t.Fatalf("%s: expected 1 attempt-stamped event, got %d", typ, len(stamped))
		}
	}

	// A failing check stamps STORY_QA_FAILED too.
	failing := &mockRunner{results: map[string]mockRunResult{"go": {output: "boom", err: context.DeadlineExceeded}}}
	qa2 := engine.NewQA(engine.QAConfig{BuildCommand: "go build ./..."}, failing, es, ps)
	if _, err := qa2.ForAttempt("s-001-a4").Run(context.Background(), "s-001", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if failed, _ := es.List(state.EventFilter{Type: state.EventStoryQAFailed, StoryID: "s-001", AttemptID: "s-001-a4"}); len(failed) != 1 {
		t.Fatalf("expected 1 attempt-stamped STORY_QA_FAILED, got %d", len(failed))
	}

	// The plain runner is unchanged by ForAttempt and emits unstamped events.
	if _, err := qa.Run(context.Background(), "s-001", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	started, _ := es.List(state.EventFilter{Type: state.EventStoryQAStarted, StoryID: "s-001"})
	var unstamped int
	for _, e := range started {
		if e.AttemptID == "" {
			unstamped++
		}
	}
	if unstamped != 1 || len(started) != 3 {
		t.Fatalf("expected 3 STORY_QA_STARTED with exactly 1 unstamped, got %d/%d", unstamped, len(started))
	}
}
