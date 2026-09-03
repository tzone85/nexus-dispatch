package state

import "time"

// EventFilter specifies criteria for filtering events from the store.
type EventFilter struct {
	Type    EventType
	AgentID string
	StoryID string
	// AttemptID, when non-empty, selects only events stamped with that
	// attempt (see Event.AttemptID). Legacy events with no attempt never match.
	AttemptID string
	Limit     int
	After     time.Time
}

// EventStore defines the interface for an append-only event log.
type EventStore interface {
	Append(event Event) error
	List(filter EventFilter) ([]Event, error)
	Count(filter EventFilter) (int, error)
	Close() error
}
