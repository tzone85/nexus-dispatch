package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ActiveAgent tracks a running agent session for the monitor.
type ActiveAgent struct {
	Assignment   Assignment
	WorktreePath string
	RuntimeName  string
}

// Executor spawns agents for dispatched assignments by creating git worktrees,
// launching tmux sessions with configured runtimes, and emitting lifecycle events.
type Executor struct {
	registry   *runtime.Registry
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
	mempalace  *memory.MemPalace
	llmClient  llm.Client // for native runtimes (Gemma)
}

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

// SpawnResult holds the outcome of spawning an agent for one assignment.
type SpawnResult struct {
	Assignment   Assignment
	WorktreePath string
	RuntimeName  string
	Error        error
}

// SpawnAll creates worktrees and launches tmux sessions for each assignment.
// It builds a wave brief from all assignments so each agent knows which
// parallel stories are running and which files to avoid.
func (e *Executor) SpawnAll(repoDir string, assignments []Assignment, stories map[string]PlannedStory) []SpawnResult {
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

	results := make([]SpawnResult, 0, len(assignments))
	for _, a := range assignments {
		result := e.spawn(repoDir, a, stories[a.StoryID], waveStories)
		results = append(results, result)
	}
	return results
}

func (e *Executor) spawn(repoDir string, a Assignment, story PlannedStory, waveStories []WaveStoryInfo) SpawnResult {
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

	// CLAUDE.md is now written unconditionally in Spawn() on every launch,
	// so we don't duplicate it here.

	// Resolve runtime for this role
	rtName := e.runtimeForRole(a.Role)
	result.RuntimeName = rtName

	// Check if this is a native runtime (e.g., Gemma)
	if e.registry.IsNative(rtName) {
		return e.spawnNative(repoDir, a, story, waveStories, worktreePath, rtName, result)
	}

	rt, err := e.registry.Get(rtName)
	if err != nil {
		result.Error = fmt.Errorf("get runtime %s: %w", rtName, err)
		return result
	}

	// Build the agent prompt context
	promptCtx := agent.PromptContext{
		StoryID:            a.StoryID,
		StoryTitle:         story.Title,
		StoryDescription:   story.Description,
		AcceptanceCriteria: string(story.AcceptanceCriteria),
		RepoPath:           worktreePath,
		Complexity:         story.Complexity,
		ReviewFeedback:     e.latestReviewFeedback(a.StoryID),
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
				sb.WriteString(fmt.Sprintf("- %s\n", r.Text))
			}
			promptCtx.PriorWorkContext = sb.String()
		}
	}

	// Inject wave brief for parallel story awareness.
	promptCtx.WaveBrief = BuildWaveBrief(a.StoryID, waveStories)

	// Resolve model for this role
	modelCfg := a.Role.ModelConfig(e.config.Models)

	// Build log path for post-mortem diagnosis
	logDir := filepath.Join(execExpandHome(e.config.Workspace.StateDir), "logs")
	os.MkdirAll(logDir, 0o755)
	logFile := filepath.Join(logDir, a.StoryID+".log")

	// Spawn the runtime session
	if err := rt.Spawn(runtime.SessionConfig{
		SessionName:  a.SessionName,
		WorkDir:      worktreePath,
		Model:        modelCfg.Model,
		Goal:         agent.GoalPrompt(a.Role, promptCtx),
		SystemPrompt: agent.SystemPrompt(a.Role, promptCtx),
		LogFile:      logFile,
	}); err != nil {
		result.Error = fmt.Errorf("spawn runtime for %s: %w", a.StoryID, err)
		return result
	}

	// Emit STORY_STARTED event
	startEvt := state.NewEvent(state.EventStoryStarted, a.AgentID, a.StoryID, map[string]any{
		"worktree_path": worktreePath,
		"runtime":       rtName,
		"session_name":  a.SessionName,
		"branch":        a.Branch,
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

// latestReviewFeedback queries the event store for the most recent
// STORY_REVIEW_FAILED event (emitted by "monitor") for the given story
// and extracts the "feedback" field from its payload. Returns an empty
// string if no feedback is found.
func (e *Executor) latestReviewFeedback(storyID string) string {
	events, err := e.eventStore.List(state.EventFilter{
		Type:    state.EventStoryReviewFailed,
		AgentID: "monitor",
		StoryID: storyID,
	})
	if err != nil || len(events) == 0 {
		return ""
	}

	// Take the most recent event (last in the list).
	latest := events[len(events)-1]
	if latest.Payload == nil {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(latest.Payload, &payload); err != nil {
		return ""
	}

	feedback, _ := payload["feedback"].(string)
	return feedback
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

// spawnNative runs a story using the native Gemma runtime (no tmux, no external CLI).
// It calls the LLM directly via function calling tools.
func (e *Executor) spawnNative(repoDir string, a Assignment, story PlannedStory, waveStories []WaveStoryInfo, worktreePath, rtName string, result SpawnResult) SpawnResult {
	if e.llmClient == nil {
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
	}

	if e.mempalace != nil && e.mempalace.IsAvailable() {
		repoName := filepath.Base(repoDir)
		query := story.Title + " " + story.Description
		searchResults, _ := e.mempalace.Search(query, repoName, "", 5)
		if len(searchResults) > 0 {
			var sb strings.Builder
			sb.WriteString("## Prior Work in This Requirement\n\n")
			for _, r := range searchResults {
				sb.WriteString(fmt.Sprintf("- %s\n", r.Text))
			}
			promptCtx.PriorWorkContext = sb.String()
		}
	}
	promptCtx.WaveBrief = BuildWaveBrief(a.StoryID, waveStories)

	modelCfg := a.Role.ModelConfig(e.config.Models)
	systemPrompt := agent.SystemPrompt(a.Role, promptCtx)
	goal := agent.GoalPrompt(a.Role, promptCtx)

	// Emit STORY_STARTED
	startEvt := state.NewEvent(state.EventStoryStarted, a.AgentID, a.StoryID, map[string]any{
		"worktree_path": worktreePath, "runtime": rtName, "branch": a.Branch,
	})
	e.eventStore.Append(startEvt)
	e.projStore.Project(startEvt)

	// Run native Gemma runtime in a goroutine (non-blocking, like tmux).
	// On completion (success or failure) the goroutine emits STORY_COMPLETED
	// so the monitor's pollOnce can trigger the post-execution pipeline.
	go func() {
		gemmaRT := runtime.NewGemmaRuntime(e.llmClient, runtime.GemmaRuntimeConfig{
			MaxIterations:    nativeCfg.MaxIterations,
			CommandAllowlist: nativeCfg.CommandAllowlist,
		})

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
			e.eventStore.Append(evt)
			e.projStore.Project(evt)
		}

		log.Printf("[native-runtime] executing %s in %s", a.StoryID, worktreePath)
		execResult := gemmaRT.Execute(context.Background(), worktreePath, modelCfg.Model, systemPrompt, goal)

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
		completeEvt := state.NewEvent(state.EventStoryCompleted, a.AgentID, a.StoryID, payload)
		e.eventStore.Append(completeEvt)
		e.projStore.Project(completeEvt)
	}()

	return result
}
