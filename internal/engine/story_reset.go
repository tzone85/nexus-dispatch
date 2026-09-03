package engine

import (
	"fmt"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// resetStoryToDraft uses the EscalationMachine to decide whether the story
// should be retried at the current tier, escalated to the next tier, or
// paused (all tiers exhausted). It emits the appropriate events so the
// dispatcher picks the story back up with the correct routing.
//
// This is the attempt-less entry point for callers that act on a story
// rather than on one agent run (manager / tech-lead paths). The
// post-execution pipeline uses resetStoryToDraftFor so the events it emits
// carry the attempt that failed.
func (m *Monitor) resetStoryToDraft(storyID, fromAgent, reason string) {
	m.resetStoryToDraftFor(storyID, "", fromAgent, reason)
}

// resetStoryToDraftFor is resetStoryToDraft with attempt stamping: every
// STORY_ESCALATED / STORY_REVIEW_FAILED it emits is tagged with attemptID
// (see attempt_id.go). An empty attemptID emits legacy, unstamped events.
func (m *Monitor) resetStoryToDraftFor(storyID, attemptID, fromAgent, reason string) {
	shouldEsc, nextTier, err := m.escalation.ShouldEscalate(storyID)
	if err != nil {
		log.Printf("[pipeline] escalation check error for %s: %v", storyID, err)
	}

	if shouldEsc {
		currentTier, _ := m.escalation.CurrentTier(storyID)
		if nextTier >= 4 {
			m.pauseRequirement(storyID, fmt.Sprintf(
				"story exhausted all escalation tiers (%d): %s", currentTier, reason,
			))
			return
		}
		log.Printf("[pipeline] escalating %s from tier %d to tier %d: %s", storyID, currentTier, nextTier, reason)
		emitEventOrLog(m.eventStore, m.projStore,
			state.NewEventForAttempt(state.EventStoryEscalated, fromAgent, storyID, attemptID, map[string]any{
				"from_tier": currentTier,
				"to_tier":   nextTier,
				"reason":    reason,
			}))

		// Record Bayesian outcome: escalation is a failure for the current role.
		m.recordBayesianEscalation(storyID, currentTier)

		// Also reset to draft so the dispatcher picks it up at the new tier.
		emitEventOrLog(m.eventStore, m.projStore,
			state.NewEventForAttempt(state.EventStoryReviewFailed, fromAgent, storyID, attemptID, map[string]any{
				"reason": fmt.Sprintf("escalated to tier %d: %s", nextTier, reason),
			}))
		return
	}

	// Normal reset within current tier.
	retryCount, _ := m.escalation.RetryCountAtCurrentTier(storyID)
	currentTier, _ := m.escalation.CurrentTier(storyID)
	maxRetries := m.escalation.MaxRetriesForTier(currentTier)
	log.Printf("[pipeline] reset %s to draft (attempt %d/%d at tier %d): %s",
		storyID, retryCount+1, maxRetries, currentTier, reason)

	emitEventOrLog(m.eventStore, m.projStore,
		state.NewEventForAttempt(state.EventStoryReviewFailed, fromAgent, storyID, attemptID, map[string]any{
			"reason": reason,
		}))
}
