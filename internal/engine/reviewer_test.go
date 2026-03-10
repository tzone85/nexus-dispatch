package engine_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestReviewer_Review_Passed(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate story
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"passed": true, "comments": [{"file": "main.go", "line": 10, "severity": "info", "comment": "Consider adding a comment"}], "summary": "Looks good overall"}`,
	})

	reviewer := engine.NewReviewer(client, "sonnet", 4000, es, ps)
	result, err := reviewer.Review(
		context.Background(),
		"s-001",
		"Add user model",
		"User model exists with tests",
		"diff --git a/main.go b/main.go\n+func NewUser() {}",
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !result.Passed {
		t.Fatal("expected review to pass")
	}
	if len(result.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(result.Comments))
	}
	if result.Summary != "Looks good overall" {
		t.Fatalf("unexpected summary: %s", result.Summary)
	}

	// Verify STORY_REVIEW_PASSED event
	events, err := es.List(state.EventFilter{Type: state.EventStoryReviewPassed})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 STORY_REVIEW_PASSED event, got %d", len(events))
	}
}

func TestReviewer_Review_Failed(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"passed": false, "comments": [{"file": "auth.go", "line": 5, "severity": "critical", "comment": "SQL injection vulnerability"}], "summary": "Security issues found"}`,
	})

	reviewer := engine.NewReviewer(client, "sonnet", 4000, es, ps)
	result, err := reviewer.Review(
		context.Background(),
		"s-001",
		"Add login",
		"Login works",
		"diff --git a/auth.go b/auth.go\n+query := \"SELECT * FROM users WHERE name='\" + name",
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed {
		t.Fatal("expected review to fail")
	}

	// Verify STORY_REVIEW_FAILED event
	events, err := es.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 STORY_REVIEW_FAILED event, got %d", len(events))
	}
}

func TestReviewer_Review_EmptyDiff(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	client := llm.NewReplayClient()
	reviewer := engine.NewReviewer(client, "sonnet", 4000, es, ps)

	_, err := reviewer.Review(context.Background(), "s-001", "Task", "AC", "")
	if err == nil {
		t.Fatal("expected error for empty diff")
	}
}

func TestReviewer_Review_LLMError(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	defer ps.Close()

	client := llm.NewReplayClient() // no responses
	reviewer := engine.NewReviewer(client, "sonnet", 4000, es, ps)

	_, err = reviewer.Review(context.Background(), "s-001", "Task", "AC", "some diff")
	if err == nil {
		t.Fatal("expected LLM error")
	}
}
