package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ManagerAction is the structured response from the Manager's LLM diagnosis.
type ManagerAction struct {
	Diagnosis     string         `json:"diagnosis"`
	Category      string         `json:"category"`
	Action        string         `json:"action"`
	RetryConfig   *RetryConfig   `json:"retry_config,omitempty"`
	RewriteConfig *RewriteConfig `json:"rewrite_config,omitempty"`
	SplitConfig   *SplitConfig   `json:"split_config,omitempty"`
}

// RetryConfig describes how to retry the story after an environment fix.
type RetryConfig struct {
	TargetRole    string   `json:"target_role"`
	ResetTier     int      `json:"reset_tier"`
	WorktreeReset bool     `json:"worktree_reset"`
	EnvFixes      []string `json:"env_fixes"`
}

// RewriteConfig describes a rewritten story definition.
type RewriteConfig struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Complexity         int      `json:"complexity"`
	OwnedFiles         []string `json:"owned_files"`
}

// SplitConfig describes how to split a story into smaller children.
type SplitConfig struct {
	Children        []SplitChildConfig `json:"children"`
	DependencyEdges [][]string         `json:"dependency_edges"`
}

// SplitChildConfig holds the definition for one child of a split story.
type SplitChildConfig struct {
	Suffix             string   `json:"suffix"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Complexity         int      `json:"complexity"`
	OwnedFiles         []string `json:"owned_files"`
}

// validManagerActions enumerates the actions the Manager can return.
var validManagerActions = map[string]bool{
	"retry":                true,
	"rewrite":              true,
	"split":                true,
	"escalate_to_techlead": true,
}

// parseManagerAction unmarshals a JSON byte slice into a ManagerAction,
// validating that the action field is one of the allowed values.
func parseManagerAction(data []byte) (ManagerAction, error) {
	var action ManagerAction
	if err := json.Unmarshal(data, &action); err != nil {
		return ManagerAction{}, fmt.Errorf("parse manager action: %w", err)
	}
	if !validManagerActions[action.Action] {
		return ManagerAction{}, fmt.Errorf("invalid manager action: %q", action.Action)
	}
	return action, nil
}

// eventSummary is a lightweight representation of a domain event used for
// building the diagnostic prompt without coupling to the full state.Event.
type eventSummary struct {
	Type    string
	AgentID string
}

// DiagnosticContext packages all the information the Manager LLM needs to
// diagnose why a story failed and choose a corrective action.
type DiagnosticContext struct {
	StoryID            string
	StoryTitle         string
	StoryDescription   string
	AcceptanceCriteria string
	Complexity         int
	SplitDepth         int
	OwnedFiles         []string
	RequirementText    string
	SiblingStories     []state.Story
	AgentLog           string
	EventHistory       []eventSummary
	WorktreeStatus     string
	WorktreeLog        string
	WorktreeFiles      string
	DependencyDiffs    string
}

// Manager diagnoses why a story failed by calling an LLM with diagnostic
// context and returning a structured corrective action.
type Manager struct {
	llmClient  llm.Client
	eventStore state.EventStore
	projStore  state.ProjectionStore
	model      string
	maxTokens  int
}

// NewManager creates a Manager wired to the given LLM client, model
// configuration, event store, and projection store.
func NewManager(client llm.Client, model string, maxTokens int, es state.EventStore, ps state.ProjectionStore) *Manager {
	return &Manager{
		llmClient:  client,
		eventStore: es,
		projStore:  ps,
		model:      model,
		maxTokens:  maxTokens,
	}
}

// managerSystemPrompt instructs the LLM on its role and expected response
// format for failure diagnosis.
const managerSystemPrompt = `You are a failure diagnosis manager for an AI agent orchestration system.
You receive diagnostic context about a story (task) that has failed multiple times.
Diagnose why it failed and choose the best corrective action.

Respond with a single JSON object (no markdown fences):
{
  "diagnosis": "human-readable explanation",
  "category": "environment | structural | complexity | transient | unknown",
  "action": "retry | rewrite | split | escalate_to_techlead",
  ... action-specific config ...
}

Actions:
- retry: Environmental fix. Include retry_config: {target_role, reset_tier (0-1), worktree_reset (bool), env_fixes (string[])}
- rewrite: Story needs different description. Include rewrite_config: {title, description, acceptance_criteria, complexity, owned_files}
- split: Too complex. Include split_config: {children [{suffix, title, description, acceptance_criteria, complexity, owned_files}], dependency_edges}
- escalate_to_techlead: Structural problem beyond your ability. No extra config.

Constraints:
- If split_depth >= 2, do NOT split (use rewrite or escalate_to_techlead)
- Children owned_files must not overlap
- Prefer the simplest action that fixes the problem`

// Diagnose calls the Manager LLM to analyse the diagnostic context and
// return a structured corrective action for the failed story.
func (m *Manager) Diagnose(ctx context.Context, dc DiagnosticContext) (ManagerAction, error) {
	prompt := m.buildPrompt(dc)

	resp, err := m.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     m.model,
		MaxTokens: m.maxTokens,
		System:    managerSystemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return ManagerAction{}, fmt.Errorf("manager LLM call: %w", err)
	}

	cleaned := extractJSON(resp.Content)
	return parseManagerAction([]byte(cleaned))
}

// BuildDiagnosticContext collects all available diagnostic information for
// the given story from the projection store, event store, agent log, and
// worktree state.
func (m *Manager) BuildDiagnosticContext(storyID, worktreePath, logDir string) (DiagnosticContext, error) {
	dc := DiagnosticContext{StoryID: storyID}

	// Read story from projection store.
	if story, err := m.projStore.GetStory(storyID); err == nil {
		dc.StoryTitle = story.Title
		dc.StoryDescription = story.Description
		dc.AcceptanceCriteria = story.AcceptanceCriteria
		dc.Complexity = story.Complexity
		dc.SplitDepth = story.SplitDepth
		dc.OwnedFiles = story.OwnedFiles

		// Fetch sibling stories for context.
		if story.ReqID != "" {
			siblings, err := m.projStore.ListStories(state.StoryFilter{ReqID: story.ReqID})
			if err == nil {
				dc.SiblingStories = siblings
			}
		}
	}

	// Read agent log (keep tail for most recent output).
	logPath := filepath.Join(logDir, storyID+".log")
	if data, err := os.ReadFile(logPath); err == nil {
		dc.AgentLog = managerTruncate(string(data), 4000)
	}

	// Read event history for this story.
	events, _ := m.eventStore.List(state.EventFilter{StoryID: storyID})
	for _, evt := range events {
		dc.EventHistory = append(dc.EventHistory, eventSummary{
			Type:    string(evt.Type),
			AgentID: evt.AgentID,
		})
	}

	// Read worktree state.
	if worktreePath != "" {
		dc.WorktreeStatus = runGit(worktreePath, "status", "--short")
		dc.WorktreeLog = runGit(worktreePath, "log", "--oneline", "-5")
		dc.WorktreeFiles = runGit(worktreePath, "ls-files")
	}

	return dc, nil
}

// buildPrompt formats the diagnostic context into a structured prompt for
// the Manager LLM.
func (m *Manager) buildPrompt(dc DiagnosticContext) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Story: %s\n", dc.StoryID)
	fmt.Fprintf(&b, "**Title:** %s\n", dc.StoryTitle)
	fmt.Fprintf(&b, "**Description:** %s\n", dc.StoryDescription)
	fmt.Fprintf(&b, "**Acceptance Criteria:** %s\n", dc.AcceptanceCriteria)
	fmt.Fprintf(&b, "**Complexity:** %d | **Split Depth:** %d (max: 2)\n\n", dc.Complexity, dc.SplitDepth)

	if len(dc.OwnedFiles) > 0 {
		fmt.Fprintf(&b, "**Owned Files:** %s\n\n", strings.Join(dc.OwnedFiles, ", "))
	}

	if dc.AgentLog != "" {
		fmt.Fprintf(&b, "## Agent Log\n```\n%s\n```\n\n", dc.AgentLog)
	}

	if dc.WorktreeStatus != "" {
		fmt.Fprintf(&b, "## Worktree\n")
		fmt.Fprintf(&b, "**status:**\n```\n%s\n```\n", dc.WorktreeStatus)
		fmt.Fprintf(&b, "**log:**\n```\n%s\n```\n", dc.WorktreeLog)
		fmt.Fprintf(&b, "**files:**\n```\n%s\n```\n\n", dc.WorktreeFiles)
	}

	if len(dc.EventHistory) > 0 {
		fmt.Fprintf(&b, "## Events (%d)\n", len(dc.EventHistory))
		for _, evt := range dc.EventHistory {
			fmt.Fprintf(&b, "- %s (agent: %s)\n", evt.Type, evt.AgentID)
		}
		b.WriteString("\n")
	}

	if dc.DependencyDiffs != "" {
		fmt.Fprintf(&b, "## Dependency Diffs\n```\n%s\n```\n\n", dc.DependencyDiffs)
	}

	return b.String()
}

// runGit executes a git command in the given directory and returns trimmed
// stdout. Errors are silently ignored -- callers use the empty string as a
// missing-data sentinel.
func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// managerTruncate returns the last maxLen bytes of s. If s is shorter than
// maxLen it is returned unchanged. Keeping the tail preserves the most
// recent (and usually most diagnostic) output.
func managerTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[len(s)-maxLen:]
}
