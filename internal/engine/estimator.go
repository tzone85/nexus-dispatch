package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// EstimateOptions controls estimate behavior.
type EstimateOptions struct {
	Quick        bool
	RateOverride float64
	Save         bool
	Project      string
}

// Estimator orchestrates the plan-to-cost pipeline.
type Estimator struct {
	llmClient  llm.Client
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
}

// NewEstimator creates a new Estimator.
// For quick mode, llmClient, eventStore, and projStore can be nil.
func NewEstimator(client llm.Client, cfg config.Config, es state.EventStore, ps state.ProjectionStore) *Estimator {
	return &Estimator{
		llmClient:  client,
		config:     cfg,
		eventStore: es,
		projStore:  ps,
	}
}

// Estimate produces a cost estimate for a requirement.
func (e *Estimator) Estimate(ctx context.Context, requirement, repoPath string, opts EstimateOptions) (Estimate, error) {
	var stories []StoryEstimate

	if opts.Quick {
		stories = QuickEstimate(requirement)
	} else {
		planned, err := e.planStories(ctx, requirement, repoPath)
		if err != nil {
			return Estimate{}, fmt.Errorf("planning: %w", err)
		}
		stories = e.mapToStoryEstimates(planned)
	}

	est := CalculateCost(stories, e.config.Billing, opts.RateOverride)
	est.Requirement = requirement
	est.Project = opts.Project
	est.IsQuick = opts.Quick
	est.EstimateID = generateEstimateID()

	if opts.Save && e.eventStore != nil {
		if err := e.persistEstimate(est); err != nil {
			return est, fmt.Errorf("saving estimate: %w", err)
		}
	}

	return est, nil
}

func (e *Estimator) planStories(ctx context.Context, requirement, repoPath string) ([]PlannedStory, error) {
	planner := NewPlanner(e.llmClient, e.config, e.eventStore, e.projStore)
	reqID := fmt.Sprintf("est-%s", time.Now().Format("20060102-150405"))
	result, err := planner.Plan(ctx, reqID, requirement, repoPath)
	if err != nil {
		return nil, err
	}
	return result.Stories, nil
}

func (e *Estimator) mapToStoryEstimates(planned []PlannedStory) []StoryEstimate {
	stories := make([]StoryEstimate, len(planned))
	for i, p := range planned {
		role := agent.RouteByComplexity(p.Complexity, e.config.Routing)
		stories[i] = StoryEstimate{
			Title:      p.Title,
			Complexity: p.Complexity,
			Role:       string(role),
		}
	}
	return stories
}

func (e *Estimator) persistEstimate(est Estimate) error {
	payload := map[string]any{
		"estimate_id":  est.EstimateID,
		"requirement":  est.Requirement,
		"is_quick":     est.IsQuick,
		"stories":      est.Summary.StoryCount,
		"total_points": est.Summary.TotalPoints,
		"hours_low":    est.Summary.HoursLow,
		"hours_high":   est.Summary.HoursHigh,
		"quote_low":    est.Summary.QuoteLow,
		"quote_high":   est.Summary.QuoteHigh,
		"llm_cost":     est.Summary.LLMCost,
		"rate":         est.Summary.Rate,
		"currency":     est.Summary.Currency,
		"project":      est.Project,
	}
	event := state.NewEvent(state.EventReqEstimated, "estimator", "", payload)
	return e.eventStore.Append(event)
}

func generateEstimateID() string {
	return fmt.Sprintf("est-%s", time.Now().Format("20060102-150405"))
}
