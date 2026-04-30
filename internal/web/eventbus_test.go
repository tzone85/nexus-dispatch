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
	case env := <-ch:
		if env.Event.Type != state.EventStoryProgress {
			t.Errorf("Type = %q, want %q", env.Event.Type, state.EventStoryProgress)
		}
		if env.Event.StoryID != "s-001" {
			t.Errorf("StoryID = %q, want s-001", env.Event.StoryID)
		}
		if env.SeqNo == 0 {
			t.Error("SeqNo should be > 0")
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

	for i, ch := range []chan EventEnvelope{ch1, ch2} {
		select {
		case env := <-ch:
			if env.Event.Type != state.EventReqSubmitted {
				t.Errorf("subscriber %d: Type = %q, want REQ_SUBMITTED", i, env.Event.Type)
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
	bus.Publish(evt)

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after Unsubscribe")
	}
}

func TestEventBus_SlowConsumerDropAndCount(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// Fill the channel buffer (capacity is 256) and overflow.
	for i := 0; i < 400; i++ {
		bus.Publish(state.NewEvent(state.EventStoryProgress, "agent", "s-001", map[string]any{
			"iteration": i,
		}))
	}

	if got := bus.DropCount(ch); got == 0 {
		t.Error("expected non-zero drop count after overflow")
	}

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
	if count > 256 {
		t.Errorf("received %d events, expected at most 256 (buffer size)", count)
	}
	if count == 0 {
		t.Error("expected at least some events")
	}
}

func TestEventBus_SeqNoMonotonic(t *testing.T) {
	bus := NewEventBus()
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	for i := 0; i < 5; i++ {
		bus.Publish(state.NewEvent(state.EventStoryProgress, "a", "s", nil))
	}

	var prev uint64
	for i := 0; i < 5; i++ {
		select {
		case env := <-ch:
			if env.SeqNo <= prev {
				t.Errorf("seq %d not greater than previous %d", env.SeqNo, prev)
			}
			prev = env.SeqNo
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}

func TestEventBus_PublishNoSubscribers(t *testing.T) {
	bus := NewEventBus()
	// Should not panic.
	bus.Publish(state.NewEvent(state.EventStoryProgress, "agent", "s-001", nil))
}
