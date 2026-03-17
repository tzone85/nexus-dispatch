package llm

import (
	"errors"
	"fmt"
	"strings"
)

// APIError represents a structured error from an LLM provider's HTTP API.
// It carries the HTTP status code and whether the error is transient (retryable).
type APIError struct {
	StatusCode int
	Message    string
	Retryable  bool
	RetryAfter int // seconds; 0 means not specified
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// IsInsufficientBalance returns true when the error indicates the API account
// has run out of credits. This is a fatal condition — retrying won't help.
func IsInsufficientBalance(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	return apiErr.StatusCode == 400 &&
		(strings.Contains(msg, "credit balance") ||
			strings.Contains(msg, "billing") ||
			strings.Contains(msg, "insufficient_quota"))
}

// IsFatalAPIError returns true when the error is a non-retryable API error
// that will never succeed regardless of retries — e.g. invalid credentials
// (401), insufficient balance (400), or permission denied (403).
func IsFatalAPIError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == 401 || apiErr.StatusCode == 403 || IsInsufficientBalance(err)
}

// IsRateLimited returns true when the error is an HTTP 429 (Too Many Requests).
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == 429
}

// IsOverloaded returns true when the API reports it is overloaded (Anthropic 529).
func IsOverloaded(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == 529
}

// IsRetryable returns true when the error is transient and the request can be retried.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Retryable
}

// RetryAfterSeconds returns the Retry-After hint from the API, or 0 if not set.
func RetryAfterSeconds(err error) int {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return 0
	}
	return apiErr.RetryAfter
}
