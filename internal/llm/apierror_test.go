package llm

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fakeResp(status int, headers map[string]string) *http.Response {
	r := &http.Response{StatusCode: status, Header: http.Header{}}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestNewAPIError_PopulatesFields(t *testing.T) {
	body := `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	err := newAPIError("anthropic", fakeResp(401, nil), []byte(body))
	if err.StatusCode != 401 || err.Provider != "anthropic" {
		t.Fatalf("fields: %+v", err)
	}
	if err.Message != body {
		t.Fatalf("Message should carry the body snippet, got %q", err.Message)
	}
	if err.Retryable {
		t.Fatal("401 is not retryable")
	}
	if !strings.Contains(err.Error(), "anthropic API error (status 401)") {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !IsFatalAPIError(err) {
		t.Fatal("401 must be fatal")
	}
}

func TestNewAPIError_RetryableStatuses(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 503, 504, 529} {
		if e := newAPIError("p", fakeResp(status, nil), nil); !e.Retryable || !IsRetryable(e) {
			t.Errorf("status %d must be retryable", status)
		}
	}
	for _, status := range []int{400, 401, 402, 403, 404, 422} {
		if e := newAPIError("p", fakeResp(status, nil), nil); e.Retryable {
			t.Errorf("status %d must not be retryable", status)
		}
	}
}

func TestNewAPIError_Predicates(t *testing.T) {
	if !IsRateLimited(newAPIError("openai", fakeResp(429, nil), []byte("slow down"))) {
		t.Error("429 → IsRateLimited")
	}
	if !IsOverloaded(newAPIError("anthropic", fakeResp(529, nil), []byte("overloaded"))) {
		t.Error("529 → IsOverloaded")
	}
	e402 := newAPIError("openai", fakeResp(402, nil), []byte("payment required"))
	if !IsInsufficientBalance(e402) || !IsFatalAPIError(e402) {
		t.Error("402 → insufficient balance, fatal")
	}
	if !IsCapacityError(newAPIError("ollama", fakeResp(503, nil), nil)) {
		t.Error("503 → capacity")
	}
	if IsFatalAPIError(newAPIError("p", fakeResp(500, nil), nil)) {
		t.Error("500 is not fatal")
	}
}

func TestNewAPIError_RetryAfter(t *testing.T) {
	if e := newAPIError("p", fakeResp(429, map[string]string{"Retry-After": "7"}), nil); e.RetryAfter != 7 || RetryAfterSeconds(e) != 7 {
		t.Errorf("seconds form: got %d", e.RetryAfter)
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if e := newAPIError("p", fakeResp(429, map[string]string{"Retry-After": future}), nil); e.RetryAfter < 85 || e.RetryAfter > 91 {
		t.Errorf("HTTP-date form: got %d", e.RetryAfter)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if e := newAPIError("p", fakeResp(429, map[string]string{"Retry-After": past}), nil); e.RetryAfter != 0 {
		t.Errorf("past date must yield 0, got %d", e.RetryAfter)
	}
	if e := newAPIError("p", fakeResp(429, map[string]string{"Retry-After": "soon"}), nil); e.RetryAfter != 0 {
		t.Errorf("garbage must yield 0, got %d", e.RetryAfter)
	}
	if e := newAPIError("p", fakeResp(429, nil), nil); e.RetryAfter != 0 {
		t.Errorf("absent header must yield 0, got %d", e.RetryAfter)
	}
}

func TestNewAPIError_BodySnippetIsBoundedAndRedacted(t *testing.T) {
	long := strings.Repeat("x", 2000)
	e := newAPIError("p", fakeResp(500, nil), []byte(long))
	if len(e.Message) > maxErrorBodyBytes+len("…") {
		t.Errorf("snippet too long: %d", len(e.Message))
	}
	if !strings.HasSuffix(e.Message, "…") {
		t.Error("truncated snippet must be marked")
	}

	leaky := `{"error":"bad key sk-ant-api03-abcdef1234567890abcdef","hint":"Bearer abcdefghijklmnopqrstuvwxyz"}`
	e = newAPIError("p", fakeResp(401, nil), []byte(leaky))
	if strings.Contains(e.Message, "sk-ant-") || strings.Contains(e.Error(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("secret leaked into error: %q", e.Message)
	}
	if !strings.Contains(e.Message, "bad key") {
		t.Fatalf("non-secret text must survive: %q", e.Message)
	}

	if e := newAPIError("p", fakeResp(500, nil), []byte("  \n")); e.Message != "500 Internal Server Error" {
		t.Errorf("empty body falls back to status text, got %q", e.Message)
	}
}

func TestAPIError_ErrorWithoutProvider(t *testing.T) {
	e := &APIError{StatusCode: 503, Message: "busy"}
	if e.Error() != "API error (status 503): busy" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestQuotaError_UnwrapsToAPIError(t *testing.T) {
	api := newAPIError("google", fakeResp(429, map[string]string{"Retry-After": "3"}), []byte("quota"))
	qe := newQuotaError(api)
	if !IsQuotaError(qe) {
		t.Fatal("QuotaError must satisfy IsQuotaError")
	}
	var got *APIError
	if !errors.As(qe, &got) || got.StatusCode != 429 {
		t.Fatal("QuotaError must unwrap to the underlying *APIError")
	}
	if !IsRateLimited(qe) || RetryAfterSeconds(qe) != 3 || !IsRetryable(qe) {
		t.Error("predicates must see through QuotaError")
	}
	if qe.StatusCode != 429 || !strings.Contains(qe.Error(), "quota exhausted (HTTP 429)") {
		t.Errorf("legacy fields/message: %+v %q", qe, qe.Error())
	}
	if (&QuotaError{StatusCode: 429}).Unwrap() != nil {
		t.Error("QuotaError without an API error unwraps to nil")
	}
}
