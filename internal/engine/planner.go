// Package engine implements the NXD orchestration pipeline:
// Requirement -> Plan -> Dispatch -> Execute -> Review -> QA -> Merge -> Cleanup.
package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// PlannedStory represents a story decomposed from a requirement by the Tech Lead.
type PlannedStory struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Complexity         int      `json:"complexity"`
	DependsOn          []string `json:"depends_on"`
}

// PlanResult holds the output of a planning session: stories and their
// dependency graph.
type PlanResult struct {
	RequirementID string
	Stories       []PlannedStory
	Graph         *graph.DAG
}

// Planner decomposes a requirement into implementable stories via the
// Tech Lead LLM and emits corresponding domain events.
type Planner struct {
	llmClient  llm.Client
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
}

// NewPlanner creates a Planner wired to the given LLM client, configuration,
// event store, and projection store.
func NewPlanner(client llm.Client, cfg config.Config, es state.EventStore, ps state.ProjectionStore) *Planner {
	return &Planner{
		llmClient:  client,
		config:     cfg,
		eventStore: es,
		projStore:  ps,
	}
}

// Plan takes a requirement and produces decomposed stories with a dependency
// graph. It emits REQ_SUBMITTED, STORY_CREATED (per story), and REQ_PLANNED
// events.
func (p *Planner) Plan(ctx context.Context, reqID, requirement, repoPath string) (PlanResult, error) {
	// Emit requirement submitted
	reqPayload := map[string]any{
		"id":          reqID,
		"title":       requirement,
		"description": requirement,
	}
	if err := p.emitAndProject(state.EventReqSubmitted, "system", "", reqPayload); err != nil {
		return PlanResult{}, fmt.Errorf("emit req submitted: %w", err)
	}

	// Scan repo for tech stack
	stack := nxdgit.ScanRepo(repoPath)

	// Build Tech Lead prompt
	promptCtx := agent.PromptContext{
		RepoPath:  repoPath,
		TechStack: fmt.Sprintf("%s (%s)", stack.Language, stack.BuildTool),
	}
	systemPrompt := agent.SystemPrompt(agent.RoleTechLead, promptCtx)

	userMessage := fmt.Sprintf(`Decompose this requirement into atomic, implementable stories:

Requirement: %s

Respond with a JSON array of stories. Each story must have:
- id: short identifier (e.g., "s-001")
- title: brief title
- description: what to implement
- acceptance_criteria: how to verify it's done
- complexity: Fibonacci score (1, 2, 3, 5, 8, 13)
- depends_on: array of story IDs this depends on (empty if none)

Respond ONLY with the JSON array, no other text.`, requirement)

	// Call Tech Lead
	resp, err := p.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     p.config.Models.TechLead.Model,
		MaxTokens: p.config.Models.TechLead.MaxTokens,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: userMessage}},
	})
	if err != nil {
		return PlanResult{}, fmt.Errorf("tech lead planning: %w", err)
	}

	// Parse stories from response
	var stories []PlannedStory
	if err := json.Unmarshal([]byte(resp.Content), &stories); err != nil {
		return PlanResult{}, fmt.Errorf("parse stories: %w (response: %s)", err, resp.Content)
	}

	// Build dependency graph
	dag := graph.New()
	for _, s := range stories {
		dag.AddNode(s.ID)
	}
	for _, s := range stories {
		for _, dep := range s.DependsOn {
			dag.AddEdge(s.ID, dep)
		}
	}

	// Validate no cycles
	if _, err := dag.TopologicalSort(); err != nil {
		return PlanResult{}, fmt.Errorf("dependency cycle: %w", err)
	}

	// Emit events for each story
	for _, s := range stories {
		storyPayload := map[string]any{
			"id":                  s.ID,
			"req_id":              reqID,
			"title":               s.Title,
			"description":         s.Description,
			"acceptance_criteria": s.AcceptanceCriteria,
			"complexity":          s.Complexity,
		}
		if err := p.emitAndProject(state.EventStoryCreated, "tech-lead", s.ID, storyPayload); err != nil {
			return PlanResult{}, fmt.Errorf("emit story created %s: %w", s.ID, err)
		}
	}

	// Emit requirement planned
	if err := p.eventStore.Append(state.NewEvent(state.EventReqPlanned, "tech-lead", "", map[string]any{
		"id": reqID,
	})); err != nil {
		return PlanResult{}, fmt.Errorf("emit req planned: %w", err)
	}

	return PlanResult{
		RequirementID: reqID,
		Stories:       stories,
		Graph:         dag,
	}, nil
}

// emitAndProject appends an event to the event store and projects it.
func (p *Planner) emitAndProject(eventType state.EventType, agentID, storyID string, payload map[string]any) error {
	evt := state.NewEvent(eventType, agentID, storyID, payload)
	if err := p.eventStore.Append(evt); err != nil {
		return err
	}
	return p.projStore.Project(evt)
}
