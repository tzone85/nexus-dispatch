package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
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
}

// NewExecutor creates an Executor wired to the runtime registry, configuration,
// event store, and projection store.
func NewExecutor(reg *runtime.Registry, cfg config.Config, es state.EventStore, ps state.ProjectionStore) *Executor {
	return &Executor{
		registry:   reg,
		config:     cfg,
		eventStore: es,
		projStore:  ps,
	}
}

// SpawnResult holds the outcome of spawning an agent for one assignment.
type SpawnResult struct {
	Assignment   Assignment
	WorktreePath string
	RuntimeName  string
	Error        error
}

// SpawnAll creates worktrees and launches tmux sessions for each assignment.
func (e *Executor) SpawnAll(repoDir string, assignments []Assignment, stories map[string]PlannedStory) []SpawnResult {
	results := make([]SpawnResult, 0, len(assignments))
	for _, a := range assignments {
		result := e.spawn(repoDir, a, stories[a.StoryID])
		results = append(results, result)
	}
	return results
}

func (e *Executor) spawn(repoDir string, a Assignment, story PlannedStory) SpawnResult {
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
	provider := strings.ToLower(modelCfg.Provider)

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
