package engine

import (
	"testing"
)

// TestPendingForRuntime_TranslatesShape covers the runtime-adapter
// translation: engine.Directive → runtime.PendingDirective. The runtime
// package can't import engine (cycle), so this adapter is the only
// path Gemma's iteration loop has to operator directives. Empty input
// must produce nil so the runtime can `if len(d) == 0 { return }`.
func TestPendingForRuntime_TranslatesShape(t *testing.T) {
	es := newDirectiveTestStore(t)
	id := emitDirective(t, es, "REQ-RT", "STORY-RT", "use feature flag X")

	d := NewDirectiveStore(es)
	got, err := d.PendingForRuntime("REQ-RT", "STORY-RT")
	if err != nil {
		t.Fatalf("PendingForRuntime: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].ID != id || got[0].Instruction != "use feature flag X" {
		t.Errorf("translation wrong: %+v", got[0])
	}
}

// TestPendingForRuntime_EmptyReturnsNil covers the early-return branch
// — important because runtime.DirectiveProvider callers treat nil as
// "no work" without iterating an empty slice.
func TestPendingForRuntime_EmptyReturnsNil(t *testing.T) {
	d := NewDirectiveStore(newDirectiveTestStore(t))
	got, err := d.PendingForRuntime("REQ-X", "STORY-X")
	if err != nil {
		t.Fatalf("PendingForRuntime: %v", err)
	}
	if got != nil {
		t.Errorf("empty store → nil expected, got %v", got)
	}
}

// TestAsRuntimeProvider_NilReceiverReturnsNil covers the chain-friendly
// guard: callers do `executor.SetDirectives(store.AsRuntimeProvider())`
// without checking — nil store must yield nil provider.
func TestAsRuntimeProvider_NilReceiverReturnsNil(t *testing.T) {
	var d *DirectiveStore
	if d.AsRuntimeProvider() != nil {
		t.Error("nil store must produce nil provider for safe chaining")
	}
}

// TestAsRuntimeProvider_RoundTrip drives both adapter methods (Pending
// + Ack) through the public DirectiveProvider interface so the
// indirection is exercised end to end.
func TestAsRuntimeProvider_RoundTrip(t *testing.T) {
	es := newDirectiveTestStore(t)
	id := emitDirective(t, es, "REQ", "STORY", "do thing")

	d := NewDirectiveStore(es)
	provider := d.AsRuntimeProvider()
	if provider == nil {
		t.Fatal("real store must produce non-nil provider")
	}

	pending, err := provider.Pending("REQ", "STORY")
	if err != nil {
		t.Fatalf("provider.Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("provider.Pending wrong: %+v", pending)
	}

	if err := provider.Ack("agent", "STORY", []string{id}); err != nil {
		t.Fatalf("provider.Ack: %v", err)
	}
	// After ack, Pending should be empty.
	pending, _ = provider.Pending("REQ", "STORY")
	if len(pending) != 0 {
		t.Errorf("after Ack, expected 0 pending, got %d", len(pending))
	}
}
