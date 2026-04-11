package engine

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func setupAttemptStore(t *testing.T) state.EventStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestAttemptTracker_SingleSuccess(t *testing.T) {
	es := setupAttemptStore(t)
	storyID := "s1"

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", storyID, nil))

	tracker := NewAttemptTracker(es)
	attempts, err := tracker.ListAttempts(storyID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	if attempts[0].Outcome != "success" {
		t.Errorf("outcome = %q, want success", attempts[0].Outcome)
	}
	if attempts[0].Role != "junior" {
		t.Errorf("role = %q, want junior", attempts[0].Role)
	}
	if attempts[0].Number != 1 {
		t.Errorf("number = %d, want 1", attempts[0].Number)
	}
}

func TestAttemptTracker_RetryAfterQAFailure(t *testing.T) {
	es := setupAttemptStore(t)
	storyID := "s2"

	// Attempt 1: QA fails
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAFailed, "qa", storyID, map[string]any{
		"failed_checks": []string{"test"},
	}))

	// Attempt 2: success
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-2", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", storyID, nil))

	tracker := NewAttemptTracker(es)
	attempts, err := tracker.ListAttempts(storyID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Outcome != "qa_failed" {
		t.Errorf("attempt 1 outcome = %q, want qa_failed", attempts[0].Outcome)
	}
	if attempts[1].Outcome != "success" {
		t.Errorf("attempt 2 outcome = %q, want success", attempts[1].Outcome)
	}
	if attempts[1].Number != 2 {
		t.Errorf("attempt 2 number = %d, want 2", attempts[1].Number)
	}
}

func TestAttemptTracker_EscalationAcrossTiers(t *testing.T) {
	es := setupAttemptStore(t)
	storyID := "s3"

	// Attempt 1: junior, review fail
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-j", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", storyID, map[string]any{
		"reason": "missing error handling",
	}))

	// Attempt 2: senior, success
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-s", storyID, map[string]any{
		"tier": 1, "role": "senior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", storyID, nil))

	tracker := NewAttemptTracker(es)
	attempts, err := tracker.ListAttempts(storyID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Tier != 0 {
		t.Errorf("attempt 1 tier = %d, want 0", attempts[0].Tier)
	}
	if attempts[1].Tier != 1 {
		t.Errorf("attempt 2 tier = %d, want 1", attempts[1].Tier)
	}
	if attempts[0].Error != "missing error handling" {
		t.Errorf("attempt 1 error = %q, want 'missing error handling'", attempts[0].Error)
	}
}

func TestAttemptTracker_InProgress(t *testing.T) {
	es := setupAttemptStore(t)
	storyID := "s4"

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	// No completion event — still running

	tracker := NewAttemptTracker(es)
	attempts, err := tracker.ListAttempts(storyID)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	if attempts[0].Outcome != "in_progress" {
		t.Errorf("outcome = %q, want in_progress", attempts[0].Outcome)
	}
}

func TestAttemptTracker_NoAttempts(t *testing.T) {
	es := setupAttemptStore(t)
	tracker := NewAttemptTracker(es)
	attempts, err := tracker.ListAttempts("nonexistent")
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %d, want 0", len(attempts))
	}
}

func TestAttemptTracker_LastAttempt(t *testing.T) {
	es := setupAttemptStore(t)
	storyID := "s5"

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", storyID, map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAFailed, "qa", storyID, nil))
	es.Append(state.NewEvent(state.EventStoryStarted, "agent-2", storyID, map[string]any{
		"tier": 1, "role": "senior",
	}))
	es.Append(state.NewEvent(state.EventStoryQAPassed, "qa", storyID, nil))

	tracker := NewAttemptTracker(es)
	last, err := tracker.LastAttempt(storyID)
	if err != nil {
		t.Fatalf("LastAttempt: %v", err)
	}
	if last == nil {
		t.Fatal("LastAttempt returned nil")
	}
	if last.Number != 2 {
		t.Errorf("last attempt number = %d, want 2", last.Number)
	}
	if last.Outcome != "success" {
		t.Errorf("last attempt outcome = %q, want success", last.Outcome)
	}
}
