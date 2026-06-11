package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestWaitForNativeShutdown_EmptyReturnsImmediately covers the trivial case
// where no goroutine has been registered. The implementation must not block.
func TestWaitForNativeShutdown_EmptyReturnsImmediately(t *testing.T) {
	e := &Executor{}
	start := time.Now()
	if err := e.WaitForNativeShutdown(2 * time.Second); err != nil {
		t.Fatalf("empty drain: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("empty drain took %v, want immediate", elapsed)
	}
}

// TestWaitForNativeShutdown_AwaitsLiveGoroutine simulates a running spawnNative
// goroutine by incrementing the WaitGroup directly. The drain must block until
// the simulated goroutine signals Done.
func TestWaitForNativeShutdown_AwaitsLiveGoroutine(t *testing.T) {
	e := &Executor{}
	e.nativeWG.Add(1)

	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		e.nativeWG.Done()
		close(released)
	}()

	start := time.Now()
	if err := e.WaitForNativeShutdown(2 * time.Second); err != nil {
		t.Fatalf("drain: %v", err)
	}
	<-released
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("drain returned too quickly (%v), expected to wait for goroutine", elapsed)
	}
}

// TestWaitForNativeShutdown_TimeoutReturnsDeadlineExceeded asserts that a
// goroutine that never signals Done causes the drain to time out rather than
// block forever. Models a hung native runtime.
func TestWaitForNativeShutdown_TimeoutReturnsDeadlineExceeded(t *testing.T) {
	e := &Executor{}
	e.nativeWG.Add(1) // never Done

	err := e.WaitForNativeShutdown(120 * time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain timeout: got %v, want context.DeadlineExceeded", err)
	}

	// Release the WaitGroup so the test cleanup goroutine doesn't leak.
	e.nativeWG.Done()
}

// TestWaitForNativeShutdown_ReusableAfterDrain confirms the WaitGroup can be
// re-armed after one drain — important because Executor instances may run
// multiple resume cycles in long-lived daemons.
func TestWaitForNativeShutdown_ReusableAfterDrain(t *testing.T) {
	e := &Executor{}
	var wg sync.WaitGroup
	wg.Add(2)

	// Drain 1
	e.nativeWG.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		e.nativeWG.Done()
	}()
	if err := e.WaitForNativeShutdown(time.Second); err != nil {
		t.Fatalf("drain 1: %v", err)
	}

	// Drain 2 (Executor is reused)
	e.nativeWG.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		e.nativeWG.Done()
	}()
	if err := e.WaitForNativeShutdown(time.Second); err != nil {
		t.Fatalf("drain 2: %v", err)
	}

	wg.Wait()
}
