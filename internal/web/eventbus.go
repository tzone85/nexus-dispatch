package web

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// EventBus provides in-process pub/sub for real-time event streaming.
// Producers call Publish; consumers call Subscribe to get a channel.
//
// Slow consumers have events dropped, but drops are counted per-subscriber
// and reported via DropCount(). When a drop occurs the subscriber's gap
// counter increments; clients can compare the SeqNo on every received event
// to detect missed events and request a snapshot.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan EventEnvelope]*subscription
	nextSeq     atomic.Uint64
}

// EventEnvelope wraps a state.Event with a monotonic sequence number so
// subscribers can detect dropped events and re-sync.
type EventEnvelope struct {
	SeqNo uint64
	Event state.Event
}

// subscription holds per-channel telemetry.
type subscription struct {
	dropped       atomic.Uint64
	lastDropLogAt time.Time
	mu            sync.Mutex // guards lastDropLogAt
}

// NewEventBus creates a new event bus with no subscribers.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan EventEnvelope]*subscription),
	}
}

// Publish sends an event to all subscribers. Non-blocking: if a subscriber's
// channel is full, the event is dropped for that subscriber and recorded.
// A throttled log line warns operators about persistent backpressure.
func (b *EventBus) Publish(evt state.Event) {
	env := EventEnvelope{
		SeqNo: b.nextSeq.Add(1),
		Event: evt,
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch, sub := range b.subscribers {
		select {
		case ch <- env:
		default:
			n := sub.dropped.Add(1)
			sub.mu.Lock()
			now := time.Now()
			if now.Sub(sub.lastDropLogAt) > 30*time.Second {
				log.Printf("[eventbus] dropping events for slow consumer (total dropped: %d, latest seq=%d type=%s)", n, env.SeqNo, evt.Type)
				sub.lastDropLogAt = now
			}
			sub.mu.Unlock()
		}
	}
}

// Subscribe returns a channel that receives published events. The channel
// is buffered (256 events). Call Unsubscribe to clean up.
func (b *EventBus) Subscribe() chan EventEnvelope {
	ch := make(chan EventEnvelope, 256)
	b.mu.Lock()
	b.subscribers[ch] = &subscription{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (b *EventBus) Unsubscribe(ch chan EventEnvelope) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	b.mu.Unlock()
	close(ch)
}

// DropCount returns the number of events dropped for the given subscriber.
// Useful for tests and dashboards.
func (b *EventBus) DropCount(ch chan EventEnvelope) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sub, ok := b.subscribers[ch]
	if !ok {
		return 0
	}
	return sub.dropped.Load()
}
