package llm

import (
	"context"
	"fmt"
	"sync"
)

// ReplayClient returns pre-configured responses in order, useful for testing
// components that depend on the Client interface without hitting real APIs.
type ReplayClient struct {
	responses []CompletionResponse
	cursor    int
	mu        sync.Mutex
	calls     []CompletionRequest // records all calls for inspection
}

// NewReplayClient creates a ReplayClient that returns the given responses
// in sequence. Each call to Complete consumes the next response.
func NewReplayClient(responses ...CompletionResponse) *ReplayClient {
	return &ReplayClient{responses: responses}
}

// Complete returns the next pre-configured response, or an error if all
// responses have been consumed. It records every request for later inspection.
func (r *ReplayClient) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, req)

	if r.cursor >= len(r.responses) {
		return CompletionResponse{}, fmt.Errorf(
			"replay client exhausted: %d responses available, call #%d",
			len(r.responses), r.cursor+1,
		)
	}

	resp := r.responses[r.cursor]
	r.cursor++
	return resp, nil
}

// CallCount returns the number of calls made to Complete.
func (r *ReplayClient) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// CallAt returns the CompletionRequest from the call at the given index.
func (r *ReplayClient) CallAt(index int) CompletionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[index]
}

// ErrorClient always returns the same error on every Complete call. Useful
// for testing error handling paths (e.g., fatal API errors, transient failures).
type ErrorClient struct {
	err error
}

// NewErrorClient creates an ErrorClient that always returns the given error.
func NewErrorClient(err error) *ErrorClient {
	return &ErrorClient{err: err}
}

// Complete always returns the configured error.
func (e *ErrorClient) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	return CompletionResponse{}, e.err
}
