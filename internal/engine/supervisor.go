package engine

import (
	"context"
	"encoding/json"
	"fmt"

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
	model      string
	maxTokens  int
}

// NewSupervisor creates a Supervisor wired to the given LLM client, model
// configuration, and event store.
func NewSupervisor(client llm.Client, model string, maxTokens int, es state.EventStore) *Supervisor {
	return &Supervisor{
		llmClient:  client,
		eventStore: es,
		model:      model,
		maxTokens:  maxTokens,
	}
}

// Review assesses whether stories are on track to fulfill the original
// requirement. It calls the LLM to evaluate progress and emits either a
// SUPERVISOR_CHECK or SUPERVISOR_DRIFT_DETECTED event.
func (s *Supervisor) Review(ctx context.Context, requirement string, stories []PlannedStory, storyStatuses map[string]string) (SupervisorResult, error) {
	statusSummary := buildStatusSummary(stories, storyStatuses)

	prompt := fmt.Sprintf(`Review the progress of this requirement:

Requirement: %s

Stories and their status:
%s

Assess whether the stories are on track to fulfill the requirement.
Respond with JSON: {"on_track": bool, "concerns": ["..."], "reprioritize": ["story-id", ...]}`, requirement, statusSummary)

	resp, err := s.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     s.model,
		MaxTokens: s.maxTokens,
		System:    "You are a Supervisor reviewing development progress. Respond only with JSON.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return SupervisorResult{}, fmt.Errorf("supervisor review: %w", err)
	}

	var result SupervisorResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return SupervisorResult{}, fmt.Errorf("parse supervisor response: %w", err)
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
