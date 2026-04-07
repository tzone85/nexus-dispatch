package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// SupervisorResult holds the outcome of a periodic drift check.
type SupervisorResult struct {
	OnTrack      bool     `json:"on_track"`
	Concerns     []string `json:"concerns"`
	Reprioritize []string `json:"reprioritize"`
}

// Supervisor performs periodic reviews of requirement progress, detecting
// drift from the original goal.
type Supervisor struct {
	llmClient  llm.Client
	eventStore state.EventStore
	provider   string
	model      string
	maxTokens  int
}

// NewSupervisor creates a Supervisor wired to the given LLM client, model
// configuration, and event store. The provider parameter is used to determine
// whether the model supports native tool calling.
func NewSupervisor(client llm.Client, provider, model string, maxTokens int, es state.EventStore) *Supervisor {
	return &Supervisor{
		llmClient:  client,
		eventStore: es,
		provider:   provider,
		model:      model,
		maxTokens:  maxTokens,
	}
}

// Review assesses whether stories are on track to fulfill the original
// requirement. It calls the LLM to evaluate progress and emits either a
// SUPERVISOR_CHECK or SUPERVISOR_DRIFT_DETECTED event.
//
// When the provider+model supports native tool calling, the LLM is given
// structured supervisor tools. If tool processing fails but the response
// contains parseable JSON text, the text path is attempted without an
// additional LLM call. A separate text-only LLM call is made only when
// the provider does not support tools.
func (s *Supervisor) Review(ctx context.Context, requirement string, stories []PlannedStory, storyStatuses map[string]string) (SupervisorResult, error) {
	statusSummary := buildStatusSummary(stories, storyStatuses)

	prompt := fmt.Sprintf(`Review the progress of this requirement:

Requirement: %s

Stories and their status:
%s

Assess whether the stories are on track to fulfill the requirement.
Respond with JSON: {"on_track": bool, "concerns": ["..."], "reprioritize": ["story-id", ...]}`, requirement, statusSummary)

	systemPrompt := "You are a Supervisor reviewing development progress. Respond only with JSON."

	var result SupervisorResult
	var reviewErr error

	if llm.HasToolSupport(s.provider, s.model) {
		result, reviewErr = s.reviewWithTools(ctx, systemPrompt, prompt)
	} else {
		result, reviewErr = s.reviewWithText(ctx, systemPrompt, prompt)
	}

	if reviewErr != nil {
		return SupervisorResult{}, reviewErr
	}

	eventType := state.EventSupervisorCheck
	if !result.OnTrack {
		eventType = state.EventSupervisorDriftDetected
	}
	s.eventStore.Append(state.NewEvent(eventType, "supervisor", "", map[string]any{
		"on_track": result.OnTrack,
		"concerns": result.Concerns,
	}))

	return result, nil
}

// reviewWithTools performs a supervisor review using native tool calling. The
// LLM is given supervisor tool definitions. If the response contains no tool
// calls but has text content, the text is parsed as a legacy JSON result.
func (s *Supervisor) reviewWithTools(ctx context.Context, systemPrompt, userPrompt string) (SupervisorResult, error) {
	resp, err := s.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:      s.model,
		MaxTokens:  s.maxTokens,
		System:     systemPrompt,
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: userPrompt}},
		Tools:      SupervisorTools(),
		ToolChoice: "required",
	})
	if err != nil {
		return SupervisorResult{}, fmt.Errorf("supervisor review (tools): %w", err)
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
		toolResult, toolErr := ProcessSupervisorToolCalls(toolCalls)
		if toolErr == nil {
			return convertToolResultToSupervisorResult(toolResult), nil
		}
		log.Printf("[supervisor] tool call processing failed, trying text fallback: %v", toolErr)
	}

	// Fallback: try parsing the response text as a legacy JSON result.
	if resp.Content != "" {
		var result SupervisorResult
		cleaned := extractJSON(resp.Content)
		if jsonErr := json.Unmarshal([]byte(cleaned), &result); jsonErr == nil {
			log.Printf("[supervisor] used text fallback from tool response")
			return result, nil
		}
	}

	return SupervisorResult{}, fmt.Errorf("supervisor: no tool calls and text fallback failed")
}

// reviewWithText performs a supervisor review using the legacy JSON text
// parsing path.
func (s *Supervisor) reviewWithText(ctx context.Context, systemPrompt, userPrompt string) (SupervisorResult, error) {
	resp, err := s.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     s.model,
		MaxTokens: s.maxTokens,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: userPrompt}},
	})
	if err != nil {
		return SupervisorResult{}, fmt.Errorf("supervisor review: %w", err)
	}

	var result SupervisorResult
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return SupervisorResult{}, fmt.Errorf("parse supervisor response: %w", err)
	}

	return result, nil
}

// convertToolResultToSupervisorResult maps a SupervisorToolResult to the
// existing SupervisorResult type used by the rest of the pipeline.
func convertToolResultToSupervisorResult(tr SupervisorToolResult) SupervisorResult {
	onTrack := len(tr.Drifts) == 0

	concerns := make([]string, len(tr.Drifts))
	for i, d := range tr.Drifts {
		concerns[i] = fmt.Sprintf("[%s] %s: %s (recommendation: %s)",
			d.Severity, d.StoryID, d.DriftType, d.Recommendation)
	}

	reprioritize := make([]string, len(tr.Reprioritizations))
	for i, r := range tr.Reprioritizations {
		reprioritize[i] = r.StoryID
	}

	return SupervisorResult{
		OnTrack:      onTrack,
		Concerns:     concerns,
		Reprioritize: reprioritize,
	}
}

// buildStatusSummary formats story statuses into a human-readable summary.
func buildStatusSummary(stories []PlannedStory, storyStatuses map[string]string) string {
	summary := ""
	for _, story := range stories {
		status := storyStatuses[story.ID]
		if status == "" {
			status = "pending"
		}
		summary += fmt.Sprintf("- %s: %s (complexity: %d, status: %s)\n",
			story.ID, story.Title, story.Complexity, status)
	}
	return summary
}
