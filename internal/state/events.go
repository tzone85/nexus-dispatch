package state

import (
	"crypto/rand"
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// EventType represents the type of a domain event in the system.
type EventType string

const (
	// Request lifecycle events.
	EventReqSubmitted EventType = "REQ_SUBMITTED"
	EventReqAnalyzed  EventType = "REQ_ANALYZED"
	EventReqPlanned   EventType = "REQ_PLANNED"
	EventReqPaused    EventType = "REQ_PAUSED"
	EventReqResumed   EventType = "REQ_RESUMED"
	EventReqCompleted           EventType = "REQ_COMPLETED"
	EventReqClassified          EventType = "REQ_CLASSIFIED"
	EventInvestigationCompleted EventType = "INVESTIGATION_COMPLETED"
	EventReqPendingReview       EventType = "REQ_PENDING_REVIEW"
	EventReqRejected            EventType = "REQ_REJECTED"

	// Story lifecycle events.
	EventStoryCreated         EventType = "STORY_CREATED"
	EventStoryEstimated       EventType = "STORY_ESTIMATED"
	EventStoryAssigned        EventType = "STORY_ASSIGNED"
	EventStoryStarted         EventType = "STORY_STARTED"
	EventStoryProgress        EventType = "STORY_PROGRESS"
	EventStoryCompleted       EventType = "STORY_COMPLETED"
	EventStoryReviewRequested EventType = "STORY_REVIEW_REQUESTED"
	EventStoryReviewPassed    EventType = "STORY_REVIEW_PASSED"
	EventStoryReviewFailed    EventType = "STORY_REVIEW_FAILED"
	EventStoryQAStarted       EventType = "STORY_QA_STARTED"
	EventStoryQAPassed        EventType = "STORY_QA_PASSED"
	EventStoryPRCreated       EventType = "STORY_PR_CREATED"
	EventStoryMerged          EventType = "STORY_MERGED"
	EventStoryMergeReady      EventType = "STORY_MERGE_READY"
	EventStoryRecovery        EventType = "STORY_RECOVERY"
	EventStoryEscalated       EventType = "STORY_ESCALATED"
	EventStoryRewritten       EventType = "STORY_REWRITTEN"
	EventStorySplit           EventType = "STORY_SPLIT"

	// Agent lifecycle events.
	EventAgentSpawned    EventType = "AGENT_SPAWNED"
	EventAgentCheckpoint EventType = "AGENT_CHECKPOINT"
	EventAgentResumed    EventType = "AGENT_RESUMED"
	EventAgentStuck      EventType = "AGENT_STUCK"
	EventAgentTerminated EventType = "AGENT_TERMINATED"

	// Supervisor events.
	EventSupervisorCheck         EventType = "SUPERVISOR_CHECK"
	EventSupervisorReprioritize  EventType = "SUPERVISOR_REPRIORITIZE"
	EventSupervisorDriftDetected EventType = "SUPERVISOR_DRIFT_DETECTED"

	// Cleanup events.
	EventWorktreePruned EventType = "WORKTREE_PRUNED"
	EventBranchDeleted  EventType = "BRANCH_DELETED"
	EventGCCompleted    EventType = "GC_COMPLETED"
)

// Event represents a single domain event in the append-only event store.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agent_id"`
	StoryID   string    `json:"story_id,omitempty"`
	Payload   []byte    `json:"payload,omitempty"`
}

// DecodePayload unmarshals a JSON-encoded event payload into a map.
// Returns an empty map if the payload is nil or cannot be decoded.
func DecodePayload(payload []byte) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// NewEvent creates a new Event with a ULID identifier and current timestamp.
// If data is nil, the payload will be nil (not an empty JSON object).
func NewEvent(eventType EventType, agentID, storyID string, data map[string]any) Event {
	var payload []byte
	if data != nil {
		payload, _ = json.Marshal(data)
	}

	id := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)

	return Event{
		ID:        id.String(),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		AgentID:   agentID,
		StoryID:   storyID,
		Payload:   payload,
	}
}
