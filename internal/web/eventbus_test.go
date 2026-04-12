package web

import (
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestEventBus_SubscribeAndPublish(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	evt := state.NewEvent(state.EventStoryProgress, "agent-1", "s-001", map[string]any{
		"iteration": 1,
	})
	bus.Publish(evt)

	select {
	case received := <-ch:
		if received.Type != state.EventStoryProgress {
			t.Errorf("Type = %q, want %q", received.Type, state.EventStoryProgress)
		}
		if received.StoryID != "s-001" {
			t.Errorf("StoryID = %q, want s-001", received.StoryID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()
	defer bus.Unsubscribe(ch1)
	defer bus.Unsubscribe(ch2)

	evt := state.NewEvent(state.EventReqSubmitted, "system", "", nil)
	bus.Publish(evt)

	for i, ch := range []chan state.Event{ch1, ch2} {
		select {
		case received := <-ch:
			if received.Type != state.EventReqSubmitted {
				t.Errorf("subscriber %d: Type = %q, want REQ_SUBMITTED", i, received.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe()
	bus.Unsubscribe(ch)

	// Publishing after unsubscribe should not panic.
	evt := state.NewEvent(state.EventStoryProgress, "agent-1", "s-001", nil)
	bus.Publish(evt) // no subscribers, should be a no-op

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after Unsubscribe")
	}
}

func TestEventBus_SlowConsumerDrop(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// Fill the channel buffer (capacity is 64).
	for i := 0; i < 100; i++ {
		bus.Publish(state.NewEvent(state.EventStoryProgress, "agent", "s-001", map[string]any{
			"iteration": i,
		}))
	}

	// Drain what we can — should get at most 64 (buffer size).
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count > 64 {
		t.Errorf("received %d events, expected at most 64 (buffer size)", count)
	}
	if count == 0 {
		t.Error("expected at least some events")
	}
}

func TestEventBus_PublishNoSubscribers(t *testing.T) {
	bus := NewEventBus()
	// Should not panic.
	bus.Publish(state.NewEvent(state.EventStoryProgress, "agent", "s-001", nil))
}
