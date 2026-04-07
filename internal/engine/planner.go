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
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	AcceptanceCriteria FlexibleString `json:"acceptance_criteria"`
	Complexity         int            `json:"complexity"`
	DependsOn          []string       `json:"depends_on"`
	OwnedFiles         []string       `json:"owned_files"`
	WaveHint           string         `json:"wave_hint"`
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
		"repo_path":   repoPath,
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
- description: what to implement, including exact file paths (e.g., "Create src/models/user.js")
- acceptance_criteria: how to verify it's done
- complexity: Fibonacci score (1, 2, 3, 5, 8, 13)
- depends_on: array of story IDs this depends on (empty if none)
- owned_files: array of exact file paths this story will create or modify (e.g., ["src/models/user.go", "src/models/user_test.go"])
- wave_hint: either "sequential" or "parallel" — use "sequential" for stories that modify shared config, lock files, or core infrastructure

IMPORTANT:
- The first story (s-001) MUST establish the project directory structure and scaffold empty placeholder files.
- All subsequent stories MUST reference specific file paths in their descriptions.
- Distribute work across different files to minimize merge conflicts between parallel agents.
- Each file path MUST appear in exactly ONE story's owned_files — no overlapping file ownership between stories.
- Use explicit relative paths from the project root (e.g., "src/api/handler.go", not just "handler.go").
- Keep story complexity at or below %d.

Respond ONLY with the JSON array, no other text.`, requirement, p.config.Planning.MaxStoryComplexity)

	// Build the LLM request. If the provider supports native tool calling,
	// attach planner tool definitions so the model uses structured output.
	req := llm.CompletionRequest{
		Model:     p.config.Models.TechLead.Model,
		MaxTokens: p.config.Models.TechLead.MaxTokens,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: userMessage}},
	}

	useTools := llm.HasToolSupport(
		p.config.Models.TechLead.Provider,
		p.config.Models.TechLead.Model,
	)
	if useTools {
		req.Tools = PlannerTools()
		req.ToolChoice = "required"
	}

	// Call Tech Lead
	resp, err := p.llmClient.Complete(ctx, req)
	if err != nil {
		return PlanResult{}, fmt.Errorf("tech lead planning: %w", err)
	}

	// Parse stories from the response. Prefer structured tool calls when
	// available; fall back to JSON text parsing otherwise.
	var stories []PlannedStory
	if useTools && len(resp.ToolCalls) > 0 {
		toolResult, toolErr := ProcessPlannerToolCalls(resp.ToolCalls)
		if toolErr == nil && len(toolResult.Stories) > 0 {
			stories = mapToolStories(toolResult.Stories)
		} else {
			// Tool processing failed — fall back to text parsing
			stories, err = parseStoriesFromText(resp.Content)
			if err != nil {
				return PlanResult{}, err
			}
		}
	} else {
		stories, err = parseStoriesFromText(resp.Content)
		if err != nil {
			return PlanResult{}, err
		}
	}

	// Make story IDs globally unique by prefixing with short req ID.
	// LLMs always generate generic IDs like "s-001" which collide across
	// requirements.
	prefix := reqID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	idMap := make(map[string]string, len(stories))
	for i, s := range stories {
		newID := prefix + "-" + s.ID
		idMap[s.ID] = newID
		stories[i].ID = newID
	}
	for i, s := range stories {
		for j, dep := range s.DependsOn {
			if newDep, ok := idMap[dep]; ok {
				stories[i].DependsOn[j] = newDep
			}
		}
	}

	// Validate story complexity
	for _, s := range stories {
		if p.config.Planning.MaxStoryComplexity > 0 && s.Complexity > p.config.Planning.MaxStoryComplexity {
			return PlanResult{}, fmt.Errorf("story %s complexity %d exceeds max %d", s.ID, s.Complexity, p.config.Planning.MaxStoryComplexity)
		}
	}

	// Validate no overlapping file ownership
	fileOwner := make(map[string]string)
	for _, s := range stories {
		for _, f := range s.OwnedFiles {
			if owner, exists := fileOwner[f]; exists {
				return PlanResult{}, fmt.Errorf("file %s claimed by %s and %s", f, owner, s.ID)
			}
			fileOwner[f] = s.ID
		}
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
			"acceptance_criteria": string(s.AcceptanceCriteria),
			"complexity":          s.Complexity,
			"depends_on":          s.DependsOn,
			"owned_files":         s.OwnedFiles,
			"wave_hint":           s.WaveHint,
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

// RePlan takes a single failing story and its failure context, calls the LLM
// to decompose it into smaller replacement stories, emits STORY_CREATED
// events for each replacement, and returns them. Unlike Plan, it does NOT
// emit REQ_SUBMITTED or REQ_PLANNED events -- the caller is responsible for
// emitting STORY_SPLIT and mutating the DAG.
func (p *Planner) RePlan(ctx context.Context, storyID, reqID, failureContext string) ([]PlannedStory, error) {
	prompt := fmt.Sprintf(`A story has failed multiple times and needs re-planning.

Story ID: %s
Requirement ID: %s
Failure Context:
%s

Decompose this into smaller, more focused replacement stories.
Return a JSON array of story objects with fields: id, title, description, acceptance_criteria, complexity, owned_files.
Each story ID should use the parent ID as prefix with a letter suffix (e.g., "%s-a", "%s-b").
Keep complexity at or below 5.

Respond ONLY with the JSON array, no other text.`, storyID, reqID, failureContext, storyID, storyID)

	model := p.config.Models.TechLead
	resp, err := p.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     model.Model,
		MaxTokens: model.MaxTokens,
		System:    "You are a technical lead re-planning a failed story. Break it into smaller, more focused sub-stories that are easier to implement correctly.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("replan LLM call: %w", err)
	}

	var stories []PlannedStory
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &stories); err != nil {
		return nil, fmt.Errorf("parse replan stories: %w (response: %s)", err, resp.Content)
	}

	return stories, nil
}

// parseStoriesFromText extracts PlannedStory values from a plain-text JSON
// response. This is the fallback path for models that do not support native
// tool calling.
func parseStoriesFromText(content string) ([]PlannedStory, error) {
	var stories []PlannedStory
	cleaned := extractJSON(content)
	if err := json.Unmarshal([]byte(cleaned), &stories); err != nil {
		return nil, fmt.Errorf("parse stories: %w (response: %s)", err, content)
	}
	return stories, nil
}

// mapToolStories converts ToolStory values (produced by ProcessPlannerToolCalls)
// into the canonical PlannedStory type used throughout the engine.
func mapToolStories(toolStories []ToolStory) []PlannedStory {
	stories := make([]PlannedStory, len(toolStories))
	for i, ts := range toolStories {
		stories[i] = PlannedStory{
			ID:                 ts.ID,
			Title:              ts.Title,
			Description:        ts.Description,
			AcceptanceCriteria: FlexibleString(ts.AcceptanceCriteria),
			Complexity:         ts.Complexity,
			DependsOn:          ts.DependsOn,
		}
	}
	return stories
}

// emitAndProject appends an event to the event store and projects it.
func (p *Planner) emitAndProject(eventType state.EventType, agentID, storyID string, payload map[string]any) error {
	evt := state.NewEvent(eventType, agentID, storyID, payload)
	if err := p.eventStore.Append(evt); err != nil {
		return err
	}
	return p.projStore.Project(evt)
}
