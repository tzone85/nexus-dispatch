package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type delayClient struct {
	delay   time.Duration
	current atomic.Int32
	peak    atomic.Int32
}

func (d *delayClient) Complete(ctx context.Context, _ CompletionRequest) (CompletionResponse, error) {
	n := d.current.Add(1)
	// Track peak concurrency.
	for {
		old := d.peak.Load()
		if n <= old || d.peak.CompareAndSwap(old, n) {
			break
		}
	}
	defer d.current.Add(-1)

	select {
	case <-time.After(d.delay):
		return CompletionResponse{Content: "ok"}, nil
	case <-ctx.Done():
		return CompletionResponse{}, ctx.Err()
	}
}

func TestSemaphoreClient_LimitsConcurrency(t *testing.T) {
	inner := &delayClient{delay: 50 * time.Millisecond}
	sc := NewSemaphoreClient(inner, 2)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc.Complete(context.Background(), CompletionRequest{})
		}()
	}
	wg.Wait()

	if peak := inner.peak.Load(); peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
}

func TestSemaphoreClient_RespectsContext(t *testing.T) {
	inner := &delayClient{delay: 10 * time.Second}
	sc := NewSemaphoreClient(inner, 1)

	// Fill the single slot.
	go sc.Complete(context.Background(), CompletionRequest{})
	time.Sleep(10 * time.Millisecond) // let the goroutine acquire the slot

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sc.Complete(ctx, CompletionRequest{})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestSemaphoreClient_MinConcurrencyIsOne(t *testing.T) {
	sc := NewSemaphoreClient(NewErrorClient(nil), 0)
	if cap(sc.sem) != 1 {
		t.Errorf("sem capacity = %d, want 1", cap(sc.sem))
	}
}
