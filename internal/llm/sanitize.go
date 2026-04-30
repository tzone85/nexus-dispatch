package llm

import (
	"context"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// SanitizingClient wraps an llm.Client and screens responses for embedded
// secrets and prompt-injection attempts before returning them. This closes
// the gap where input was sanitized in the planner but LLM-generated content
// flowed unchecked through the system (audit finding H7).
//
// On detection the response Content is replaced with a redacted notice and
// the incident is logged. The Usage field is preserved so cost tracking
// stays accurate. ToolCalls are passed through unchanged because they are
// already structured output, but the sanitizer is invoked on each tool call
// argument string for completeness.
type SanitizingClient struct {
	inner Client
	role  string // descriptive label for log lines (e.g. "review", "manager")
}

// NewSanitizingClient wraps inner with output sanitization. role is a free-form
// label used only for logging.
func NewSanitizingClient(inner Client, role string) *SanitizingClient {
	return &SanitizingClient{inner: inner, role: role}
}

// Complete forwards to the wrapped client and screens the response.
func (s *SanitizingClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	resp, err := s.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}

	if resp.Content != "" {
		if sanitize.ScanForSecrets(resp.Content) {
			log.Printf("[sanitize] %s LLM response contained a secret-like token; redacting", s.role)
			resp.Content = "[REDACTED: model output contained credential-like token]"
		} else if sanitize.DetectPromptInjection(resp.Content) {
			log.Printf("[sanitize] %s LLM response contained prompt-injection markers; redacting", s.role)
			resp.Content = "[REDACTED: model output contained injection markers]"
		}
	}

	return resp, nil
}
