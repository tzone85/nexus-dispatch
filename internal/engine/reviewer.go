package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ReviewComment represents a single code review comment.
type ReviewComment struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"` // "critical", "major", "minor", "info"
	Comment  string `json:"comment"`
}

// ReviewResult holds the outcome of a code review.
type ReviewResult struct {
	Passed   bool            `json:"passed"`
	Comments []ReviewComment `json:"comments"`
	Summary  string          `json:"summary"`
}

// DefaultMaxDiffBytes is the default byte budget for the diff interpolated
// into the reviewer prompt (config: review.max_diff_bytes).
const DefaultMaxDiffBytes = 200 * 1024

// NoStructuredVerdictFeedback is the summary returned when the reviewer model
// produced neither a tool call nor parseable JSON. The gate fails closed: a
// verdict that cannot be parsed is not a pass.
const NoStructuredVerdictFeedback = "reviewer returned no structured verdict"

// Reviewer performs AI-powered code review on story branch diffs using the
// Senior model.
type Reviewer struct {
	llmClient    llm.Client
	eventStore   state.EventStore
	projStore    state.ProjectionStore
	provider     string
	model        string
	maxTokens    int
	maxDiffBytes int
	// attemptID stamps the review events (see ForAttempt); empty = unstamped.
	attemptID string
}

// NewReviewer creates a Reviewer wired to the given LLM client, model
// configuration, event store, and projection store. The provider parameter
// is used to determine whether the model supports native tool calling.
func NewReviewer(client llm.Client, provider, model string, maxTokens int, es state.EventStore, ps state.ProjectionStore) *Reviewer {
	return &Reviewer{
		llmClient:    client,
		eventStore:   es,
		projStore:    ps,
		provider:     provider,
		model:        model,
		maxTokens:    maxTokens,
		maxDiffBytes: DefaultMaxDiffBytes,
	}
}

// ForAttempt returns a shallow copy of the reviewer whose STORY_REVIEW_PASSED
// / STORY_REVIEW_FAILED events carry attempt_id, so a stale review of an
// earlier attempt can never be mistaken for the current one. The receiver is
// not modified; the copy shares the client and stores.
func (r *Reviewer) ForAttempt(attemptID string) *Reviewer {
	c := *r
	c.attemptID = attemptID
	return &c
}

// WithMaxDiffBytes sets the diff byte budget (review.max_diff_bytes) and
// returns the reviewer for chaining. Non-positive values keep the default.
func (r *Reviewer) WithMaxDiffBytes(n int) *Reviewer {
	if n > 0 {
		r.maxDiffBytes = n
	}
	return r
}

// DiffTruncationMarker is the explicit marker appended to a capped diff so the
// reviewer model knows how much it did not see.
func DiffTruncationMarker(remaining int) string {
	return fmt.Sprintf("[diff truncated: %d more bytes]", remaining)
}

// TruncateDiffForReview caps diff at maxBytes (DefaultMaxDiffBytes when
// maxBytes <= 0), appending DiffTruncationMarker when it had to cut. The
// second result reports whether truncation happened.
func TruncateDiffForReview(diff string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxDiffBytes
	}
	if len(diff) <= maxBytes {
		return diff, false
	}
	return diff[:maxBytes] + "\n" + DiffTruncationMarker(len(diff)-maxBytes), true
}

// Review takes a story ID, title, acceptance criteria, and the git diff of
// the branch changes. It calls the Senior LLM for code review and emits
// either STORY_REVIEW_PASSED or STORY_REVIEW_FAILED.
//
// When the provider+model supports native tool calling, the LLM is given
// structured reviewer tools. If tool processing fails but the response
// contains parseable JSON text, the text path is attempted without an
// additional LLM call. A separate text-only LLM call is made only when
// the provider does not support tools.
//
// The variadic `extra` parameter accepts up to two
// optional strings — extra[0] is the blast-radius context (existing) and
// extra[1] is the worktree file tree (`git ls-files` output) so the
// reviewer doesn't hallucinate "missing file X" when X is in the repo
// but unchanged.
func (r *Reviewer) Review(ctx context.Context, storyID, title, acceptanceCriteria, diff string, extra ...string) (ReviewResult, error) {
	if diff == "" {
		return ReviewResult{}, fmt.Errorf("empty diff for story %s", storyID)
	}

	// Build optional blast-radius context from codegraph analysis.
	blastRadiusCtx := ""
	if len(extra) > 0 && extra[0] != "" {
		blastRadiusCtx = "\n" + extra[0] + "\n"
	}

	// Optional worktree file tree.
	fileTreeCtx := ""
	if len(extra) > 1 && extra[1] != "" {
		fileTreeCtx = "\n\nWorktree files (git ls-files):\n" + extra[1] + "\n"
	}

	diff, truncated := TruncateDiffForReview(diff, r.maxDiffBytes)
	truncationNote := ""
	if truncated {
		truncationNote = "\nNOTE: the diff was truncated to fit the review budget; the marker at its end says how many bytes were cut. Review what is shown and record the truncation in your summary — do not assume the unseen part is correct.\n"
	}

	prompt := fmt.Sprintf(`Review this code change for the following story:

Story: %s
Acceptance Criteria: %s
%s%s
Diff:%s
%s

Review the code for:
1. Correctness - does it meet the acceptance criteria?
2. Code quality - clean, readable, well-structured?
3. Test coverage - are changes tested?
4. Security - any vulnerabilities?
5. Performance - any obvious issues?
6. Blast radius - if blast radius analysis is provided above, check whether high-risk callers or dependents might break.

Do NOT claim a file is missing if it appears in the worktree files list above.

Verdict policy: set passed=true when the acceptance criteria are met and
there are no critical or major defects (bugs, security holes, broken
builds or tests, unmet criteria). Record minor and info-level findings
(naming, comments, style, polish, nice-to-haves) as review comments on a
PASSING review — never reject solely for minor or info findings. A diff
that is only placeholders or stubs with no real implementation does NOT
meet acceptance criteria and must be rejected.`, title, acceptanceCriteria, blastRadiusCtx, fileTreeCtx, truncationNote, diff)

	systemPrompt := "You are a Senior code reviewer. Review code changes and provide structured feedback."

	var result ReviewResult
	var reviewErr error

	if llm.HasToolSupport(r.provider, r.model) {
		result, reviewErr = r.reviewWithTools(ctx, systemPrompt, prompt)
	} else {
		result, reviewErr = r.reviewWithText(ctx, systemPrompt, prompt)
	}

	if reviewErr != nil {
		return ReviewResult{}, reviewErr
	}

	// Emit appropriate event
	eventType := state.EventStoryReviewPassed
	if !result.Passed {
		eventType = state.EventStoryReviewFailed
	}

	evt := state.NewEventForAttempt(eventType, "reviewer", storyID, r.attemptID, map[string]any{
		"passed":        result.Passed,
		"comment_count": len(result.Comments),
		"summary":       result.Summary,
	})
	if err := r.eventStore.Append(evt); err != nil {
		return result, fmt.Errorf("emit review event: %w", err)
	}
	if err := r.projStore.Project(evt); err != nil {
		return result, fmt.Errorf("project review event: %w", err)
	}

	return result, nil
}

// reviewWithTools performs a review using native tool calling. The LLM is
// given the reviewer tool definitions and required to call submit_review.
// If the response contains no tool calls but has text content, the text
// is parsed as a legacy JSON review result as a fallback.
func (r *Reviewer) reviewWithTools(ctx context.Context, systemPrompt, userPrompt string) (ReviewResult, error) {
	resp, err := r.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:      r.model,
		MaxTokens:  r.maxTokens,
		System:     systemPrompt,
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: userPrompt}},
		Tools:      ReviewerTools(),
		ToolChoice: "required",
	})
	if err != nil {
		return ReviewResult{}, fmt.Errorf("reviewer LLM call (tools): %w", err)
	}

	// Process tool calls from the response.
	toolCalls := resp.ToolCalls
	if len(toolCalls) == 0 {
		// Some providers may return tool calls embedded in text.
		parsed, parseErr := llm.ParseToolCallsFromText(resp.Content)
		if parseErr == nil && len(parsed) > 0 {
			toolCalls = parsed
		}
	}

	if len(toolCalls) > 0 {
		toolResult, toolErr := ProcessReviewerToolCalls(toolCalls)
		if toolErr == nil {
			// If the reviewer requested more context, treat it as a non-fatal
			// pass-through.
			if toolResult.ContextRequest != nil {
				log.Printf("[reviewer] context requested for files: %v (reason: %s)",
					toolResult.ContextRequest.Files, toolResult.ContextRequest.Reason)
				return ReviewResult{
					Passed:  false,
					Summary: fmt.Sprintf("Review incomplete: additional context needed (%s)", toolResult.ContextRequest.Reason),
				}, nil
			}
			return convertToolResultToReviewResult(toolResult), nil
		}
		log.Printf("[reviewer] tool call processing failed, trying text fallback: %v", toolErr)
	}

	// Fallback: try parsing the response text as a legacy JSON review.
	if resp.Content != "" {
		var result ReviewResult
		cleaned := extractJSON(resp.Content)
		if jsonErr := json.Unmarshal([]byte(cleaned), &result); jsonErr == nil {
			log.Printf("[reviewer] used text fallback from tool response")
			return result, nil
		}

		// Fail closed: prose without a tool call or JSON verdict is not a pass.
		// A substring scan for "fail"/"reject" let any non-committal reply
		// through the gate; the model must produce a structured verdict.
		log.Printf("[reviewer] model returned plain text with no structured verdict; failing closed")
		return noStructuredVerdict(), nil
	}

	return ReviewResult{}, fmt.Errorf("reviewer: no tool calls and empty response")
}

// reviewWithText performs a review using the legacy JSON text parsing path.
func (r *Reviewer) reviewWithText(ctx context.Context, systemPrompt, userPrompt string) (ReviewResult, error) {
	textPrompt := userPrompt + `

Respond with JSON:
{
  "passed": true/false,
  "comments": [{"file": "path", "line": 0, "severity": "critical|major|minor|info", "comment": "..."}],
  "summary": "brief summary"
}`

	resp, err := r.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     r.model,
		MaxTokens: r.maxTokens,
		System:    systemPrompt + " Respond only with JSON.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: textPrompt}},
	})
	if err != nil {
		return ReviewResult{}, fmt.Errorf("reviewer LLM call: %w", err)
	}

	var result ReviewResult
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		log.Printf("[reviewer] unparseable review response (%v); failing closed", err)
		return noStructuredVerdict(), nil
	}

	return result, nil
}

// noStructuredVerdict is the fail-closed result for an unparseable reply.
func noStructuredVerdict() ReviewResult {
	return ReviewResult{Passed: false, Summary: NoStructuredVerdictFeedback}
}

// convertToolResultToReviewResult maps a ReviewToolResult to the existing
// ReviewResult type used by the rest of the pipeline.
func convertToolResultToReviewResult(tr ReviewToolResult) ReviewResult {
	passed := tr.Verdict == "approve"

	comments := make([]ReviewComment, len(tr.FileComments))
	for i, fc := range tr.FileComments {
		comments[i] = ReviewComment{
			File:     fc.File,
			Line:     fc.Line,
			Severity: fc.Severity,
			Comment:  fc.Message,
		}
	}

	return ReviewResult{
		Passed:   passed,
		Comments: comments,
		Summary:  tr.Summary,
	}
}
