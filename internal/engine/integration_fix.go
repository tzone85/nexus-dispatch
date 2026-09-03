package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TechLeadFixer dispatches a focused fix story when the post-merge integration
// build fails. It uses the Tech Lead LLM to produce a one-sentence description
// of what needs reconciling, then logs the suggestion for the operator.
//
// MVP approach: non-blocking background goroutine that logs the LLM-generated
// fix description. Full worktree dispatch is a Wave 2 enhancement.
type TechLeadFixer struct {
	llmClient  llm.Client
	model      string
	maxTokens  int
	eventStore state.EventStore
	projStore  state.ProjectionStore
}

// NewTechLeadFixer creates a TechLeadFixer.
func NewTechLeadFixer(
	client llm.Client,
	model string,
	maxTokens int,
	es state.EventStore,
	ps state.ProjectionStore,
) *TechLeadFixer {
	return &TechLeadFixer{
		llmClient:  client,
		model:      model,
		maxTokens:  maxTokens,
		eventStore: es,
		projStore:  ps,
	}
}

// buildPrompt constructs the prompt sent to the Tech Lead LLM to produce a
// focused fix-story description.
//
// Parameters:
//   - triggerStoryID: the story whose merge broke the build
//   - buildError: combined stderr+stdout from the failed build command
//   - recentStories: the last ≤5 merged stories for this requirement
func (f *TechLeadFixer) buildPrompt(triggerStoryID, buildError string, recentStories []state.Story) string {
	var sb strings.Builder
	sb.WriteString("The following stories were recently merged and the main branch no longer compiles:\n\n")

	sb.WriteString("Recently merged stories:\n")
	for _, s := range recentStories {
		fmt.Fprintf(&sb, "  - [%s] %s\n", s.ID, s.Title)
	}

	sb.WriteString("\nBuild error (combined stdout+stderr):\n```\n")
	sb.WriteString(buildError)
	sb.WriteString("\n```\n\n")

	sb.WriteString("Produce ONE focused story description (plain text, ≤3 sentences) that a senior developer should implement to fix the build. ")
	sb.WriteString("Identify the likely conflict between the stories above, name the exact files and symbols involved, and describe the reconciliation. ")
	sb.WriteString("Do NOT produce JSON. Do NOT ask clarifying questions. Respond with the story description only.")
	return sb.String()
}

// DispatchIntegrationFix is the entry point called by the monitor after a
// failed post-merge integration build. It runs runIntegrationFix in a
// detached goroutine so it never stalls the monitor's pipeline goroutine.
func (f *TechLeadFixer) DispatchIntegrationFix(ctx context.Context, triggerStoryID, repoDir, buildError string) {
	_ = repoDir // reserved: a Wave 2 enhancement dispatches a real fix story into a worktree
	// The caller (postExecutionPipeline) passes its pipeline context and then
	// returns immediately — firing its deferred pipelineCancel(). If we parented
	// fixCtx on that context, it would be cancelled microseconds after this
	// goroutine starts, aborting the LLM diagnosis every time. Detach the
	// cancellation (keeping any context values) so this deliberately
	// fire-and-forget goroutine gets its full 2-minute budget.
	baseCtx := context.WithoutCancel(ctx)
	go func() {
		fixCtx, cancel := context.WithTimeout(baseCtx, 2*time.Minute)
		defer cancel()
		_, _ = f.runIntegrationFix(fixCtx, triggerStoryID, buildError)
	}()
}

// runIntegrationFix produces and records the Tech Lead's fix suggestion:
//  1. Fetches the ≤5 most recently merged stories for the same requirement.
//  2. Asks the Tech Lead LLM to produce a fix-story description.
//  3. Persists the outcome as STORY_INTEGRATION_FAILED — with fix_hint on
//     success, or fix_error when the LLM call failed, so a broken mainline is
//     never silent in the event log.
//
// Returns the suggestion (trimmed) or the error that prevented one.
func (f *TechLeadFixer) runIntegrationFix(ctx context.Context, triggerStoryID, buildError string) (string, error) {
	story, err := f.projStore.GetStory(triggerStoryID)
	if err != nil {
		log.Printf("[integration-fixer] cannot look up trigger story %s: %v", triggerStoryID, err)
		return "", fmt.Errorf("look up trigger story %s: %w", triggerStoryID, err)
	}

	allStories, err := f.projStore.ListStories(state.StoryFilter{ReqID: story.ReqID})
	if err != nil {
		log.Printf("[integration-fixer] cannot list stories for req %s: %v", story.ReqID, err)
		return "", fmt.Errorf("list stories for %s: %w", story.ReqID, err)
	}

	// Collect up to 5 recently merged stories (latest first).
	var merged []state.Story
	for i := len(allStories) - 1; i >= 0 && len(merged) < 5; i-- {
		if allStories[i].Status == "merged" {
			merged = append(merged, allStories[i])
		}
	}

	prompt := f.buildPrompt(triggerStoryID, buildError, merged)

	payload := map[string]any{
		"build_error":   buildError,
		"trigger_story": triggerStoryID,
	}
	resp, err := f.llmClient.Complete(ctx, llm.CompletionRequest{
		Model:     f.model,
		MaxTokens: f.maxTokens,
		System:    "You are a Tech Lead diagnosing a broken main branch after a multi-story merge. Be concise and precise.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		log.Printf("[integration-fixer] LLM call failed for %s: %v", triggerStoryID, err)
		log.Printf("[integration-fixer] MANUAL FIX NEEDED for %s — build error:\n%s", triggerStoryID, buildError)
		payload["fix_error"] = err.Error()
		f.record(triggerStoryID, payload)
		return "", fmt.Errorf("tech lead fix diagnosis: %w", err)
	}

	fixDescription := strings.TrimSpace(resp.Content)
	log.Printf("[integration-fixer] suggested fix for %s:\n%s", triggerStoryID, fixDescription)
	log.Printf("[integration-fixer] to dispatch: nxd req %q", fixDescription)
	payload["fix_hint"] = fixDescription
	f.record(triggerStoryID, payload)
	return fixDescription, nil
}

// record persists the fixer's outcome so the suggestion (or its absence) is
// visible in the event log and dashboard.
func (f *TechLeadFixer) record(triggerStoryID string, payload map[string]any) {
	evt := state.NewEvent(state.EventStoryIntegrationFailed, "integration-fixer", triggerStoryID, payload)
	if err := f.eventStore.Append(evt); err != nil {
		log.Printf("[integration-fixer] failed to append fix event: %v", err)
	}
}
