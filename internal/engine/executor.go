package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/devdb"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/repolearn"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ActiveAgent tracks a running agent session for the monitor.
type ActiveAgent struct {
	Assignment   Assignment
	WorktreePath string
	RuntimeName  string
	DB           devdb.DB
}

// Executor spawns agents for dispatched assignments by creating git worktrees,
// launching tmux sessions with configured runtimes, and emitting lifecycle events.
type Executor struct {
	registry      *runtime.Registry
	config        config.Config
	eventStore    state.EventStore
	projStore     state.ProjectionStore
	mempalace     *memory.MemPalace
	llmClient     llm.Client // for native runtimes (Gemma)
	artifactStore *artifact.Store
	scratchboard  *scratchboard.Scratchboard
	controller    *Controller
	directives    *DirectiveStore // optional: feeds operator directives into native runtime iterations
	projectDir    string          // path to project state dir (for loading RepoProfile)
	lifecycle     *devdb.Lifecycle

	// nativeWG tracks live native-runtime goroutines so shutdown paths
	// (Ctrl-C in resume, daemon stop) can drain them and let each one
	// flush STORY_COMPLETED before the process exits. Without this, a
	// killed process would leave a story stuck mid-flight; the next
	// `nxd resume` would re-dispatch it and a second goroutine would
	// race against the now-stale worktree.
	nativeWG sync.WaitGroup
}

// SetProjectDir sets the project state directory for loading RepoProfile.
func (e *Executor) SetProjectDir(dir string) {
	e.projectDir = dir
}

// SetDevDBLifecycle wires a devdb Lifecycle for per-story DB provisioning.
func (e *Executor) SetDevDBLifecycle(lc *devdb.Lifecycle) { e.lifecycle = lc }

// HasDevDBLifecycle reports whether a devdb Lifecycle has been configured.
func (e *Executor) HasDevDBLifecycle() bool { return e.lifecycle != nil }

// WaitForNativeShutdown blocks until every in-flight native-runtime goroutine
// has finished, or until timeout elapses, whichever comes first. Returns nil
// on a clean drain and context.DeadlineExceeded if the timeout fired with
// goroutines still running. Safe to call multiple times — sync.WaitGroup is
// reusable. Call from the resume.go shutdown path so STORY_COMPLETED events
// flush before the process exits; the next nxd resume then sees consistent
// state instead of re-dispatching a story whose worktree may still be mutating.
func (e *Executor) WaitForNativeShutdown(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		e.nativeWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

// GetDevDBLifecycle returns the configured devdb Lifecycle, or nil if not set.
func (e *Executor) GetDevDBLifecycle() *devdb.Lifecycle { return e.lifecycle }

// NewExecutor creates an Executor wired to the runtime registry, configuration,
// event store, projection store, and optional MemPalace client. Pass nil for
// mp when MemPalace is not configured.
func NewExecutor(reg *runtime.Registry, cfg config.Config, es state.EventStore, ps state.ProjectionStore, mp *memory.MemPalace) *Executor {
	return &Executor{
		registry:   reg,
		config:     cfg,
		eventStore: es,
		projStore:  ps,
		mempalace:  mp,
	}
}

// SetLLMClient sets the LLM client for native runtime execution.
func (e *Executor) SetLLMClient(client llm.Client) {
	e.llmClient = client
}

// SetArtifactStore sets the artifact store for persisting per-story artifacts.
func (e *Executor) SetArtifactStore(store *artifact.Store) {
	e.artifactStore = store
}

// SetScratchboard sets the shared scratchboard for cross-agent knowledge.
func (e *Executor) SetScratchboard(sb *scratchboard.Scratchboard) {
	e.scratchboard = sb
}

// SetController sets the periodic controller for context cancellation support.
func (e *Executor) SetController(c *Controller) {
	e.controller = c
}

// SetDirectiveStore wires the operator-directive store. When set, the
// native runtime checks for pending directives at the top of each
// iteration and prepends them to the agent's prompt.
func (e *Executor) SetDirectiveStore(d *DirectiveStore) {
	e.directives = d
}

// SpawnResult holds the outcome of spawning an agent for one assignment.
type SpawnResult struct {
	Assignment   Assignment
	WorktreePath string
	RuntimeName  string
	Error        error
	DB           devdb.DB
}

// SpawnAll creates worktrees and launches tmux sessions for each assignment.
// It builds a wave brief from all assignments so each agent knows which
// parallel stories are running and which files to avoid. For native runtimes
// it wraps the LLM client with a concurrency semaphore so that parallel
// agents don't overwhelm a single-GPU Ollama instance.
func (e *Executor) SpawnAll(ctx context.Context, repoDir string, assignments []Assignment, stories map[string]PlannedStory) []SpawnResult {
	// Build wave story info for parallel awareness.
	waveStories := make([]WaveStoryInfo, 0, len(assignments))
	for _, a := range assignments {
		if story, ok := stories[a.StoryID]; ok {
			waveStories = append(waveStories, WaveStoryInfo{
				ID:         a.StoryID,
				Title:      story.Title,
				OwnedFiles: story.OwnedFiles,
			})
		}
	}

	// Build a shared semaphore-wrapped LLM client for native runtimes in
	// this wave. All native goroutines share one semaphore so that at most
	// N concurrent LLM calls proceed (default 1 for single-GPU Ollama).
	nativeClient := e.buildNativeClient()

	results := make([]SpawnResult, 0, len(assignments))
	for _, a := range assignments {
		result := e.spawn(ctx, repoDir, a, stories[a.StoryID], waveStories, nativeClient)
		results = append(results, result)
	}
	return results
}

// buildNativeClient wraps e.llmClient with a concurrency semaphore based on
// the first native runtime's Concurrency config. Returns nil if no LLM client
// is set.
func (e *Executor) buildNativeClient() llm.Client {
	if e.llmClient == nil {
		return nil
	}
	concurrency := 1
	for _, rtCfg := range e.config.Runtimes {
		if rtCfg.Native && rtCfg.Concurrency > 0 {
			concurrency = rtCfg.Concurrency
			break
		}
	}
	return llm.NewSemaphoreClient(e.llmClient, concurrency)
}

func (e *Executor) spawn(ctx context.Context, repoDir string, a Assignment, story PlannedStory, waveStories []WaveStoryInfo, nativeClient llm.Client) SpawnResult {
	result := SpawnResult{Assignment: a}

	// Determine worktree path
	worktreeBase := filepath.Join(execExpandHome(e.config.Workspace.StateDir), "worktrees")
	worktreePath := filepath.Join(worktreeBase, a.StoryID)
	result.WorktreePath = worktreePath

	// Create worktree with branch
	if err := nxdgit.CreateWorktree(repoDir, worktreePath, a.Branch); err != nil {
		result.Error = fmt.Errorf("create worktree for %s: %w", a.StoryID, err)
		return result
	}

	// Provision a per-story ephemeral DB if a Lifecycle is configured.
	if e.lifecycle != nil {
		project := filepath.Base(e.config.Workspace.StateDir)
		if project == "" || project == "." {
			project = "default"
		}
		db, err := e.lifecycle.Provision(ctx, a.StoryID, project, worktreePath)
		if err != nil {
			log.Printf("[executor] devdb provision failed for %s: %v", a.StoryID, err)
			_ = devdb.WriteFallbackNotice(worktreePath, err)
		} else {
			result.DB = db
		}
	}

	// CLAUDE.md is now written unconditionally in Spawn() on every launch,
	// so we don't duplicate it here.

	// Resolve runtime for this role
	rtName := e.runtimeForRole(a.Role)
	result.RuntimeName = rtName

	// Check if this is a native runtime (e.g., Gemma)
	if e.registry.IsNative(rtName) {
		return e.spawnNative(ctx, repoDir, a, story, waveStories, worktreePath, rtName, result, nativeClient)
	}

	rt, err := e.registry.Get(rtName)
	if err != nil {
		result.Error = fmt.Errorf("get runtime %s: %w", rtName, err)
		return result
	}

	// Build the agent prompt context
	feedback := e.latestReviewFeedback(a.StoryID)

	// Load RepoProfile if available to enrich prompts with pre-learned knowledge.
	var techStackStr, lintCmd, buildCmd, testCmd string
	if e.projectDir != "" {
		if profile, err := repolearn.LoadProfile(e.projectDir); err == nil && profile.TechStack.PrimaryLanguage != "" {
			techStackStr = profile.Summary()
			lintCmd = profile.Build.LintCommand
			buildCmd = profile.Build.BuildCommand
			testCmd = profile.Test.TestCommand
		}
	}
	if techStackStr == "" {
		stack := nxdgit.ScanRepo(worktreePath)
		techStackStr = fmt.Sprintf("%s (%s)", stack.Language, stack.BuildTool)
	}

	promptCtx := agent.PromptContext{
		StoryID:            a.StoryID,
		StoryTitle:         story.Title,
		StoryDescription:   story.Description,
		AcceptanceCriteria: string(story.AcceptanceCriteria),
		RepoPath:           worktreePath,
		Complexity:         story.Complexity,
		ReviewFeedback:     feedback,
		TechStack:          techStackStr,
		LintCommand:        lintCmd,
		BuildCommand:       buildCmd,
		TestCommand:        testCmd,
		IsFrontend:         detectFrontend(story.Title, story.Description, story.OwnedFiles),
	}

	// Query MemPalace for prior work context.
	if e.mempalace != nil && e.mempalace.IsAvailable() {
		repoName := filepath.Base(repoDir)
		query := story.Title + " " + story.Description
		results, _ := e.mempalace.Search(query, repoName, "", 5)
		if len(results) > 0 {
			var sb strings.Builder
			sb.WriteString("## Prior Work in This Requirement\n\n")
			sb.WriteString("The following has already been built. Build on this, do not recreate.\n\n")
			for _, r := range results {
				fmt.Fprintf(&sb, "- %s\n", r.Text)
			}
			promptCtx.PriorWorkContext = sb.String()
		}
	}

	// Inject wave brief for parallel story awareness.
	promptCtx.WaveBrief = BuildWaveBrief(a.StoryID, waveStories)

	// If this is a retry (feedback exists from a prior attempt), enhance
	// the goal prompt with attempt history so the agent learns from failures.
	var goalPrompt string
	if feedback != "" {
		tracker := NewAttemptTracker(e.eventStore)
		attempts, _ := tracker.ListAttempts(a.StoryID)

		priorAttempts := make([]agent.AttemptSummary, 0, len(attempts))
		for _, att := range attempts {
			priorAttempts = append(priorAttempts, agent.AttemptSummary{
				Number:  att.Number,
				Role:    att.Role,
				Outcome: att.Outcome,
				Error:   att.Error,
			})
		}

		tmplCtx := agent.TemplateContext{
			StoryID:            a.StoryID,
			StoryTitle:         story.Title,
			StoryDescription:   story.Description,
			AcceptanceCriteria: string(story.AcceptanceCriteria),
			Complexity:         story.Complexity,
			RepoPath:           worktreePath,
			TechStack:          promptCtx.TechStack,
			LintCommand:        promptCtx.LintCommand,
			BuildCommand:       promptCtx.BuildCommand,
			TestCommand:        promptCtx.TestCommand,
			ReviewFeedback:     feedback,
			IsExistingCodebase: promptCtx.IsExistingCodebase,
			IsBugFix:           promptCtx.IsBugFix,
			IsInfrastructure:   promptCtx.IsInfrastructure,
			IsFrontend:         promptCtx.IsFrontend,
			IsRetry:            true,
			RetryNumber:        len(attempts) + 1,
			PriorAttempts:      priorAttempts,
		}
		goalPrompt = agent.RenderGoalWithAttempts(tmplCtx)
	} else {
		goalPrompt = agent.GoalPrompt(a.Role, promptCtx)
	}

	// Resolve model for this role
	modelCfg := a.Role.ModelConfig(e.config.Models)

	// Build log path for post-mortem diagnosis
	logDir := filepath.Join(execExpandHome(e.config.Workspace.StateDir), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile := filepath.Join(logDir, a.StoryID+".log")

	// Spawn the runtime session
	if err := rt.Spawn(runtime.SessionConfig{
		SessionName:  a.SessionName,
		WorkDir:      worktreePath,
		Model:        modelCfg.Model,
		Goal:         goalPrompt,
		SystemPrompt: agent.SystemPrompt(a.Role, promptCtx),
		LogFile:      logFile,
	}); err != nil {
		result.Error = fmt.Errorf("spawn runtime for %s: %w", a.StoryID, err)
		return result
	}

	// Emit STORY_STARTED event with tier and role so AttemptTracker can
	// reconstruct attempt history without reverse-engineering roles.
	startEvt := state.NewEvent(state.EventStoryStarted, a.AgentID, a.StoryID, map[string]any{
		"worktree_path": worktreePath,
		"runtime":       rtName,
		"session_name":  a.SessionName,
		"branch":        a.Branch,
		"tier":          tierForRole(a.Role),
		"role":          string(a.Role),
	})
	if err := e.eventStore.Append(startEvt); err != nil {
		result.Error = fmt.Errorf("emit story started: %w", err)
		return result
	}
	if err := e.projStore.Project(startEvt); err != nil {
		result.Error = fmt.Errorf("project story started: %w", err)
		return result
	}

	return result
}

// runtimeForRole selects the configured runtime whose CLI can serve the
// model provider assigned to the given role. For offline setups the default
// runtime is typically "aider" backed by Ollama.
func (e *Executor) runtimeForRole(role agent.Role) string {
	modelCfg := role.ModelConfig(e.config.Models)
	modelName := strings.ToLower(modelCfg.Model)
	provider := strings.ToLower(modelCfg.Provider)

	// Check native runtimes first — if the model matches a native runtime's
	// model list, prefer it (no external CLI dependency needed).
	for name, rtCfg := range e.config.Runtimes {
		if !rtCfg.Native {
			continue
		}
		for _, m := range rtCfg.Models {
			if strings.HasPrefix(modelName, strings.ToLower(m)) {
				return name
			}
		}
	}

	// Well-known provider → runtime mappings
	providerRuntimes := map[string][]string{
		"ollama":    {"aider", "ollama"},
		"anthropic": {"claude-code", "claude"},
		"openai":    {"codex", "openai"},
		"google":    {"gemini"},
		"gemini":    {"gemini"},
	}

	if candidates, ok := providerRuntimes[provider]; ok {
		for _, name := range candidates {
			if _, exists := e.config.Runtimes[name]; exists {
				return name
			}
		}
	}

	// Fallback: first available runtime
	for name := range e.config.Runtimes {
		return name
	}
	return "aider"
}

// latestReviewFeedback returns the most recent failure feedback for the given
// story so a re-spawned agent can see why its prior attempt was rejected.
//
// Failures arrive as two event types: STORY_REVIEW_FAILED (review rejections
// and escalations, carrying a "reason") and STORY_QA_FAILED (QA failures,
// carrying a richer "feedback"). They are emitted by several agents
// ("reviewer", "qa", "merger", "manager", "dashboard"), so this must NOT
// filter by AgentID — an earlier version filtered AgentID:"monitor" on
// REVIEW_FAILED only, which matched nothing and silently disabled the entire
// retry-with-feedback loop. We scan both types, take the newest by timestamp,
// and prefer "feedback" over "reason".
func (e *Executor) latestReviewFeedback(storyID string) string {
	var (
		newest state.Event
		found  bool
	)
	for _, t := range []state.EventType{state.EventStoryReviewFailed, state.EventStoryQAFailed} {
		events, err := e.eventStore.List(state.EventFilter{
			Type:    t,
			StoryID: storyID,
		})
		if err != nil {
			continue
		}
		for _, ev := range events {
			if !found || ev.Timestamp.After(newest.Timestamp) {
				newest = ev
				found = true
			}
		}
	}
	if !found || newest.Payload == nil {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(newest.Payload, &payload); err != nil {
		return ""
	}

	if feedback, _ := payload["feedback"].(string); feedback != "" {
		return feedback
	}
	reason, _ := payload["reason"].(string)
	return reason
}

// execExpandHome replaces a leading ~ with the user's home directory.
func execExpandHome(path string) string {
	if len(path) == 0 || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// tierForRole maps agent roles to escalation tier numbers. These values
// align with the 5-tier escalation chain: 0 = same-role retry (junior/
// intermediate), 1 = senior, 2 = manager diagnosis, 3 = tech_lead re-plan,
// 4 = pause. Roles that aren't part of the execution chain (qa, supervisor)
// default to tier 0.
func tierForRole(role agent.Role) int {
	switch role {
	case agent.RoleJunior, agent.RoleIntermediate:
		return 0
	case agent.RoleSenior:
		return 1
	case agent.RoleManager:
		return 2
	case agent.RoleTechLead:
		return 3
	default:
		return 0
	}
}

// spawnNative runs a story using the native Gemma runtime (no tmux, no external CLI).
// It calls the LLM directly via function calling tools.
func (e *Executor) spawnNative(ctx context.Context, repoDir string, a Assignment, story PlannedStory, waveStories []WaveStoryInfo, worktreePath, rtName string, result SpawnResult, nativeClient llm.Client) SpawnResult {
	if nativeClient == nil {
		result.Error = fmt.Errorf("native runtime %s requires an LLM client (call SetLLMClient first)", rtName)
		return result
	}

	nativeCfg, ok := e.registry.NativeConfig(rtName)
	if !ok {
		result.Error = fmt.Errorf("native runtime config not found: %s", rtName)
		return result
	}

	// Build prompt context
	promptCtx := agent.PromptContext{
		StoryID:            a.StoryID,
		StoryTitle:         story.Title,
		StoryDescription:   story.Description,
		AcceptanceCriteria: string(story.AcceptanceCriteria),
		RepoPath:           worktreePath,
		Complexity:         story.Complexity,
		ReviewFeedback:     e.latestReviewFeedback(a.StoryID),
		IsFrontend:         detectFrontend(story.Title, story.Description, story.OwnedFiles),
	}

	if e.mempalace != nil && e.mempalace.IsAvailable() {
		repoName := filepath.Base(repoDir)
		query := story.Title + " " + story.Description
		searchResults, _ := e.mempalace.Search(query, repoName, "", 5)
		if len(searchResults) > 0 {
			var sb strings.Builder
			sb.WriteString("## Prior Work in This Requirement\n\n")
			for _, r := range searchResults {
				fmt.Fprintf(&sb, "- %s\n", r.Text)
			}
			promptCtx.PriorWorkContext = sb.String()
		}
	}
	promptCtx.WaveBrief = BuildWaveBrief(a.StoryID, waveStories)

	modelCfg := a.Role.ModelConfig(e.config.Models)
	systemPrompt := agent.SystemPrompt(a.Role, promptCtx)
	goal := agent.GoalPrompt(a.Role, promptCtx)

	// Write launch config artifact for reproducibility.
	if e.artifactStore != nil {
		_ = e.artifactStore.Write(a.StoryID, artifact.TypeLaunchConfig, artifact.LaunchConfig{
			StoryID:   a.StoryID,
			Runtime:   rtName,
			Model:     modelCfg.Model,
			Prompt:    goal,
			WaveBrief: promptCtx.WaveBrief,
		})
	}

	// Emit STORY_STARTED with tier and role for attempt tracking.
	startEvt := state.NewEvent(state.EventStoryStarted, a.AgentID, a.StoryID, map[string]any{
		"worktree_path": worktreePath, "runtime": rtName, "branch": a.Branch,
		"tier": tierForRole(a.Role), "role": string(a.Role),
	})
	if err := e.eventStore.Append(startEvt); err != nil {
		log.Printf("[native-runtime] append STORY_STARTED for %s: %v", a.StoryID, err)
	}
	if err := e.projStore.Project(startEvt); err != nil {
		log.Printf("[native-runtime] project STORY_STARTED for %s: %v", a.StoryID, err)
	}

	// Per-story metrics labelling: stamp story_id / tier / role onto the
	// shared metrics client for this goroutine's LLM calls. If nativeClient
	// is not a *metrics.MetricsClient (dry-run, tests) the helpers no-op.
	storyClient := metrics.LabelRole(
		metrics.LabelTier(
			metrics.LabelStory(nativeClient, a.StoryID),
			tierForRole(a.Role),
		),
		string(a.Role),
	)

	// Run native Gemma runtime in a goroutine (non-blocking, like tmux).
	// On completion (success or failure) the goroutine emits STORY_COMPLETED
	// so the monitor's pollOnce can trigger the post-execution pipeline.
	// nativeWG tracks the goroutine so shutdown can drain in-flight runs
	// before the process exits (prevents lost STORY_COMPLETED writes →
	// double-dispatch on next resume).
	e.nativeWG.Add(1)
	go func() {
		defer e.nativeWG.Done()
		gemmaRT := runtime.NewGemmaRuntime(storyClient, runtime.GemmaRuntimeConfig{
			MaxIterations:    nativeCfg.MaxIterations,
			CommandAllowlist: nativeCfg.CommandAllowlist,
		})
		gemmaRT.AgentID = a.AgentID
		gemmaRT.StoryID = a.StoryID
		gemmaRT.Scratchboard = e.scratchboard
		gemmaRT.Criteria = ConfigCriteriaToRuntime(e.config.QA.SuccessCriteria)
		// Operator-directive injection: pull pending instructions for this
		// req/story at the top of each iteration. Nil-safe.
		gemmaRT.ReqID = a.ReqID
		gemmaRT.Directives = e.directives.AsRuntimeProvider()

		// Wire progress callback to emit fine-grained STORY_PROGRESS events.
		gemmaRT.OnProgress = func(prog runtime.ProgressEvent) {
			payload := map[string]any{
				"iteration": prog.Iteration,
				"max_iter":  prog.MaxIter,
				"phase":     string(prog.Phase),
				"detail":    prog.Detail,
			}
			if prog.Tool != "" {
				payload["tool"] = prog.Tool
			}
			if prog.File != "" {
				payload["file"] = prog.File
			}
			if prog.Command != "" {
				payload["command"] = prog.Command
			}
			if prog.IsError {
				payload["is_error"] = true
			}
			evt := state.NewEvent(state.EventStoryProgress, a.AgentID, a.StoryID, payload)
			if err := e.eventStore.Append(evt); err != nil {
				log.Printf("[native-runtime] append STORY_PROGRESS for %s: %v", a.StoryID, err)
			}
			if err := e.projStore.Project(evt); err != nil {
				log.Printf("[native-runtime] project STORY_PROGRESS for %s: %v", a.StoryID, err)
			}

			// Append to per-story trace artifact for post-mortem replay.
			if e.artifactStore != nil {
				if err := e.artifactStore.Append(a.StoryID, artifact.TypeTraceEvents, payload); err != nil {
					log.Printf("[native-runtime] artifact append for %s: %v", a.StoryID, err)
				}
			}
		}

		// Create a cancellable context parented to the executor's context so
		// monitor cancellation (Ctrl-C, graceful shutdown) propagates to the
		// native goroutine. The controller can also stop stuck agents.
		execCtx, execCancel := context.WithCancel(ctx)
		defer execCancel()
		if e.controller != nil {
			e.controller.RegisterCancel(a.StoryID, execCancel)
		}
		defer func() {
			if e.controller != nil {
				e.controller.DeregisterCancel(a.StoryID)
			}
		}()

		log.Printf("[native-runtime] executing %s in %s", a.StoryID, worktreePath)
		execStart := time.Now()
		execResult := gemmaRT.Execute(execCtx, worktreePath, modelCfg.Model, systemPrompt, goal)

		if execResult.Error != nil {
			log.Printf("[native-runtime] %s failed after %d iterations: %v",
				a.StoryID, execResult.Iterations, execResult.Error)
		} else {
			log.Printf("[native-runtime] %s completed in %d iterations: %s",
				a.StoryID, execResult.Iterations, execResult.Summary)
		}

		// Emit STORY_COMPLETED regardless of success/failure — the monitor's
		// post-execution pipeline handles empty diffs and retries.
		payload := map[string]any{
			"iterations": execResult.Iterations,
			"native":     true,
		}
		if execResult.Summary != "" {
			payload["summary"] = execResult.Summary
		}
		if execResult.Error != nil {
			payload["error"] = execResult.Error.Error()
		}
		if len(execResult.CriteriaResult) > 0 {
			passed := criteria.AllPassed(execResult.CriteriaResult)
			payload["criteria_passed"] = passed
			if !passed {
				payload["criteria_failures"] = criteria.FailureSummary(execResult.CriteriaResult)
			}
		}
		completeEvt := state.NewEvent(state.EventStoryCompleted, a.AgentID, a.StoryID, payload)
		if err := e.eventStore.Append(completeEvt); err != nil {
			log.Printf("[native-runtime] append STORY_COMPLETED for %s: %v", a.StoryID, err)
		}
		if err := e.projStore.Project(completeEvt); err != nil {
			log.Printf("[native-runtime] project STORY_COMPLETED for %s: %v", a.StoryID, err)
		}

		// Stage timing for the execute phase. Outcome is "success" only when
		// the runtime returned without error AND criteria (if any) passed.
		execOutcome := "success"
		if execResult.Error != nil {
			execOutcome = "failure"
		} else if len(execResult.CriteriaResult) > 0 && !criteria.AllPassed(execResult.CriteriaResult) {
			execOutcome = "failure"
		}
		EmitStageCompleted(e.eventStore, e.projStore, a.AgentID, a.StoryID, "execute", execOutcome, execStart)
	}()

	return result
}

// ConfigCriteriaToRuntime converts config.SuccessCriterion slice to
// criteria.Criterion slice for use by the executor, native runtime, and QA
// pipeline. Command criteria accept the historical `value:` form and the
// newer `path:` fallback. `test_passes` normalizes a full "go test ..." command
// to the package/flag args expected by criteria.TypeTestPasses.
//
// Live-test discovery (LB6): for `command_succeeds` and `test_passes` the
// Target IS the command (e.g. "go build ./..."). Config users typically put
// it in `value:`. Earlier code mapped only `path → Target`, which produced
// an empty Target for command-style criteria and made every story fail
// with "command rejected by allowlist:" before even running.
//
// Mapping rules:
//   - command_succeeds / test_passes: command lives in value, fall back to path
//   - file_exists / file_contains: path is the target
//   - coverage_above: path = package, value = threshold
func ConfigCriteriaToRuntime(cfgCriteria []config.SuccessCriterion) []criteria.Criterion {
	if len(cfgCriteria) == 0 {
		return nil
	}
	result := make([]criteria.Criterion, 0, len(cfgCriteria))
	for _, c := range cfgCriteria {
		var target, expected string
		switch c.Kind {
		case "command_succeeds":
			target = c.Value
			if target == "" {
				target = c.Path // backwards compat for misconfigured YAML
			}
		case "test_passes":
			target = testTargetFromConfig(c)
		case "file_contains":
			target = c.Path
			expected = c.Value
		case "coverage_above":
			target = c.Path
			expected = c.Value
		case "file_exists":
			target = c.Path
		default:
			target = c.Path
			expected = c.Value
		}
		result = append(result, criteria.Criterion{
			Type:           criteria.Type(c.Kind),
			Target:         target,
			Expected:       expected,
			Command:        c.Command,
			SQL:            c.SQL,
			ExpectedRows:   c.ExpectedRows,
			SchemaBaseline: c.SchemaBaseline,
		})
	}
	return result
}

func testTargetFromConfig(c config.SuccessCriterion) string {
	target := c.Value
	if target == "" {
		target = c.Path
	}
	return normalizeGoTestTarget(target)
}

func normalizeGoTestTarget(target string) string {
	fields := strings.Fields(target)
	if len(fields) >= 2 && fields[0] == "go" && fields[1] == "test" {
		fields = fields[2:]
		if len(fields) == 0 {
			return "./..."
		}
		return strings.Join(fields, " ")
	}
	return target
}
