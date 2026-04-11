package engine

import (
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Attempt records a single execution attempt for a story.
type Attempt struct {
	Number    int           `json:"number"`
	Tier      int           `json:"tier"`
	Role      string        `json:"role"`
	AgentID   string        `json:"agent_id"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Duration  time.Duration `json:"duration_ms"`
	Outcome   string        `json:"outcome"` // "success", "review_failed", "qa_failed", "error"
	Error     string        `json:"error,omitempty"`
}

// AttemptTracker reconstructs attempt history from the event log.
type AttemptTracker struct {
	eventStore state.EventStore
}

// NewAttemptTracker creates a tracker that reads from the event store.
func NewAttemptTracker(es state.EventStore) *AttemptTracker {
	return &AttemptTracker{eventStore: es}
}

// ListAttempts reconstructs all attempts for a story by scanning events.
// An attempt starts with STORY_STARTED and ends with
// STORY_QA_PASSED, STORY_MERGED, STORY_REVIEW_FAILED, STORY_QA_FAILED,
// or STORY_RESET.
func (at *AttemptTracker) ListAttempts(storyID string) ([]Attempt, error) {
	events, err := at.eventStore.List(state.EventFilter{StoryID: storyID})
	if err != nil {
		return nil, err
	}

	var attempts []Attempt
	var current *Attempt
	attemptNum := 0

	for _, evt := range events {
		switch evt.Type {
		case state.EventStoryStarted:
			attemptNum++
			payload := state.DecodePayload(evt.Payload)
			current = &Attempt{
				Number:    attemptNum,
				AgentID:   evt.AgentID,
				StartedAt: evt.Timestamp,
			}
			if tier, ok := payload["tier"]; ok {
				if tierNum, ok := tier.(float64); ok {
					current.Tier = int(tierNum)
				}
			}
			if role, ok := payload["role"]; ok {
				if roleStr, ok := role.(string); ok {
					current.Role = roleStr
				}
			}

		case state.EventStoryQAPassed, state.EventStoryMerged:
			if current != nil {
				current.EndedAt = evt.Timestamp
				current.Duration = current.EndedAt.Sub(current.StartedAt)
				current.Outcome = "success"
				attempts = append(attempts, *current)
				current = nil
			}

		case state.EventStoryReviewFailed:
			if current != nil {
				current.EndedAt = evt.Timestamp
				current.Duration = current.EndedAt.Sub(current.StartedAt)
				current.Outcome = "review_failed"
				payload := state.DecodePayload(evt.Payload)
				if reason, ok := payload["reason"]; ok {
					if reasonStr, ok := reason.(string); ok {
						current.Error = reasonStr
					}
				}
				attempts = append(attempts, *current)
				current = nil
			}

		case state.EventStoryQAFailed:
			if current != nil {
				current.EndedAt = evt.Timestamp
				current.Duration = current.EndedAt.Sub(current.StartedAt)
				current.Outcome = "qa_failed"
				payload := state.DecodePayload(evt.Payload)
				if checks, ok := payload["failed_checks"]; ok {
					if checksSlice, ok := checks.([]any); ok {
						names := make([]string, 0, len(checksSlice))
						for _, c := range checksSlice {
							if s, ok := c.(string); ok {
								names = append(names, s)
							}
						}
						current.Error = strings.Join(names, ", ")
					}
				}
				attempts = append(attempts, *current)
				current = nil
			}

		case state.EventStoryReset:
			if current != nil {
				current.EndedAt = evt.Timestamp
				current.Duration = current.EndedAt.Sub(current.StartedAt)
				current.Outcome = "error"
				payload := state.DecodePayload(evt.Payload)
				if reason, ok := payload["reason"]; ok {
					if reasonStr, ok := reason.(string); ok {
						current.Error = reasonStr
					}
				}
				attempts = append(attempts, *current)
				current = nil
			}
		}
	}

	// If there's an unclosed attempt (still running), include it.
	if current != nil {
		current.Outcome = "in_progress"
		current.EndedAt = time.Now().UTC()
		current.Duration = current.EndedAt.Sub(current.StartedAt)
		attempts = append(attempts, *current)
	}

	return attempts, nil
}

// LastAttempt returns the most recent attempt, or nil if none.
func (at *AttemptTracker) LastAttempt(storyID string) (*Attempt, error) {
	attempts, err := at.ListAttempts(storyID)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 {
		return nil, nil
	}
	last := attempts[len(attempts)-1]
	return &last, nil
}
