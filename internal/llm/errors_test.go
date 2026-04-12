package llm_test

import (
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestIsFatalAPIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"401 unauthorized", &llm.APIError{StatusCode: 401, Message: "invalid key"}, true},
		{"403 forbidden", &llm.APIError{StatusCode: 403, Message: "denied"}, true},
		{"400 billing", &llm.APIError{StatusCode: 400, Message: "credit balance too low"}, true},
		{"400 insufficient_quota", &llm.APIError{StatusCode: 400, Message: "insufficient_quota"}, true},
		{"429 rate limited", &llm.APIError{StatusCode: 429, Message: "rate limited"}, false},
		{"500 server error", &llm.APIError{StatusCode: 500, Message: "internal"}, false},
		{"non-API error", errors.New("network timeout"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := llm.IsFatalAPIError(tt.err); got != tt.want {
				t.Errorf("IsFatalAPIError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsInsufficientBalance(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"billing", &llm.APIError{StatusCode: 400, Message: "Your credit balance is too low"}, true},
		{"quota", &llm.APIError{StatusCode: 400, Message: "insufficient_quota"}, true},
		{"billing keyword", &llm.APIError{StatusCode: 400, Message: "billing account suspended"}, true},
		{"400 other", &llm.APIError{StatusCode: 400, Message: "bad request"}, false},
		{"non-API", errors.New("timeout"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := llm.IsInsufficientBalance(tt.err); got != tt.want {
				t.Errorf("IsInsufficientBalance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	if llm.IsRateLimited(&llm.APIError{StatusCode: 429, Message: "slow down"}) != true {
		t.Error("expected true for 429")
	}
	if llm.IsRateLimited(&llm.APIError{StatusCode: 500, Message: "error"}) != false {
		t.Error("expected false for 500")
	}
	if llm.IsRateLimited(errors.New("not an API error")) != false {
		t.Error("expected false for non-API error")
	}
}

func TestIsOverloaded(t *testing.T) {
	if llm.IsOverloaded(&llm.APIError{StatusCode: 529, Message: "overloaded"}) != true {
		t.Error("expected true for 529")
	}
	if llm.IsOverloaded(&llm.APIError{StatusCode: 200, Message: "ok"}) != false {
		t.Error("expected false for 200")
	}
}

func TestIsRetryable(t *testing.T) {
	if llm.IsRetryable(&llm.APIError{StatusCode: 500, Retryable: true}) != true {
		t.Error("expected true for retryable error")
	}
	if llm.IsRetryable(&llm.APIError{StatusCode: 401, Retryable: false}) != false {
		t.Error("expected false for non-retryable error")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	if got := llm.RetryAfterSeconds(&llm.APIError{StatusCode: 429, RetryAfter: 30}); got != 30 {
		t.Errorf("RetryAfterSeconds = %d, want 30", got)
	}
	if got := llm.RetryAfterSeconds(&llm.APIError{StatusCode: 429}); got != 0 {
		t.Errorf("RetryAfterSeconds = %d, want 0 (default)", got)
	}
	if got := llm.RetryAfterSeconds(errors.New("not API")); got != 0 {
		t.Errorf("RetryAfterSeconds = %d, want 0 for non-API error", got)
	}
}

func TestAPIError_Error(t *testing.T) {
	err := &llm.APIError{StatusCode: 401, Message: "unauthorized"}
	s := err.Error()
	if s != "API error (status 401): unauthorized" {
		t.Errorf("Error() = %q", s)
	}
}

func TestQuotaError(t *testing.T) {
	qe := &llm.QuotaError{StatusCode: 429, Message: "rate limited"}
	if !llm.IsQuotaError(qe) {
		t.Error("expected IsQuotaError to return true")
	}
	if llm.IsQuotaError(errors.New("other")) {
		t.Error("expected IsQuotaError to return false for non-quota error")
	}
	if qe.Error() == "" {
		t.Error("expected non-empty error message")
	}
}
