package engine

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestLastAttempt_NoAttempts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	es, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	tracker := NewAttemptTracker(es)
	last, err := tracker.LastAttempt("s-nonexistent")
	if err != nil {
		t.Fatalf("LastAttempt: %v", err)
	}
	if last != nil {
		t.Errorf("expected nil for no attempts, got %+v", last)
	}
}

func TestLastAttempt_SingleAttempt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	es, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", "s-001", map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", "s-001", nil))

	tracker := NewAttemptTracker(es)
	last, err := tracker.LastAttempt("s-001")
	if err != nil {
		t.Fatalf("LastAttempt: %v", err)
	}
	if last == nil {
		t.Fatal("expected non-nil last attempt")
	}
	if last.Number != 1 {
		t.Errorf("expected attempt 1, got %d", last.Number)
	}
	if last.Outcome != "success" {
		t.Errorf("expected success outcome, got %q", last.Outcome)
	}
}

func TestLastAttempt_MultipleAttempts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	es, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	// Attempt 1: fail
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", "s-001", map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-001", map[string]any{
		"summary": "bad code",
	}))

	// Attempt 2: success
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-2", "s-001", map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", "s-001", nil))

	tracker := NewAttemptTracker(es)
	last, err := tracker.LastAttempt("s-001")
	if err != nil {
		t.Fatalf("LastAttempt: %v", err)
	}
	if last == nil {
		t.Fatal("expected non-nil last attempt")
	}
	if last.Number != 2 {
		t.Errorf("expected attempt 2, got %d", last.Number)
	}
	if last.Role != "junior" {
		t.Errorf("expected junior role, got %q", last.Role)
	}
}
