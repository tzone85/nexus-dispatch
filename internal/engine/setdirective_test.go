package engine

import "testing"

// TestExecutor_SetDirectiveStore covers the trivial setter — 0%
// before this. The runtime relies on operator directives being
// findable via the executor's wiring; a future rename of the field
// would silently break the directive-injection feature without this
// test.
func TestExecutor_SetDirectiveStore(t *testing.T) {
	e := &Executor{}
	if e.directives != nil {
		t.Fatal("Executor.directives should start nil")
	}
	ds := &DirectiveStore{}
	e.SetDirectiveStore(ds)
	if e.directives != ds {
		t.Errorf("SetDirectiveStore did not install the provided store")
	}
}
