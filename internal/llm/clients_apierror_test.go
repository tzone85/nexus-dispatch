package llm_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// errorServer answers every request with status + body and a Retry-After
// header when retryAfter is non-empty.
func errorServer(t *testing.T, status int, body, retryAfter string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type clientFactory struct {
	provider string
	make     func(baseURL string) llm.Client
}

func allClientFactories() []clientFactory {
	return []clientFactory{
		{"anthropic", func(u string) llm.Client { return llm.NewAnthropicClient("k").WithBaseURL(u) }},
		{"openai", func(u string) llm.Client { return llm.NewOpenAIClient("k").WithBaseURL(u) }},
		{"ollama", func(u string) llm.Client {
			return llm.NewOllamaClient("m", llm.WithOllamaBaseURL(u), llm.WithOllamaRetryBackoff(0))
		}},
		{"google", func(u string) llm.Client { return llm.NewGoogleClient("k", llm.WithGoogleBaseURL(u)) }},
	}
}

func TestClients_NonOKStatusReturnsAPIError(t *testing.T) {
	cases := []struct {
		status     int
		body       string
		retryAfter string
		fatal      bool
		rateLtd    bool
		overloaded bool
		retryable  bool
		balance    bool
	}{
		{status: 401, body: `{"error":"invalid api key sk-ant-api03-abcdef1234567890abcdef"}`, fatal: true},
		{status: 402, body: `{"error":"payment required"}`, fatal: true, balance: true},
		{status: 429, body: `{"error":"rate limited"}`, retryAfter: "12", rateLtd: true, retryable: true},
		{status: 500, body: `{"error":"internal"}`, retryable: true},
		{status: 529, body: `{"error":"overloaded"}`, overloaded: true, retryable: true},
	}
	for _, f := range allClientFactories() {
		for _, tc := range cases {
			t.Run(f.provider+"/"+http.StatusText(tc.status), func(t *testing.T) {
				srv := errorServer(t, tc.status, tc.body, tc.retryAfter)
				_, err := f.make(srv.URL).Complete(context.Background(), llm.CompletionRequest{
					Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				})
				if err == nil {
					t.Fatal("expected error")
				}
				var apiErr *llm.APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("expected *APIError in chain, got %T: %v", err, err)
				}
				if apiErr.StatusCode != tc.status || apiErr.Provider != f.provider {
					t.Errorf("status/provider = %d/%q, want %d/%q", apiErr.StatusCode, apiErr.Provider, tc.status, f.provider)
				}
				if strings.Contains(apiErr.Message, "sk-ant-") || strings.Contains(err.Error(), "sk-ant-") {
					t.Error("secret leaked into error")
				}
				if !strings.Contains(apiErr.Message, "error") {
					t.Errorf("body snippet missing: %q", apiErr.Message)
				}
				if got := llm.IsFatalAPIError(err); got != tc.fatal {
					t.Errorf("IsFatalAPIError = %v, want %v", got, tc.fatal)
				}
				if got := llm.IsRateLimited(err); got != tc.rateLtd {
					t.Errorf("IsRateLimited = %v, want %v", got, tc.rateLtd)
				}
				if got := llm.IsOverloaded(err); got != tc.overloaded {
					t.Errorf("IsOverloaded = %v, want %v", got, tc.overloaded)
				}
				if got := llm.IsRetryable(err); got != tc.retryable {
					t.Errorf("IsRetryable = %v, want %v", got, tc.retryable)
				}
				if got := llm.IsInsufficientBalance(err); got != tc.balance {
					t.Errorf("IsInsufficientBalance = %v, want %v", got, tc.balance)
				}
				if tc.retryAfter != "" && llm.RetryAfterSeconds(err) != 12 {
					t.Errorf("RetryAfterSeconds = %d, want 12", llm.RetryAfterSeconds(err))
				}
			})
		}
	}
}

func TestOllamaClient_NotFoundKeepsHintAndAPIError(t *testing.T) {
	srv := errorServer(t, 404, `{"error":"model not found"}`, "")
	client := llm.NewOllamaClient("gemma4", llm.WithOllamaBaseURL(srv.URL))
	_, err := client.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
		t.Fatalf("expected 404 APIError, got %v", err)
	}
	if !strings.Contains(err.Error(), "ollama pull gemma4") {
		t.Errorf("operator hint lost: %v", err)
	}
	if llm.IsCapacityError(err) {
		t.Error("404 is an operator error, not capacity")
	}
}

func TestGoogleClient_QuotaErrorsWrapAPIError(t *testing.T) {
	for _, status := range []int{429, 403} {
		srv := errorServer(t, status, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, "5")
		client := llm.NewGoogleClient("k", llm.WithGoogleBaseURL(srv.URL))
		_, err := client.Complete(context.Background(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
		if !llm.IsQuotaError(err) {
			t.Fatalf("status %d must remain a QuotaError for FallbackClient, got %v", status, err)
		}
		var apiErr *llm.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Provider != "google" {
			t.Fatalf("status %d must also carry *APIError, got %v", status, err)
		}
		if llm.RetryAfterSeconds(err) != 5 {
			t.Errorf("Retry-After lost: %d", llm.RetryAfterSeconds(err))
		}
	}
}
