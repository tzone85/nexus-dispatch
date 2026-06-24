package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// capacityTestStores builds in-memory event + projection stores for the
// capacity-pause unit tests.
func capacityTestStores(t *testing.T) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	es, err := state.NewFileStore(t.TempDir() + "/events.jsonl")
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	t.Cleanup(func() {
		es.Close()
		ps.Close()
	})
	return es, ps
}

func seedCapacityStory(t *testing.T, es state.EventStore, ps state.ProjectionStore, reqID, storyID string) {
	t.Helper()
	reqEvt := state.NewEvent(state.EventReqSubmitted, "cli", "", map[string]any{
		"id": reqID, "title": "Req", "description": "desc",
	})
	if err := es.Append(reqEvt); err != nil {
		t.Fatal(err)
	}
	if err := ps.Project(reqEvt); err != nil {
		t.Fatal(err)
	}
	storyEvt := state.NewEvent(state.EventStoryCreated, "tl", storyID, map[string]any{
		"id": storyID, "req_id": reqID, "title": "Task", "description": "d", "complexity": 3,
	})
	if err := es.Append(storyEvt); err != nil {
		t.Fatal(err)
	}
	if err := ps.Project(storyEvt); err != nil {
		t.Fatal(err)
	}
}

// pauseIfCapacity must pause the requirement WITHOUT emitting any escalation /
// review-failed event when the error is a transient capacity exhaustion, and
// must return false (leaving the caller's normal handling to run) otherwise.
func TestPauseIfCapacity(t *testing.T) {
	t.Run("capacity error pauses cleanly", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		seedCapacityStory(t, es, ps, "r-cap", "s-cap")
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)

		capErr := fmt.Errorf(`ollama API error (status 503): {"error":"server overloaded, please retry shortly"}`)
		if !m.pauseIfCapacity("s-cap", "review", capErr) {
			t.Fatal("pauseIfCapacity returned false for a capacity error")
		}

		paused, _ := es.List(state.EventFilter{Type: state.EventReqPaused})
		if len(paused) != 1 {
			t.Errorf("expected 1 REQ_PAUSED, got %d", len(paused))
		}
		// Must NOT burn an escalation tier.
		failed, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: "s-cap"})
		if len(failed) != 0 {
			t.Errorf("capacity pause must not emit STORY_REVIEW_FAILED; got %d", len(failed))
		}
		esc, _ := es.List(state.EventFilter{Type: state.EventStoryEscalated, StoryID: "s-cap"})
		if len(esc) != 0 {
			t.Errorf("capacity pause must not emit STORY_ESCALATED; got %d", len(esc))
		}
	})

	t.Run("ordinary error returns false", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		seedCapacityStory(t, es, ps, "r-ord", "s-ord")
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)

		if m.pauseIfCapacity("s-ord", "review", fmt.Errorf("undefined: Foo")) {
			t.Fatal("pauseIfCapacity returned true for an ordinary error")
		}
		paused, _ := es.List(state.EventFilter{Type: state.EventReqPaused})
		if len(paused) != 0 {
			t.Errorf("ordinary error must not pause; got %d REQ_PAUSED", len(paused))
		}
	})

	t.Run("nil error returns false", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		seedCapacityStory(t, es, ps, "r-nil", "s-nil")
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		if m.pauseIfCapacity("s-nil", "review", nil) {
			t.Fatal("pauseIfCapacity returned true for a nil error")
		}
	})
}

// agentCompletionHasCapacityError scans the latest STORY_COMPLETED event's
// recorded error envelope. A native (Gemma) agent that hit an Ollama overload
// still emits STORY_COMPLETED (with the error in the payload) and produces an
// empty diff — without this scan it would look identical to a lazy agent and be
// wrongly escalated as "produced no code changes".
func TestAgentCompletionHasCapacityError(t *testing.T) {
	t.Run("detects capacity error in completion payload", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		evt := state.NewEvent(state.EventStoryCompleted, "agent-1", "s-1", map[string]any{
			"native": true,
			"error":  "llm completion (iteration 2): ollama API error (status 503): server busy, please try again",
		})
		es.Append(evt)
		ps.Project(evt)

		if !m.agentCompletionHasCapacityError("s-1") {
			t.Error("expected capacity error to be detected in completion payload")
		}
	})

	t.Run("ignores ordinary completion error", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		evt := state.NewEvent(state.EventStoryCompleted, "agent-1", "s-2", map[string]any{
			"native": true,
			"error":  "llm completion (iteration 1): undefined: Foo",
		})
		es.Append(evt)
		ps.Project(evt)

		if m.agentCompletionHasCapacityError("s-2") {
			t.Error("ordinary completion error must not be detected as capacity")
		}
	})

	t.Run("no completion event", func(t *testing.T) {
		es, ps := capacityTestStores(t)
		m := NewMonitor(nil, nil, nil, nil, nil, config.Config{}, es, ps)
		if m.agentCompletionHasCapacityError("s-missing") {
			t.Error("missing completion event must not be detected as capacity")
		}
	})
}

// makeWorktreeWithDiff builds a git repo with a committed change on a feature
// branch so the post-execution pipeline sees a non-empty diff and proceeds to
// the review stage.
func makeWorktreeWithDiff(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	if err := os.WriteFile(dir+"/base.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("checkout", "-b", "nxd/s-pe-cap")
	if err := os.WriteFile(dir+"/feature.txt", []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "feature")
	return dir
}

// makeEmptyWorktree builds a git repo with a feature branch identical to main
// (no diff), simulating a native agent that produced nothing.
func makeEmptyWorktree(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	if err := os.WriteFile(dir+"/base.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("checkout", "-b", branch)
	return dir
}

// TestPostExecutionPipeline_EmptyDiffCapacity verifies that when a native agent
// produces no diff AND its STORY_COMPLETED payload records an Ollama capacity
// error, the requirement pauses cleanly instead of resetting to draft (which
// would burn an escalation attempt as "produced no code changes").
func TestPostExecutionPipeline_EmptyDiffCapacity(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "r-empty", "s-empty")

	// Native agent emitted STORY_COMPLETED carrying an Ollama overload error.
	completed := state.NewEvent(state.EventStoryCompleted, "agent-1", "s-empty", map[string]any{
		"native": true,
		"error":  "llm completion (iteration 3): ollama API error (status 503): server busy, please try again. maximum pending requests exceeded",
	})
	es.Append(completed)
	ps.Project(completed)

	worktree := makeEmptyWorktree(t, "nxd/s-empty")
	m := NewMonitor(nil, nil, nil, nil, nil, config.Config{
		Routing: config.RoutingConfig{MaxRetriesBeforeEscalation: 2},
	}, es, ps)

	ag := ActiveAgent{
		Assignment: Assignment{
			StoryID: "s-empty", AgentID: "agent-1",
			SessionName: "nxd-test-empty", Branch: "nxd/s-empty",
		},
		WorktreePath: worktree,
	}

	m.postExecutionPipeline(context.Background(), ag, worktree)

	paused, _ := es.List(state.EventFilter{Type: state.EventReqPaused})
	if len(paused) < 1 {
		t.Error("expected REQ_PAUSED for empty-diff capacity error")
	}
	failed, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: "s-empty"})
	if len(failed) != 0 {
		t.Errorf("empty-diff capacity must not emit STORY_REVIEW_FAILED; got %d", len(failed))
	}
}

// TestPostExecutionPipeline_ReviewCapacity verifies a capacity error during
// review PAUSES the requirement (resume after the server recovers) rather than
// resetting to draft — which would burn an escalation attempt on a transient
// overload the story never had a chance to avoid.
func TestPostExecutionPipeline_ReviewCapacity(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "r-cap", "s-pe-cap")

	worktree := makeWorktreeWithDiff(t)

	capErr := fmt.Errorf(`reviewer LLM call: ollama API error (status 503): {"error":"server overloaded, please retry shortly"}`)
	reviewer := NewReviewer(llm.NewErrorClient(capErr), "ollama", "gemma4", 4000, es, ps)

	cfg := config.Config{Routing: config.RoutingConfig{MaxRetriesBeforeEscalation: 2}}
	m := NewMonitor(nil, nil, reviewer, nil, nil, cfg, es, ps)

	ag := ActiveAgent{
		Assignment: Assignment{
			StoryID: "s-pe-cap", AgentID: "agent-1",
			SessionName: "nxd-test-cap", Branch: "nxd/s-pe-cap",
		},
		WorktreePath: worktree,
	}

	m.postExecutionPipeline(context.Background(), ag, worktree)

	paused, _ := es.List(state.EventFilter{Type: state.EventReqPaused})
	if len(paused) < 1 {
		t.Error("expected REQ_PAUSED for capacity error during review")
	}
	failed, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: "s-pe-cap"})
	if len(failed) != 0 {
		t.Errorf("capacity error must not emit STORY_REVIEW_FAILED (burns escalation); got %d", len(failed))
	}
}
