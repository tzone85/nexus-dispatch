package codegraph

import (
	"context"
	"testing"
)

// TestRunner_BuildAndUpdate_WhenUnavailable cover the "not
// installed" branches of Build and Update. Without these tests,
// the early-return paths stayed uncovered and a regression in
// the Available() check could let those methods crash on a nil
// runner. Operators running NXD without code-review-graph
// installed rely on the methods returning an error rather than
// panicking.
func TestRunner_BuildAndUpdate_WhenUnavailable(t *testing.T) {
	r := &Runner{} // Available() returns false because BinPath is empty
	ctx := context.Background()

	if err := r.Build(ctx, "/tmp"); err == nil {
		t.Error("expected error from Build when not installed")
	}
	if err := r.Update(ctx, "/tmp", "main"); err == nil {
		t.Error("expected error from Update when not installed")
	}
	if err := r.Update(ctx, "/tmp", ""); err == nil {
		t.Error("expected error from Update when not installed (no base ref)")
	}
}

// TestGraphDB_Close_NilDB covers the nil-guard in Close (called
// when GraphDB construction half-failed). Without the guard,
// callers' defer g.Close() would panic on a partially-built db.
func TestGraphDB_Close_NilDB(t *testing.T) {
	g := &GraphDB{}
	if err := g.Close(); err != nil {
		t.Errorf("Close on nil-db GraphDB should return nil; got %v", err)
	}
}
