package engine

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Wave completion: deciding, between waves, whether a requirement is done,
// waiting on open PRs, or has more work to dispatch.
//
// A story in pr_submitted (QA passed, PR opened, not yet merged) or
// merge_ready (review_before_merge is on and a human must merge) is NOT
// complete: its code is not on the base branch, so dependents that build on
// it must not start and the requirement must not be reported complete.
// Before this file both were counted as done, which dispatched dependents
// against a base branch that lacked their parent's changes and emitted
// REQ_COMPLETED with PRs still open.

// awaitingMergeStatuses are story statuses where the work is finished but
// not yet on the base branch.
var awaitingMergeStatuses = map[string]bool{
	"pr_submitted": true,
	"merge_ready":  true,
}

// waveProgress is the classification of a requirement's stories.
type waveProgress struct {
	// completed holds stories whose code is on the base branch (merged) or
	// that were replaced by children (split) — the DAG's notion of "done".
	completed map[string]bool
	// awaitingMerge lists stories with an open PR / pending merge decision,
	// in projection order.
	awaitingMerge []string
	// pending counts everything else (draft, in_progress, review, qa, ...).
	pending int
}

// allMerged reports whether every story is merged or split.
func (p waveProgress) allMerged() bool { return p.pending == 0 && len(p.awaitingMerge) == 0 }

// onlyAwaitingMerge reports whether the only unfinished stories are waiting
// on a PR merge.
func (p waveProgress) onlyAwaitingMerge() bool { return p.pending == 0 && len(p.awaitingMerge) > 0 }

// isAwaitingMerge reports whether storyID has an open PR / pending merge.
func (p waveProgress) isAwaitingMerge(storyID string) bool {
	for _, id := range p.awaitingMerge {
		if id == storyID {
			return true
		}
	}
	return false
}

// dispatchable returns the planned stories the dispatcher may consider this
// wave: everything except stories awaiting merge. Those are not completed
// (so their dependents stay blocked) but must not be re-dispatched either.
func (p waveProgress) dispatchable(planned []PlannedStory) []PlannedStory {
	if len(p.awaitingMerge) == 0 {
		return planned
	}
	out := make([]PlannedStory, 0, len(planned))
	for _, ps := range planned {
		if !p.isAwaitingMerge(ps.ID) {
			out = append(out, ps)
		}
	}
	return out
}

// classifyStories buckets a requirement's stories for wave planning.
func classifyStories(stories []state.Story) waveProgress {
	p := waveProgress{completed: make(map[string]bool, len(stories))}
	for _, s := range stories {
		switch {
		case s.Status == "merged" || s.Status == "split":
			p.completed[s.ID] = true
		case awaitingMergeStatuses[s.Status]:
			p.awaitingMerge = append(p.awaitingMerge, s.ID)
		default:
			p.pending++
		}
	}
	return p
}

// CompletedStories returns the DAG's notion of "done" for a requirement's
// stories — merged or split — as the completed set the dispatcher expects.
// `nxd resume` uses it so a manual resume and the monitor's auto-resume agree:
// a pr_submitted / merge_ready story is NOT complete (its code is not on the
// base branch yet) and must not unblock its dependents.
func CompletedStories(stories []state.Story) map[string]bool {
	return classifyStories(stories).completed
}

// DispatchableStories filters the planned stories a manual `nxd resume` may
// hand to the dispatcher: stories awaiting merge (pr_submitted / merge_ready)
// are neither completed nor re-dispatchable, exactly as in the monitor's
// auto-resume path (waveProgress.dispatchable).
func DispatchableStories(stories []state.Story, planned []PlannedStory) []PlannedStory {
	return classifyStories(stories).dispatchable(planned)
}

// emitPendingReview records that the requirement is waiting on open PRs.
// The payload uses req_id (not id) on purpose: REQ_PENDING_REVIEW with an
// "id" flips the requirement into the plan-approval pending_review status,
// which is a different gate (`nxd approve`). Here the requirement stays in
// progress; merging the PRs and running `nxd resume` continues it.
func (m *Monitor) emitPendingReview(reqID string, openPRs, blocked []string) {
	log.Printf("[auto-resume] requirement %s is waiting on %d open PR(s): %v — merge them, then `nxd resume %s`",
		reqID, len(openPRs), openPRs, reqID)
	payload := map[string]any{
		"req_id":            reqID,
		"open_pr_story_ids": openPRs,
		"reason":            "stories awaiting PR merge",
	}
	if len(blocked) > 0 {
		payload["blocked_story_ids"] = blocked
	}
	emitEventOrLog(m.eventStore, m.projStore,
		state.NewEvent(state.EventReqPendingReview, "monitor", "", payload))
}

// reportNoDispatch explains why a wave produced no assignments: stories are
// blocked behind open PRs (REQ_PENDING_REVIEW), dependencies are simply not
// met yet, or nothing can ever be dispatched (PIPELINE_STALLED).
func (m *Monitor) reportNoDispatch(rc *RunContext, progress waveProgress, stories []state.Story) {
	if progress.pending == 0 {
		log.Printf("[auto-resume] no stories ready for next wave (dependencies not met)")
		return
	}
	if len(progress.awaitingMerge) > 0 {
		var blocked []string
		for _, s := range stories {
			if !progress.completed[s.ID] && !awaitingMergeStatuses[s.Status] {
				blocked = append(blocked, s.ID)
			}
		}
		m.emitPendingReview(rc.ReqID, progress.awaitingMerge, blocked)
		return
	}
	log.Printf("[STALL] requirement %s has %d unfinished stories but none are dispatchable — all escalation tiers exhausted or dependencies unmet", rc.ReqID, progress.pending)
	log.Printf("[STALL] run 'nxd status --req %s' to inspect, then 'nxd resume %s --godmode' to retry", rc.ReqID, rc.ReqID)
	emitEventOrLog(m.eventStore, m.projStore,
		state.NewEvent("PIPELINE_STALLED", "monitor", "", map[string]any{
			"req_id":        rc.ReqID,
			"pending_count": progress.pending,
			"total_stories": len(stories),
			"reason":        "no dispatchable stories — escalation tiers exhausted",
		}))
}

// completeRequirement runs the end-of-requirement steps once every story is
// merged: docs generation, pulling the composed mainline, branch cleanup and
// the completion gate (REQ_COMPLETED / REQ_BLOCKED).
func (m *Monitor) completeRequirement(ctx context.Context, rc *RunContext, repoDir string, stories []state.Story) {
	log.Printf("[auto-resume] all %d stories complete for requirement %s", len(stories), rc.ReqID)

	// Generate/update README + docs/ (SVG diagrams, training guide, ADRs,
	// index) as the final step, before the tree is verified.
	if m.docClient != nil {
		storyTitles := make([]string, len(stories))
		for i, s := range stories {
			storyTitles[i] = "- " + s.Title
		}
		reqTitle := rc.ReqID
		if req, reqErr := m.projStore.GetRequirement(rc.ReqID); reqErr == nil {
			reqTitle = req.Title
		}
		generateDocumentation(ctx, repoDir, reqTitle, storyTitles, m.docClient, m.docModel)
	}

	// Pull merged changes into the local checkout FIRST so verification
	// runs against the true composed mainline (all merged stories), not a
	// stale checkout. Without this the gate would verify the wrong tree.
	pullBaseAfterMerge(repoDir, m.config.Merge.BaseBranch)

	// Leave the workspace neat: remove dangling branches (and their open
	// PRs) from stories that never merged. Merged branches are already gone.
	m.cleanupDanglingBranches(rc.ReqID, repoDir)

	// Completion gate: verify the composed mainline (build + tests) and
	// auto-fix a red build up to a bounded number of cycles. Only emit
	// REQ_COMPLETED when verification is green; otherwise emit REQ_BLOCKED
	// so a requirement is never reported complete on code that does not
	// compile. Falls back to the legacy advisory path when no gate is wired.
	if m.completionGate != nil {
		if m.completionGate.Run(ctx, rc.ReqID, repoDir) {
			emitEventOrLog(m.eventStore, m.projStore,
				state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": rc.ReqID}))
		} else {
			log.Printf("[gate] %s: completion blocked — see .nxd-fix-gaps.md; run 'nxd resume %s --godmode' after addressing the gaps", rc.ReqID, rc.ReqID)
			emitEventOrLog(m.eventStore, m.projStore,
				state.NewEvent(state.EventReqBlocked, "monitor", "", map[string]any{"id": rc.ReqID}))
		}
		return
	}

	// Legacy advisory verification (no gate wired): check build/tests and
	// write a fix-gaps file, but complete the requirement regardless.
	verifyResult := RunVerificationLoop(ctx, repoDir, 1)
	if ShouldRunFixCycle(verifyResult) {
		log.Printf("[verify] cycle 1 found %d gaps — generating fix requirement", len(verifyResult.Gaps))
		if fixReq := GapsToRequirement(verifyResult.Gaps, filepath.Base(repoDir)); fixReq != "" {
			fixPath := filepath.Join(repoDir, ".nxd-fix-gaps.md")
			if err := os.WriteFile(fixPath, []byte(fixReq), 0o600); err != nil {
				log.Printf("[verify] failed to write fix requirement to %s: %v", fixPath, err)
			} else {
				log.Printf("[verify] fix requirement written to %s — run 'nxd req --file .nxd-fix-gaps.md --godmode' to auto-fix", fixPath)
			}
		}
	} else {
		log.Printf("[verify] cycle 1 clean — no critical gaps found")
	}

	// Mark requirement complete.
	emitEventOrLog(m.eventStore, m.projStore,
		state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": rc.ReqID}))
}
