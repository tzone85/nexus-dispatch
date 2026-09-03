package engine

import (
	"context"
	"fmt"
	"log"

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
	m.pauseRequirement(storyID, fmt.Sprintf(
		"post-merge integration build failed after merging %s: %v (fix the base branch, then `nxd resume`; the Tech Lead fix suggestion is recorded on STORY_INTEGRATION_FAILED)",
		storyID, truncateDiff(buildErr.Error(), 500)))
	return true
}
