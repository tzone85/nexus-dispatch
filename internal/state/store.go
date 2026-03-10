package state

// EventFilter specifies criteria for filtering events from the store.
type EventFilter struct {
	Type    EventType
	AgentID string
	StoryID string
	Limit   int
}

// EventStore defines the interface for an append-only event log.
type EventStore interface {
	Append(event Event) error
	List(filter EventFilter) ([]Event, error)
	Count(filter EventFilter) (int, error)
	Close() error
}
