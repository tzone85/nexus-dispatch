package engine

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Approval queue wiring — the monitor-side call sites for the pure helpers
// in approval_hooks.go. Every method here is a no-op (or falls back to the
// pre-approval behaviour) when no queue is wired, so the monitor works
// unchanged for callers that never call SetApprovalQueue.
//
// Decision points:
//   - security finding: pauseOnSecurityFinding (postExecutionPipeline, step 2.5)
//   - merge gate:       mergeGate (postExecutionPipeline, step 3, before the merger)
//   - merge failure:    handleMergeFailure (conflict escalation → approval + pause)
//   - integration:      checkIntegration in integration_gate.go
//   - rejection:        ReconcileRejectedApprovals (nxd resume) + mergeGate

// approvalsHint tells the operator how to unblock a pipeline waiting on an
// approval.
const approvalsHint = "decide with `nxd approvals list` and `nxd approvals approve|reject <id>`, then `nxd resume <req>`"

// WithMonApprovalQueue wires the human approval queue. Nil disables approvals.
func WithMonApprovalQueue(q *approvals.Queue) MonitorOption {
	return func(m *Monitor) { m.approvals = q }
}

// SetApprovalQueue wires the human approval queue (see WithMonApprovalQueue).
func (m *Monitor) SetApprovalQueue(q *approvals.Queue) {
	m.Configure(WithMonApprovalQueue(q))
}

// pauseOnSecurityFinding pauses the requirement after the security gate
// flagged the story. When approvals.require_for lists security_finding a
// pending approval is recorded first so the decision is tracked in the queue.
func (m *Monitor) pauseOnSecurityFinding(storyID, reqID, summary string) {
	reason := fmt.Sprintf("security gate: %s (review the finding, then fix on the branch or `nxd resume <req>` to proceed)", summary)
	if ApprovalRequired(m.config.Approvals, approvals.KindSecurityFinding) {
		it, _, err := RequestSecurityApproval(m.approvals, reqID, storyID, summary)
		switch {
		case err != nil:
			log.Printf("[approvals] record security finding for %s: %v", storyID, err)
		case it.ID != "":
			reason = fmt.Sprintf("security gate: %s — approval %s pending; %s", summary, it.ID, approvalsHint)
		}
	}
	m.pauseRequirement(storyID, reason)
}

// mergeGate runs right before the merger. It returns true when the pipeline
// must stop: the story has an unhandled rejected approval (reset to draft), or
// an approval is pending (STORY_MERGE_READY so `nxd merge` can finish the job
// once approved, then the requirement pauses). When approvals.require_for
// lists "merge" every merge first records a pending merge approval.
func (m *Monitor) mergeGate(storyID, attemptID, reqID, branch string) bool {
	if m.approvals == nil {
		return false
	}
	if it, rejected := unhandledRejection(m.approvals, m.eventStore, reqID, storyID); rejected {
		m.resetStoryToDraftFor(storyID, attemptID, "approvals", rejectionReason(it))
		return true
	}
	if ApprovalRequired(m.config.Approvals, approvals.KindMerge) && !mergeApprovedForAttempt(m.approvals, m.eventStore, reqID, storyID) {
		if _, _, err := RequestMergeApproval(m.approvals, reqID, storyID, branch); err != nil {
			log.Printf("[approvals] record merge approval for %s: %v", storyID, err)
		}
	}
	blocked, reason := MergeBlockedByApproval(m.approvals, reqID, storyID)
	if !blocked {
		return false
	}
	emitEventOrLog(m.eventStore, m.projStore, state.NewEventForAttempt(state.EventStoryMergeReady, "approvals", storyID, attemptID, map[string]any{
		"reason": "awaiting approval",
	}))
	m.pauseRequirement(storyID, fmt.Sprintf("%s; once approved run `nxd merge %s`", reason, storyID))
	return true
}

// rejectionReason is the reset feedback for a rejected approval.
func rejectionReason(it approvals.Item) string {
	reason := fmt.Sprintf("approval %s (%s) rejected by %s", it.ID, it.Kind, it.DecidedBy)
	if it.Note != "" {
		reason += ": " + it.Note
	}
	return reason
}

// unhandledRejection returns the oldest rejected approval for the story that
// has not yet been acted on. A rejection is handled once the story was reset
// (STORY_REVIEW_FAILED / STORY_RESET) after the decision — so a story that
// was re-attempted after the human said no is not reset again for the same
// decision.
func unhandledRejection(q *approvals.Queue, es state.EventStore, reqID, storyID string) (approvals.Item, bool) {
	for _, it := range q.All(reqID) {
		if it.StoryID != storyID || it.Status != approvals.StatusRejected {
			continue
		}
		if !storyResetAfter(es, storyID, it.DecidedAt) {
			return it, true
		}
	}
	return approvals.Item{}, false
}

// mergeApprovedForAttempt reports whether a human approved the merge of the
// story's current attempt: an approved "merge" item exists and the story was
// not reset after that decision (a reset means a new attempt, which needs its
// own approval).
func mergeApprovedForAttempt(q *approvals.Queue, es state.EventStore, reqID, storyID string) bool {
	for _, it := range q.All(reqID) {
		if it.StoryID == storyID && it.Kind == approvals.KindMerge && it.Status == approvals.StatusApproved &&
			!storyResetAfter(es, storyID, it.DecidedAt) {
			return true
		}
	}
	return false
}

// storyResetAfter reports whether the story was reset to draft after t.
func storyResetAfter(es state.EventStore, storyID string, t time.Time) bool {
	for _, typ := range []state.EventType{state.EventStoryReviewFailed, state.EventStoryReset} {
		evts, err := es.List(state.EventFilter{Type: typ, StoryID: storyID, After: t})
		if err != nil {
			log.Printf("[approvals] list %s for %s: %v", typ, storyID, err)
			continue
		}
		if len(evts) > 0 {
			return true
		}
	}
	return false
}

// ReconcileRejectedApprovals is run by `nxd resume`: every story of reqID
// that is waiting to merge (pr_submitted / merge_ready) but whose approval a
// human rejected is reset to draft so it is re-dispatched instead of sitting
// in the merge queue forever. Returns the IDs of the stories reset.
func ReconcileRejectedApprovals(q *approvals.Queue, es state.EventStore, ps state.ProjectionStore, reqID string) []string {
	if q == nil {
		return nil
	}
	stories, err := ps.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		log.Printf("[approvals] list stories for %s: %v", reqID, err)
		return nil
	}
	var reset []string
	for _, st := range stories {
		if !awaitingMergeStatuses[st.Status] {
			continue
		}
		it, rejected := unhandledRejection(q, es, reqID, st.ID)
		if !rejected {
			continue
		}
		emitEventOrLog(es, ps, state.NewEvent(state.EventStoryReset, "approvals", st.ID, map[string]any{
			"reason":      rejectionReason(it),
			"approval_id": it.ID,
		}))
		reset = append(reset, st.ID)
	}
	return reset
}

// conflictApprovalFiles extracts the conflicted file from a typed conflict
// escalation error, reporting whether err is one.
func conflictApprovalFiles(err error) ([]string, bool) {
	var tooLarge *ConflictTooLargeError
	if errors.As(err, &tooLarge) {
		return []string{tooLarge.File}, true
	}
	var escalated *ConflictEscalatedError
	if errors.As(err, &escalated) {
		return []string{escalated.File}, true
	}
	return nil, false
}

// handleMergeFailure decides what happens when rebaseAndMerge fails and
// returns the devdb outcome for the release. Order matters: post-rebase QA
// already reset the story; capacity and fatal API errors pause without
// escalating; a conflict the resolver escalated to a human becomes a pending
// conflict_resolution approval + pause (a retry would hit the same conflict);
// everything else resets the story to draft with the error as feedback.
func (m *Monitor) handleMergeFailure(storyID, attemptID, reqID string, err error) devdb.StoryOutcome {
	if errors.Is(err, errPostRebaseQA) {
		log.Printf("[pipeline] %s not merged: %v", storyID, err)
		return devdb.OutcomeFailed
	}
	if m.pauseIfCapacity(storyID, "merge/conflict-resolution", err) {
		return devdb.OutcomePaused
	}
	if llm.IsFatalAPIError(err) {
		log.Printf("[pipeline] FATAL: non-retryable API error during merge for %s: %v", storyID, err)
		m.pauseRequirement(storyID, fmt.Sprintf("fatal API error during merge: %v", err))
		return devdb.OutcomePaused
	}
	if files, ok := conflictApprovalFiles(err); ok && ApprovalRequired(m.config.Approvals, approvals.KindConflictResolution) {
		reason := fmt.Sprintf("rebase conflict in %v needs a human: %v — resolve on the branch or decide via `nxd approvals list`", files, err)
		it, _, reqErr := RequestConflictApproval(m.approvals, reqID, storyID, files)
		switch {
		case reqErr != nil:
			log.Printf("[approvals] record conflict approval for %s: %v", storyID, reqErr)
		case it.ID != "":
			reason = fmt.Sprintf("rebase conflict in %v escalated — approval %s pending; %s", files, it.ID, approvalsHint)
		}
		m.pauseRequirement(storyID, reason)
		return devdb.OutcomePaused
	}
	log.Printf("[pipeline] merge error for %s: %v", storyID, err)
	m.resetStoryToDraftFor(storyID, attemptID, "merger", fmt.Sprintf("merge/rebase error: %v", err))
	return devdb.OutcomeFailed
}
