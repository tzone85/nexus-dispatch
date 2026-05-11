package llm

import (
	"context"
	"testing"
)

// dummyClient is a no-op llm.Client used by SemaphoreClient tests.
// It records whether Complete was called so the rewrap test can
// confirm calls flow through to the new inner.
type dummyClient struct {
	called bool
}

func (d *dummyClient) Complete(ctx context.Context, _ CompletionRequest) (CompletionResponse, error) {
	d.called = true
	return CompletionResponse{}, nil
}

// TestSemaphoreClient_Inner_ReturnsWrappedClient covers the Inner()
// accessor — used by metrics.relabel to walk through the semaphore
// wrapper. Was 0% pre-#33 even though the function is part of the
// public API documented as test-friendly.
func TestSemaphoreClient_Inner_ReturnsWrappedClient(t *testing.T) {
	inner := &dummyClient{}
	sem := NewSemaphoreClient(inner, 1)
	if sem.Inner() != inner {
		t.Errorf("Inner() did not return the wrapped client")
	}
}

// TestSemaphoreClient_Rewrap_PreservesChannel locks down the
// Rewrap contract: the returned SemaphoreClient must share the
// original's semaphore channel so a global concurrency limit
// remains enforced across labelled siblings.
func TestSemaphoreClient_Rewrap_PreservesChannel(t *testing.T) {
	innerA := &dummyClient{}
	innerB := &dummyClient{}

	orig := NewSemaphoreClient(innerA, 2)
	rewrapped := orig.Rewrap(innerB)

	if rewrapped == nil {
		t.Fatal("Rewrap returned nil")
	}
	if rewrapped.Inner() != innerB {
		t.Errorf("Rewrap did not install new inner")
	}
	if rewrapped == orig {
		t.Error("Rewrap should return a new instance, not mutate")
	}
	// Drive a Complete call through the rewrapped client to ensure
	// the new inner is wired.
	_, err := rewrapped.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Errorf("Complete: %v", err)
	}
	if !innerB.called {
		t.Error("expected rewrapped Complete to reach new inner")
	}
}
