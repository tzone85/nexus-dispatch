package engine

import (
	"context"
	"fmt"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Post-merge integration gate.
//
// Per-story QA runs in the story's worktree, so it cannot see cross-story
// breakage: story A changes an interface, story B (merged a minute later)
// calls the old method. After each merge the monitor therefore builds the
// base branch. A failure is recorded as STORY_INTEGRATION_FAILED, handed to
// the TechLeadFixer for a diagnosis, and — unless
// qa.pause_on_integration_failure is false — pauses the requirement so the
// next wave is not branched from a red mainline.

// checkIntegration runs the post-merge build for storyID's merge. It returns
// true when the requirement was paused (the caller stops its pipeline). The
// build is only run when a TechLeadFixer is wired, matching the previous
// gating.
func (m *Monitor) checkIntegration(ctx context.Context, storyID, attemptID, repoDir string) bool {
	if m.techLeadFixer == nil {
		return false
	}
	build := m.integrationBuild
	if build == nil {
		build = runIntegrationBuild
	}
	buildErr := build(repoDir)
	if buildErr == nil {
		return false
	}

	pause := m.config.QA.PauseOnIntegrationFailure
	log.Printf("[pipeline] POST-MERGE BUILD FAILED for %s on %s: %v", storyID, m.baseBranch(repoDir), buildErr)
	emitEventOrLog(m.eventStore, m.projStore,
		state.NewEventForAttempt(state.EventStoryIntegrationFailed, "monitor", storyID, attemptID, map[string]any{
			"error":  buildErr.Error(),
			"paused": pause,
		}))
	m.techLeadFixer.DispatchIntegrationFix(ctx, storyID, repoDir, buildErr.Error())

	if !pause {
		log.Printf("[pipeline] qa.pause_on_integration_failure=false — continuing with a red mainline for %s", storyID)
		return false
	}
	m.pauseRequirement(storyID, m.integrationPauseReason(storyID, buildErr))
	return true
}

// integrationPauseReason builds the pause message and, when
// approvals.require_for lists integration_failure, records a pending
// integration_failure approval so the human decision is tracked in the queue
// (approval_wiring.go). A queue error is logged and the plain reason is used.
func (m *Monitor) integrationPauseReason(storyID string, buildErr error) string {
	reason := fmt.Sprintf(
		"post-merge integration build failed after merging %s: %v (fix the base branch, then `nxd resume`; the Tech Lead fix suggestion is recorded on STORY_INTEGRATION_FAILED)",
		storyID, truncateDiff(buildErr.Error(), 500))
	if !ApprovalRequired(m.config.Approvals, approvals.KindIntegrationFailure) {
		return reason
	}
	reqID := ""
	if story, err := m.projStore.GetStory(storyID); err == nil {
		reqID = story.ReqID
	}
	it, _, err := RequestIntegrationApproval(m.approvals, reqID, storyID, buildErr.Error())
	switch {
	case err != nil:
		log.Printf("[approvals] record integration failure for %s: %v", storyID, err)
	case it.ID != "":
		reason = fmt.Sprintf("post-merge integration build failed after merging %s: %v — approval %s pending; %s",
			storyID, truncateDiff(buildErr.Error(), 500), it.ID, approvalsHint)
	}
	return reason
}
