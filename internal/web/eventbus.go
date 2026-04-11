package web

import (
	"sync"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// EventBus provides in-process pub/sub for real-time event streaming.
// Producers call Publish; consumers call Subscribe to get a channel.
// Slow consumers have events dropped to prevent backpressure.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan state.Event]struct{}
}

// NewEventBus creates a new event bus with no subscribers.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan state.Event]struct{}),
	}
}

// Publish sends an event to all subscribers. Non-blocking: if a subscriber's
// channel is full, the event is dropped for that subscriber.
func (b *EventBus) Publish(evt state.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// drop for slow consumer
		}
	}
}

// Subscribe returns a channel that receives published events. The channel
// is buffered (64 events). Call Unsubscribe to clean up.
func (b *EventBus) Subscribe() chan state.Event {
	ch := make(chan state.Event, 64)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (b *EventBus) Unsubscribe(ch chan state.Event) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
	close(ch)
}
