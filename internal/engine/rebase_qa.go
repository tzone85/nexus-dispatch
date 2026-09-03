package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// QA after conflict resolution.
//
// QA runs on the story's worktree BEFORE the rebase onto the base branch.
// When the rebase is clean that tree is byte-for-byte what gets merged, so
// nothing new needs checking. When the ConflictResolver rewrote files, the
// tree QA approved no longer exists: the LLM's resolution is unverified code
// about to land on the mainline. postRebaseGate re-runs QA on the rebased
// tree in exactly that case and blocks the merge if it is red.
//
// The resolver signals "I touched files" by emitting STORY_PROGRESS with
// action=conflicts_resolved (ConflictResolver.emitResolutionEvent); the
// monitor compares the count before and after the rebase.

// errPostRebaseQA is returned by rebaseAndMerge when QA on the rebased tree
// failed. The story has already been reset with feedback; the caller only
// needs to stop.
var errPostRebaseQA = errors.New("QA failed on the rebased tree after conflict resolution")

// conflictsResolvedCount counts the resolver's conflicts_resolved progress
// events for storyID.
func conflictsResolvedCount(es state.EventStore, storyID string) int {
	events, err := es.List(state.EventFilter{Type: state.EventStoryProgress, StoryID: storyID})
	if err != nil {
		return 0
	}
	n := 0
	for _, evt := range events {
		if state.DecodePayload(evt.Payload)["action"] == "conflicts_resolved" {
			n++
		}
	}
	return n
}

// qaFailureFeedback formats the failed checks of a QA result into the
// feedback the re-spawned agent receives.
func qaFailureFeedback(result QAResult, context string) string {
	var out strings.Builder
	for _, check := range result.Checks {
		if !check.Passed {
			fmt.Fprintf(&out, "[%s] %s\n", check.Name, check.Output)
		}
	}
	qaOutput := out.String()
	return fmt.Sprintf(
		"QA FAILURE (%s) — fix this error:\n\n%s\nHint: %s\n\nMake the minimal change to fix this. Do not rewrite files.",
		context, qaOutput, AnalyzeFailure(qaOutput, ""),
	)
}

// postRebaseGate re-runs QA on the rebased worktree when the conflict
// resolver changed files during the rebase (resolvedBefore is the resolver
// event count taken before the rebase started). A clean rebase, or no QA
// configured, passes straight through. On a red tree the story is reset
// with feedback and errPostRebaseQA is returned.
func (m *Monitor) postRebaseGate(ctx context.Context, storyID, attemptID, worktreePath string, resolvedBefore int) error {
	if m.qa == nil || conflictsResolvedCount(m.eventStore, storyID) <= resolvedBefore {
		return nil
	}

	log.Printf("[pipeline] conflicts were resolved during rebase for %s — re-running QA on the rebased tree", storyID)
	qaStart := time.Now()
	result, err := m.qa.ForAttempt(attemptID).Run(ctx, storyID, worktreePath)
	if err != nil {
		EmitStageCompleted(m.eventStore, m.projStore, "monitor", storyID, "qa_post_rebase", "failure", qaStart)
		m.resetStoryToDraftFor(storyID, attemptID, "qa", fmt.Sprintf("QA error after conflict resolution: %v", err))
		return fmt.Errorf("%w: %v", errPostRebaseQA, err)
	}
	if !result.Passed {
		EmitStageCompleted(m.eventStore, m.projStore, "monitor", storyID, "qa_post_rebase", "failure", qaStart)
		feedback := qaFailureFeedback(result, "after rebase with conflict resolution")
		emitEventOrLog(m.eventStore, m.projStore,
			state.NewEventForAttempt(state.EventStoryQAFailed, "monitor", storyID, attemptID, map[string]any{
				"feedback": feedback,
				"source":   "post_rebase_qa",
			}))
		m.resetStoryToDraftFor(storyID, attemptID, "qa", feedback)
		return errPostRebaseQA
	}
	EmitStageCompleted(m.eventStore, m.projStore, "monitor", storyID, "qa_post_rebase", "success", qaStart)
	log.Printf("[pipeline] post-rebase QA passed for %s", storyID)
	return nil
}
