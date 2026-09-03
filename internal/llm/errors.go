package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// maxErrorBodyBytes bounds the response-body snippet kept on an APIError.
const maxErrorBodyBytes = 512

// APIError represents a structured error from an LLM provider's HTTP API.
// It carries the HTTP status code, the provider, a bounded and
// secret-redacted body snippet, and whether the error is transient.
type APIError struct {
	StatusCode int
	Provider   string // "anthropic", "openai", "ollama", "google"
	Message    string // body snippet (≤ maxErrorBodyBytes, secrets redacted)
	Retryable  bool
	RetryAfter int // seconds; 0 means not specified
}

func (e *APIError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("%s API error (status %d): %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// newAPIError builds the typed error for a non-2xx provider response. Every
// HTTP client in this package funnels its error responses through here so
// IsFatalAPIError / IsRateLimited / IsOverloaded / IsRetryable /
// RetryAfterSeconds work uniformly across providers.
func newAPIError(provider string, resp *http.Response, body []byte) *APIError {
	return &APIError{
		StatusCode: resp.StatusCode,
		Provider:   provider,
		Message:    errorBodySnippet(resp.StatusCode, body),
		Retryable:  isRetryableStatus(resp.StatusCode),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

// isRetryableStatus reports whether a status is transient: request timeout,
// rate limit, and server-side failures (including Anthropic's 529 overloaded).
func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// errorBodySnippet trims, redacts and bounds a response body for inclusion in
// an error message; an empty body falls back to the HTTP status text.
func errorBodySnippet(status int, body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return fmt.Sprintf("%d %s", status, http.StatusText(status))
	}
	s = sanitize.RedactSecrets(s)
	if len(s) > maxErrorBodyBytes {
		s = s[:maxErrorBodyBytes] + "…"
	}
	return s
}

// parseRetryAfter converts a Retry-After header (delta-seconds or HTTP-date)
// into whole seconds; unparseable, absent or past values yield 0.
func parseRetryAfter(h string) int {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0
		}
		return secs
	}
	if at, err := http.ParseTime(h); err == nil {
		if d := time.Until(at); d > 0 {
			return int(d.Round(time.Second) / time.Second)
		}
	}
	return 0
}

// IsInsufficientBalance returns true when the error indicates the API account
// has run out of credits. This is a fatal condition — retrying won't help.
func IsInsufficientBalance(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == http.StatusPaymentRequired {
		return true
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

// capacitySignatures are substrings that indicate a transient capacity /
// resource-exhaustion condition the caller should back off from rather than
// treat as a story-quality failure. These are the strings the Ollama HTTP
// client (internal/llm/ollama.go) and the Ollama server itself actually
// produce. Kept lowercase; match against a lowercased error string.
var capacitySignatures = []string{
	"too many requests",                    // HTTP 429 canonical text
	"rate limit",                           // generic rate limiting
	"overloaded",                           // Ollama "server overloaded, please retry shortly"
	"server busy",                          // Ollama "server busy, please try again"
	"llm busy",                             // Ollama "unexpected server status: llm busy"
	"no slots available",                   // Ollama queue full (OLLAMA_NUM_PARALLEL saturated)
	"maximum pending requests exceeded",    // Ollama queue depth (OLLAMA_MAX_QUEUE)
	"model is loading",                     // model warm-up in progress
	"loading model",                        // alternate warm-up wording
	"out of memory",                        // OOM — succeeds once another model unloads
	"more system memory than is available", // Ollama OOM body
	"context deadline exceeded",            // per-iteration timeout under server load
	"connection refused",                   // Ollama not reachable / overwhelmed
	"dial tcp",                             // dial failure to the Ollama socket
	"(status 503)",                         // ollama.go envelope: "ollama API error (status 503): ..."
	"(status 429)",                         // ollama.go envelope: rate limited
	"(status 529)",                         // generic overloaded status
}

// ContainsCapacitySignature reports whether a raw string carries a transient
// capacity / overload / resource-exhaustion signal. Exported so the engine's
// pause path can scan an agent's recorded error envelope with the exact same
// vocabulary the predicate uses.
func ContainsCapacitySignature(s string) bool {
	lower := strings.ToLower(s)
	for _, sig := range capacitySignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// IsCapacityError returns true when the error is a transient capacity /
// resource exhaustion — HTTP 429 (rate limit), 503 (service unavailable /
// overloaded), or 529 — whether it arrived as a typed *APIError or as a
// stringified Ollama error that never got classified.
//
// This is distinct from IsFatalAPIError (401/403/billing — permanent): a
// capacity error WILL succeed once the server frees a slot / loads the model /
// reclaims memory, so the pipeline should pause-and-resume rather than burn the
// escalation chain or fail the story. A 404 (model not found) is NOT capacity —
// it is an operator config error that retrying won't fix.
func IsCapacityError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 429 || apiErr.StatusCode == 503 || apiErr.StatusCode == 529 {
			return true
		}
	}
	// Fall back to scanning the error text: the Ollama HTTP client stringifies
	// the server's overload / busy / loading / OOM envelope into a plain error
	// before it reaches a decision point.
	return ContainsCapacitySignature(err.Error())
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

// QuotaError indicates the API free tier quota or rate limit was exhausted.
// It wraps the underlying *APIError so the generic predicates (IsRateLimited,
// RetryAfterSeconds, ...) see through it while FallbackClient keeps keying on
// IsQuotaError.
type QuotaError struct {
	StatusCode int
	Message    string
	api        *APIError
}

// newQuotaError wraps a provider APIError as a quota-exhaustion signal.
func newQuotaError(api *APIError) *QuotaError {
	return &QuotaError{StatusCode: api.StatusCode, Message: api.Message, api: api}
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("quota exhausted (HTTP %d): %s", e.StatusCode, e.Message)
}

// Unwrap exposes the underlying *APIError (nil when constructed directly).
func (e *QuotaError) Unwrap() error {
	if e.api == nil {
		return nil
	}
	return e.api
}

// IsQuotaError returns true if the error is a quota/rate-limit error.
func IsQuotaError(err error) bool {
	var qe *QuotaError
	return errors.As(err, &qe)
}
