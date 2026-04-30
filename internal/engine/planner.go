// Package engine implements the NXD orchestration pipeline:
// Requirement -> Plan -> Dispatch -> Execute -> Review -> QA -> Merge -> Cleanup.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/repolearn"
	"github.com/tzone85/nexus-dispatch/internal/sanitize"
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
	reqCtx     *RequirementContext
	projectDir string // path to project state dir (for loading RepoProfile)
}

// SetProjectDir sets the project state directory for loading RepoProfile.
func (p *Planner) SetProjectDir(dir string) {
	p.projectDir = dir
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

// PlanWithContext sets the requirement context on the planner and delegates
// to Plan. The context supplies classification flags and an optional
// investigation report that are injected into the Tech Lead prompt.
func (p *Planner) PlanWithContext(ctx context.Context, reqID, requirement, repoPath string, reqCtx RequirementContext) (PlanResult, error) {
	p.reqCtx = &reqCtx
	return p.Plan(ctx, reqID, requirement, repoPath)
}

// Plan takes a requirement and produces decomposed stories with a dependency
// graph. It emits REQ_SUBMITTED, STORY_CREATED (per story), and REQ_PLANNED
// events.
func (p *Planner) Plan(ctx context.Context, reqID, requirement, repoPath string) (PlanResult, error) {
	// Validate requirement before sending to LLM
	if sanitize.DetectPromptInjection(requirement) {
		return PlanResult{}, fmt.Errorf("requirement rejected: prompt injection detected")
	}
	if sanitize.ScanForSecrets(requirement) {
		return PlanResult{}, fmt.Errorf("requirement rejected: embedded secret detected — remove credentials before submitting")
	}

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

	// Scan repo for tech stack — prefer RepoProfile if available
	var techStackStr string
	var profileContext string
	if p.projectDir != "" {
		if profile, err := repolearn.LoadProfile(p.projectDir); err == nil && profile.TechStack.PrimaryLanguage != "" {
			techStackStr = fmt.Sprintf("%s (%s)", profile.TechStack.PrimaryLanguage, profile.TechStack.PrimaryBuildTool)
			profileContext = profile.Summary()
		}
	}
	if techStackStr == "" {
		stack := nxdgit.ScanRepo(repoPath)
		techStackStr = fmt.Sprintf("%s (%s)", stack.Language, stack.BuildTool)
	}

	// Build Tech Lead prompt
	promptCtx := agent.PromptContext{
		RepoPath:  repoPath,
		TechStack: techStackStr,
	}

	// Inject classification flags and investigation report from requirement context
	if p.reqCtx != nil {
		promptCtx.IsExistingCodebase = p.reqCtx.IsExisting
		promptCtx.IsBugFix = p.reqCtx.IsBugFix
		promptCtx.IsRefactor = p.reqCtx.IsRefactor
		promptCtx.IsInfrastructure = p.reqCtx.IsInfra
		if p.reqCtx.Report != nil {
			reportJSON, _ := json.Marshal(p.reqCtx.Report)
			promptCtx.InvestigationReport = fmt.Sprintf("## Codebase Investigation Report\n\n```json\n%s\n```", string(reportJSON))
		}
	}

	systemPrompt := agent.SystemPrompt(agent.RoleTechLead, promptCtx)

	// Inject DDD+TDD methodology guidance unless the requirement explicitly
	// opts out (`methodology: relaxed|none|...`). The opt-out only takes
	// effect when config.methodology.allow_override is true.
	methodology := buildMethodologyDirective(p.config.Methodology, requirement)
	if methodology != "" {
		systemPrompt += "\n\n" + methodology
	}

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
- Every story MUST produce code changes (new files or modifications to existing files). Do NOT create read-only "assessment" or "analysis" stories — agents that produce no code changes will be marked as failed.
- For existing codebases: skip scaffolding. The first story should write actual code.
- For new projects: the first story (s-001) should create the directory structure and initial files (e.g. go.mod, package.json, README.md, base directories).
- For new projects: ALL OTHER stories MUST list s-001 in their depends_on. They cannot run until project setup is in place. Failing to chain dependencies here will cause every parallel agent to fail because shared scaffolding does not yet exist.
- Stories that depend on output from another story (e.g. consume a type defined in another story) MUST list that story in their depends_on.
- All stories MUST reference specific file paths in their descriptions.
- Distribute work across different files to minimize merge conflicts between parallel agents.
- Each file path MUST appear in exactly ONE story's owned_files — no overlapping file ownership between stories.
- Use explicit relative paths from the project root (e.g., "src/api/handler.go", not just "handler.go").
- Keep story complexity at or below %d.
- For simple requirements (1-2 files), prefer fewer stories (1-2) over many small ones.

Respond ONLY with the JSON array, no other text.`, requirement, p.config.Planning.MaxStoryComplexity)

	// Append repo profile context if available from learning system
	if profileContext != "" {
		userMessage += fmt.Sprintf(`

Repository Profile (pre-analysed):
%s
Use this profile to inform your story decomposition. Reference the correct
build/test/lint commands in acceptance criteria. Account for the detected
architecture and conventions when planning stories.`, profileContext)
	}

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

	// Ensure all stories have IDs. Smaller models sometimes omit them.
	for i, s := range stories {
		if s.ID == "" {
			stories[i].ID = fmt.Sprintf("s-%03d", i+1)
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
		// Reject duplicate story IDs — LLM hallucination would silently drop stories.
		if _, exists := idMap[s.ID]; exists {
			return PlanResult{}, fmt.Errorf("LLM returned duplicate story ID: %s", s.ID)
		}
		newID := prefix + "-" + s.ID
		idMap[s.ID] = newID
		stories[i].ID = newID
	}
	// Validate depends_on references before remapping — a nonexistent reference
	// creates a dangling DAG edge that makes the story permanently undispatchable.
	for _, s := range stories {
		for _, dep := range s.DependsOn {
			if _, ok := idMap[dep]; !ok {
				return PlanResult{}, fmt.Errorf("story %s has depends_on reference to nonexistent story %s", s.ID, dep)
			}
		}
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

	// Live-test discovery: small models often return greenfield plans with
	// every depends_on empty, so all stories race in parallel and all fail
	// because scaffolding (go.mod, package.json) does not exist yet. If the
	// plan has 3+ stories AND zero declared dependencies AND looks like a
	// greenfield project (first story title mentions "setup"/"init"/"scaffold"),
	// auto-chain stories 2..N to depend on story 1 and log a warning.
	if len(stories) >= 3 {
		hasAnyDep := false
		for _, s := range stories {
			if len(s.DependsOn) > 0 {
				hasAnyDep = true
				break
			}
		}
		if !hasAnyDep {
			firstTitle := strings.ToLower(stories[0].Title + " " + stories[0].Description)
			if strings.Contains(firstTitle, "setup") || strings.Contains(firstTitle, "init") || strings.Contains(firstTitle, "scaffold") || strings.Contains(firstTitle, "structure") {
				log.Printf("[planner] greenfield plan with zero dependencies detected — auto-chaining stories 2..%d to depend on %s (override the LLM)", len(stories), stories[0].ID)
				firstID := stories[0].ID
				for i := 1; i < len(stories); i++ {
					stories[i].DependsOn = append(stories[i].DependsOn, firstID)
				}
			}
		}
	}

	// TDD enforcement: when methodology.tdd is on (default), every story
	// that owns at least one source-code file MUST also own a matching
	// test file. We log a warning and let the agent fix it during the
	// inner loop instead of hard-failing the whole plan, since some
	// stories legitimately touch only config / non-code files.
	methDecision := ResolveMethodology(p.config.Methodology, requirement)
	if methDecision.TDD {
		warned := false
		for _, s := range stories {
			if !storyOwnsCodeWithoutTest(s) {
				continue
			}
			if !warned {
				log.Printf("[planner] TDD enforcement: stories owning source files SHOULD also own a test file; flagging:")
				warned = true
			}
			log.Printf("[planner]   - %s (%s) owns code without a paired test file", s.ID, s.Title)
		}
	}

	// Validate no overlapping file ownership between independent stories.
	// Stories with a dependency chain (sequential execution) MAY share files
	// since they won't run in parallel. Only flag conflicts between stories
	// that could execute concurrently.
	depSet := make(map[string]map[string]bool) // story -> set of all dependencies (transitive)
	for _, s := range stories {
		depSet[s.ID] = make(map[string]bool)
		for _, d := range s.DependsOn {
			depSet[s.ID][d] = true
		}
	}
	fileOwner := make(map[string]string)
	for _, s := range stories {
		for _, f := range s.OwnedFiles {
			if owner, exists := fileOwner[f]; exists {
				// Allow if one depends on the other (sequential execution)
				if depSet[s.ID][owner] || depSet[owner][s.ID] {
					continue
				}
				log.Printf("[planner] warning: file %s claimed by %s and %s (no dependency chain)", f, owner, s.ID)
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
	// Look up original context to prevent hallucinated sub-stories.
	var reqTitle, storyTitle, storyDesc string
	if req, err := p.projStore.GetRequirement(reqID); err == nil {
		reqTitle = req.Title
	}
	if story, err := p.projStore.GetStory(storyID); err == nil {
		storyTitle = story.Title
		storyDesc = story.Description
	}

	prompt := fmt.Sprintf(`A story has failed multiple times and needs re-planning.

Original Requirement: %s
Story ID: %s
Story Title: %s
Story Description: %s
Failure Context:
%s

CRITICAL CONSTRAINTS:
- Sub-stories MUST be directly related to the original requirement and story above.
- Do NOT introduce new features, tools, or concepts not mentioned in the requirement.
- Every sub-story MUST produce code changes (no read-only analysis stories).
- Sub-stories should be smaller pieces of the SAME work, not different work.

Decompose this into 2-3 smaller, more focused replacement stories.
Return a JSON array of story objects with fields: id, title, description, acceptance_criteria, complexity, owned_files.
Each story ID should use the parent ID as prefix with a letter suffix (e.g., "%s-a", "%s-b").
Keep complexity at or below 5.

Respond ONLY with the JSON array, no other text.`, reqTitle, storyID, storyTitle, storyDesc, failureContext, storyID, storyID)

	model := p.config.Models.TechLead
	resp, err := p.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     model.Model,
		MaxTokens: model.MaxTokens,
		System:    "You are a technical lead re-planning a failed story. Break it into smaller pieces of the SAME work. Stay strictly within the scope of the original requirement. Do NOT introduce unrelated features.",
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

	if len(stories) == 0 {
		return nil, fmt.Errorf("replan produced no sub-stories")
	}

	var valid []PlannedStory
	for _, s := range stories {
		if len(s.OwnedFiles) == 0 && s.Description == "" {
			log.Printf("[replan] skipping empty sub-story %s", s.ID)
			continue
		}
		valid = append(valid, s)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("replan produced no valid sub-stories (all filtered)")
	}

	return valid, nil
}

// parseStoriesFromText extracts PlannedStory values from a plain-text JSON
// response. This is the fallback path for models that do not support native
// tool calling.
//
// Live-test discovery: small models (Gemma) return owned_files / depends_on
// as a comma-separated string instead of an array, so we parse via a lenient
// intermediate struct using FlexibleStringSlice and then convert.
func parseStoriesFromText(content string) ([]PlannedStory, error) {
	type lenientStory struct {
		ID                 string              `json:"id"`
		Title              string              `json:"title"`
		Description        string              `json:"description"`
		AcceptanceCriteria FlexibleString      `json:"acceptance_criteria"`
		Complexity         int                 `json:"complexity"`
		DependsOn          FlexibleStringSlice `json:"depends_on"`
		OwnedFiles         FlexibleStringSlice `json:"owned_files"`
		WaveHint           string              `json:"wave_hint"`
	}
	var lenient []lenientStory
	cleaned := extractJSON(content)
	if err := json.Unmarshal([]byte(cleaned), &lenient); err != nil {
		return nil, fmt.Errorf("parse stories: %w (response: %s)", err, content)
	}
	stories := make([]PlannedStory, len(lenient))
	for i, l := range lenient {
		stories[i] = PlannedStory{
			ID:                 l.ID,
			Title:              l.Title,
			Description:        l.Description,
			AcceptanceCriteria: l.AcceptanceCriteria,
			Complexity:         l.Complexity,
			DependsOn:          []string(l.DependsOn),
			OwnedFiles:         []string(l.OwnedFiles),
			WaveHint:           l.WaveHint,
		}
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
