package llm_test

import (
	"fmt"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// IsCapacityError must recognise the transient capacity / overload conditions
// an Ollama backend produces — HTTP 429/503 (typed *APIError) and the
// stringified error envelopes the Ollama HTTP client emits (server busy, no
// slots, model loading, OOM, connection refused, context deadline). These are
// transient: the request succeeds once the server has a free slot / has loaded
// the model / has memory again, so the pipeline must pause-and-resume rather
// than burn the escalation chain or fail the story.
func TestIsCapacityError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect bool
	}{
		// Typed APIError status codes from the Ollama HTTP layer.
		{name: "429 typed", err: &llm.APIError{StatusCode: 429, Message: "rate limited"}, expect: true},
		{name: "503 typed", err: &llm.APIError{StatusCode: 503, Message: "service unavailable"}, expect: true},
		{name: "529 typed", err: &llm.APIError{StatusCode: 529, Message: "overloaded"}, expect: true},
		{name: "wrapped 503 typed", err: fmt.Errorf("reviewer: %w", &llm.APIError{StatusCode: 503, Message: "x"}), expect: true},

		// Stringified Ollama HTTP-client errors — the real production failures.
		// ollama.go wraps a non-200 status as "ollama API error (status NNN): <body>".
		{name: "ollama 503 string", err: fmt.Errorf(`ollama API error (status 503): {"error":"server overloaded, please retry shortly"}`), expect: true},
		{name: "ollama 429 string", err: fmt.Errorf(`ollama API error (status 429): too many requests`), expect: true},
		{name: "server busy", err: fmt.Errorf(`ollama API error (status 503): server busy, please try again. maximum pending requests exceeded`), expect: true},
		{name: "llm busy no slots", err: fmt.Errorf(`unexpected server status: llm busy - no slots available`), expect: true},
		{name: "server overloaded", err: fmt.Errorf("Server overloaded, please retry shortly"), expect: true},
		{name: "model loading", err: fmt.Errorf("ollama: model is loading"), expect: true},
		{name: "loading model", err: fmt.Errorf("error: loading model gemma4:e4b"), expect: true},
		{name: "out of memory", err: fmt.Errorf(`ollama API error (status 500): {"error":"model requires more system memory than is available"}`), expect: true},
		{name: "out of memory phrase", err: fmt.Errorf("out of memory while loading model"), expect: true},
		{name: "context deadline", err: fmt.Errorf("ollama http request: context deadline exceeded"), expect: true},
		{name: "connection refused", err: fmt.Errorf("ollama connection refused at http://localhost:11434: is Ollama running?"), expect: true},
		{name: "dial tcp", err: fmt.Errorf("ollama http request: dial tcp 127.0.0.1:11434: connect: connection refused"), expect: true},
		{name: "too many requests", err: fmt.Errorf("too many requests"), expect: true},
		{name: "overloaded generic", err: fmt.Errorf("the backend is currently overloaded"), expect: true},

		// Must NOT classify as capacity — fatal or ordinary failures.
		{name: "401 auth typed", err: &llm.APIError{StatusCode: 401, Message: "unauthorized"}, expect: false},
		{name: "400 billing typed", err: &llm.APIError{StatusCode: 400, Message: "credit balance too low"}, expect: false},
		{name: "404 model not found", err: fmt.Errorf(`ollama model "gemma4" not found: pull it with 'ollama pull gemma4'`), expect: false},
		{name: "ordinary compile error", err: fmt.Errorf("undefined: Foo"), expect: false},
		{name: "nil", err: nil, expect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := llm.IsCapacityError(tc.err); got != tc.expect {
				t.Errorf("IsCapacityError(%v) = %v, want %v", tc.err, got, tc.expect)
			}
		})
	}
}

// ContainsCapacitySignature is the shared vocabulary scan; it must match the
// raw transient signatures and ignore fatal / ordinary text.
func TestContainsCapacitySignature(t *testing.T) {
	hits := []string{
		"server busy",
		"no slots available",
		"model is loading",
		"out of memory",
		"connection refused",
		"context deadline exceeded",
		"OVERLOADED",
		"Too Many Requests",
	}
	for _, s := range hits {
		if !llm.ContainsCapacitySignature(s) {
			t.Errorf("ContainsCapacitySignature(%q) = false, want true", s)
		}
	}

	misses := []string{
		"credit balance too low",
		"authentication failed",
		"undefined: Foo",
		"model not found: pull it with 'ollama pull'",
		"",
	}
	for _, s := range misses {
		if llm.ContainsCapacitySignature(s) {
			t.Errorf("ContainsCapacitySignature(%q) = true, want false", s)
		}
	}
}

// A capacity error must never be misclassified as fatal — they take different
// pipeline paths (fatal = give up; capacity = pause-and-resume-after-reset).
func TestCapacityErrorIsNotFatal(t *testing.T) {
	overloaded := fmt.Errorf(`ollama API error (status 503): {"error":"server overloaded, please retry shortly"}`)
	if llm.IsFatalAPIError(overloaded) {
		t.Error("Ollama overload must not be classified as fatal")
	}
	if !llm.IsCapacityError(overloaded) {
		t.Error("Ollama overload must be classified as capacity")
	}
}
