package llm

import "context"

// SemaphoreClient wraps an LLM Client with a concurrency limiter. At most
// maxConcurrent calls to Complete proceed simultaneously; additional callers
// block until a slot is available or the context is cancelled.
type SemaphoreClient struct {
	inner Client
	sem   chan struct{}
}

// NewSemaphoreClient creates a concurrency-limited wrapper around inner.
// maxConcurrent must be >= 1.
func NewSemaphoreClient(inner Client, maxConcurrent int) *SemaphoreClient {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &SemaphoreClient{
		inner: inner,
		sem:   make(chan struct{}, maxConcurrent),
	}
}

// Inner returns the wrapped client. Used by labelling helpers
// (e.g. metrics.LabelStory) that need to attach metadata to the
// underlying MetricsClient — after they label, they Rewrap to keep the
// concurrency limit enforced.
func (s *SemaphoreClient) Inner() Client { return s.inner }

// Rewrap returns a new SemaphoreClient that shares the same semaphore
// channel as s but delegates to inner. Sharing the channel preserves
// the global concurrency limit across labelled siblings.
func (s *SemaphoreClient) Rewrap(inner Client) *SemaphoreClient {
	return &SemaphoreClient{inner: inner, sem: s.sem}
}

// Complete acquires a semaphore slot, delegates to the inner client, and
// releases the slot when done. If the context is cancelled while waiting
// for a slot, the call returns immediately with the context error.
func (s *SemaphoreClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	select {
	case s.sem <- struct{}{}:
		// acquired slot
	case <-ctx.Done():
		return CompletionResponse{}, ctx.Err()
	}
	defer func() { <-s.sem }()

	return s.inner.Complete(ctx, req)
}
