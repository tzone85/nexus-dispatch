package llm

import (
	"context"
	"encoding/json"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// SanitizingClient wraps an llm.Client and screens responses for embedded
// secrets and prompt-injection attempts before returning them. This closes
// the gap where input was sanitized in the planner but LLM-generated content
// flowed unchecked through the system (audit finding H7).
//
// On detection only the matched span is redacted (the rest of the content is
// preserved so a single credential-like token no longer destroys an entire
// plan or review) and the incident is logged. Both resp.Content and every
// tool-call argument string are scanned; Usage is preserved so cost tracking
// stays accurate.
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

	resp.Content = s.redact("content", resp.Content)

	if len(resp.ToolCalls) > 0 {
		calls := make([]ToolCall, len(resp.ToolCalls))
		copy(calls, resp.ToolCalls)
		for i := range calls {
			args := string(calls[i].Arguments)
			if cleaned := s.redact("tool call "+calls[i].Name+" arguments", args); cleaned != args {
				calls[i].Arguments = json.RawMessage(cleaned)
			}
		}
		resp.ToolCalls = calls
	}

	return resp, nil
}

// redact returns text with secret and injection spans replaced, logging each
// kind of finding once per field.
func (s *SanitizingClient) redact(field, text string) string {
	if text == "" {
		return text
	}
	if sanitize.ScanForSecrets(text) {
		log.Printf("[sanitize] %s LLM response %s contained a secret-like token; redacting span", s.role, field)
		text = sanitize.RedactSecrets(text)
	}
	if sanitize.DetectPromptInjection(text) {
		log.Printf("[sanitize] %s LLM response %s contained prompt-injection markers; redacting span", s.role, field)
		text = sanitize.RedactPromptInjection(text)
	}
	return text
}
