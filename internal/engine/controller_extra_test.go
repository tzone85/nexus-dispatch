package engine

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestController_DeregisterCancel_RemovesEntry covers the cleanup path
// the runtime goroutine calls on normal completion. Without it the
// cancelFuncs map grows unbounded across the daemon's lifetime (bug
// H5 in the original audit).
func TestController_DeregisterCancel_RemovesEntry(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	ctrl.RegisterCancel("s-1", func() {})
	ctrl.RegisterCancel("s-2", func() {})

	ctrl.DeregisterCancel("s-1")

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if _, present := ctrl.cancelFuncs["s-1"]; present {
		t.Error("DeregisterCancel left s-1 in the map")
	}
	if _, present := ctrl.cancelFuncs["s-2"]; !present {
		t.Error("DeregisterCancel removed an unrelated entry (s-2)")
	}
}

// TestController_DeregisterCancel_UnknownNoOps confirms the deregister
// is safe to call for stories never registered (e.g. when the runtime
// completes before RegisterCancel ran).
func TestController_DeregisterCancel_UnknownNoOps(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	ctrl.DeregisterCancel("never-registered")
	// nothing to assert beyond not panicking; map should still be empty
	ctrl.mu.Lock()
	if len(ctrl.cancelFuncs) != 0 {
		t.Errorf("map should remain empty, got %d entries", len(ctrl.cancelFuncs))
	}
	ctrl.mu.Unlock()
}

// TestController_ExecuteAction_CancelEmitsEvent covers the action
// dispatch path's "cancel" branch: it must invoke the registered
// cancel func + emit a CONTROLLER_ACTION event. executeAction is the
// only place those events fire, so the dashboard's recovery log
// depends on this flow.
func TestController_ExecuteAction_CancelEmitsEvent(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{Enabled: true}, nil, es, ps)

	cancelled := false
	ctrl.RegisterCancel("s-cancel", func() { cancelled = true })

	ctrl.executeAction(context.Background(), ControlAction{
		Kind:    ActionCancel,
		StoryID: "s-cancel",
		Reason:  "stuck beyond threshold",
	})

	if !cancelled {
		t.Error("cancel func not invoked")
	}
	evts, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(evts) == 0 {
		t.Fatal("expected CONTROLLER_ACTION event")
	}
	payload := state.DecodePayload(evts[0].Payload)
	if payload["kind"] != "cancel" {
		t.Errorf("kind = %v, want cancel", payload["kind"])
	}
}

// TestController_ExecuteAction_RestartCancelsAndResets covers the
// "restart" branch: cancel + reset to draft + event. The reset is
// the only way a stuck story gets re-dispatched without operator
// intervention.
func TestController_ExecuteAction_RestartCancelsAndResets(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{Enabled: true}, nil, es, ps)

	ctrl.RegisterCancel("s-restart", func() {})

	ctrl.executeAction(context.Background(), ControlAction{
		Kind:    ActionRestart,
		StoryID: "s-restart",
		Reason:  "auto-restart",
	})

	rec, _ := es.List(state.EventFilter{Type: state.EventStoryRecovery})
	if len(rec) == 0 {
		t.Error("restart should emit STORY_RECOVERY")
	}
}
