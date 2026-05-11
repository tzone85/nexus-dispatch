package web

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestHub_SetEventBus covers the trivial setter — 0% pre-#33.
// The dashboard's instant-event-push feature depends on this hook
// being wired by the dashboard CLI command at startup.
func TestHub_SetEventBus(t *testing.T) {
	s := newTestServer(t)
	bus := NewEventBus()
	s.hub.SetEventBus(bus)
	if s.hub.eventBus != bus {
		t.Error("SetEventBus did not install the bus")
	}
}

// TestHub_PushEvent_NoClientsIsNoOp covers the pushEvent broadcast
// path when there are zero WS clients connected. The fan-out loop
// must complete cleanly without trying to write to a closed map.
func TestHub_PushEvent_NoClientsIsNoOp(t *testing.T) {
	s := newTestServer(t)
	evt := state.NewEvent(state.EventStoryStarted, "test", "STORY-X", map[string]any{
		"id": "STORY-X",
	})
	// Should not panic with zero clients.
	s.hub.pushEvent(context.Background(), evt, 1)
}
