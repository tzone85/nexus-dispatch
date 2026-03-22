package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestNewEvent(t *testing.T) {
	evt := state.NewEvent(state.EventStoryCreated, "agent-1", "story-1", map[string]any{
		"title":      "Add auth",
		"complexity": 5,
	})

	if evt.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if evt.Type != state.EventStoryCreated {
		t.Fatalf("expected type %s, got %s", state.EventStoryCreated, evt.Type)
	}
	if evt.AgentID != "agent-1" {
		t.Fatalf("expected agent-1, got %s", evt.AgentID)
	}
	if evt.StoryID != "story-1" {
		t.Fatalf("expected story-1, got %s", evt.StoryID)
	}
	if time.Since(evt.Timestamp) > time.Second {
		t.Fatal("timestamp should be recent")
	}

	var payload map[string]any
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["title"] != "Add auth" {
		t.Fatalf("expected title 'Add auth', got %v", payload["title"])
	}
}

func TestEventTypeConstants(t *testing.T) {
	types := []state.EventType{
		state.EventReqSubmitted,
		state.EventReqAnalyzed,
		state.EventReqPlanned,
		state.EventReqCompleted,
		state.EventStoryCreated,
		state.EventStoryEstimated,
		state.EventStoryAssigned,
		state.EventStoryStarted,
		state.EventStoryProgress,
		state.EventStoryCompleted,
		state.EventStoryReviewRequested,
		state.EventStoryReviewPassed,
		state.EventStoryReviewFailed,
		state.EventStoryQAStarted,
		state.EventStoryQAPassed,
		state.EventStoryPRCreated,
		state.EventStoryMerged,
		state.EventStoryEscalated,
		state.EventStoryRewritten,
		state.EventStorySplit,
		state.EventAgentSpawned,
		state.EventAgentCheckpoint,
		state.EventAgentResumed,
		state.EventAgentStuck,
		state.EventAgentTerminated,
		state.EventSupervisorCheck,
		state.EventSupervisorReprioritize,
		state.EventSupervisorDriftDetected,
		state.EventWorktreePruned,
		state.EventBranchDeleted,
		state.EventGCCompleted,
	}
	seen := make(map[state.EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Fatalf("duplicate event type: %s", et)
		}
		seen[et] = true
		if et == "" {
			t.Fatal("empty event type")
		}
	}
}

func TestNewEvent_NilPayload(t *testing.T) {
	evt := state.NewEvent(state.EventReqSubmitted, "system", "", nil)
	if evt.Payload != nil {
		t.Fatal("expected nil payload")
	}
}
