package engine

import (
	"fmt"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/config"
)

// Approval hooks — pure helpers the monitor calls at its decision points so
// that conflict resolutions, post-merge integration failures and security
// findings wait for a human instead of being auto-resolved, and so a merge
// cannot proceed while such an approval is pending.
//
// All helpers are nil-safe: a nil *approvals.Queue means the approval queue
// is not wired and every hook is a no-op (nothing requested, nothing blocked).
//
// Integration (one line each, monitor.go is owned by another workstream):
//
//	Monitor:                  add field `approvals *approvals.Queue` +
//	                          `func (m *Monitor) SetApprovalQueue(q *approvals.Queue) { m.approvals = q }`
//	                          resume.go: `mon.SetApprovalQueue(approvalQueue)` after approvals.Load(s.Events)
//	conflict escalation:      in ConflictResolver.emitEscalationEvent (conflict_resolver.go:629) or right after
//	                          rebaseAndMerge returns a conflict error in postExecutionPipeline:
//	                          `RequestConflictApproval(m.approvals, story.ReqID, storyID, conflictedFiles)`
//	integration failure:      monitor.go:833 after emitting STORY_INTEGRATION_FAILED:
//	                          `if _, asked, _ := RequestIntegrationApproval(m.approvals, story.ReqID, storyID, buildErr.Error()); asked { m.pauseRequirement(storyID, "post-merge build failed — awaiting approval (nxd approvals list)") }`
//	security finding:         monitor.go:765 in `case !passed:` before pauseRequirement:
//	                          `RequestSecurityApproval(m.approvals, story.ReqID, storyID, summary)`
//	merge gate:               monitor.go:786 before `if m.merger != nil {`:
//	                          `if blocked, reason := MergeBlockedByApproval(m.approvals, story.ReqID, storyID); blocked { m.pauseRequirement(storyID, reason); return }`
//	watchdog.go:63            `case runtime.StatusPermissionPrompt: if !w.cfg.AutoApprovePrompts { result.Action = "permission_prompt"; return result }`
//	                          with WatchdogConfig.AutoApprovePrompts = cfg.AutoApprovePrompts(runtimeName) (config.Config method)
//
// Whether a kind requires approval is decided by approvals.require_for; use
// ApprovalRequired(cfg, kind) at the call site when the operator disabled a
// kind (the Request* helpers always record when called).

// ApprovalRequired reports whether approvals.require_for lists kind.
func ApprovalRequired(cfg config.ApprovalsConfig, kind approvals.Kind) bool {
	return cfg.Requires(string(kind))
}

// requestOnce records a pending approval unless one of the same kind is
// already pending for the story (a retried pipeline stage must not spam the
// queue). Returns the item and whether a NEW item was created.
func requestOnce(q *approvals.Queue, reqID, storyID string, kind approvals.Kind, summary, details string) (approvals.Item, bool, error) {
	if q == nil {
		return approvals.Item{}, false, nil
	}
	if existing := q.PendingForStory(reqID, storyID, kind); len(existing) > 0 {
		return existing[0], false, nil
	}
	it, err := q.Request(reqID, storyID, kind, summary, details)
	if err != nil {
		return approvals.Item{}, false, err
	}
	return it, true, nil
}

// RequestConflictApproval asks a human to sign off on an LLM-resolved (or
// escalated) rebase conflict before the story merges.
func RequestConflictApproval(q *approvals.Queue, reqID, storyID string, files []string) (approvals.Item, bool, error) {
	summary := fmt.Sprintf("conflict resolution on %d file(s) needs review", len(files))
	if len(files) == 1 {
		summary = "conflict resolution on " + files[0] + " needs review"
	}
	return requestOnce(q, reqID, storyID, approvals.KindConflictResolution, summary, strings.Join(files, "\n"))
}

// RequestIntegrationApproval asks a human how to proceed after the post-merge
// integration build failed on the mainline.
func RequestIntegrationApproval(q *approvals.Queue, reqID, storyID, buildErr string) (approvals.Item, bool, error) {
	return requestOnce(q, reqID, storyID, approvals.KindIntegrationFailure,
		"post-merge integration build failed on mainline", buildErr)
}

// RequestSecurityApproval asks a human to accept or reject a security-gate
// finding at/above the gate severity.
func RequestSecurityApproval(q *approvals.Queue, reqID, storyID, summary string) (approvals.Item, bool, error) {
	return requestOnce(q, reqID, storyID, approvals.KindSecurityFinding,
		"security gate flagged the story", summary)
}

// RequestMergeApproval gates a merge on an explicit human OK (kind "merge").
func RequestMergeApproval(q *approvals.Queue, reqID, storyID, branch string) (approvals.Item, bool, error) {
	return requestOnce(q, reqID, storyID, approvals.KindMerge,
		"merge of "+branch+" awaits approval", "")
}

// MergeBlockedByApproval reports whether the story (or, when storyID is
// empty, any story of the requirement) has a pending approval, with a
// human-readable reason listing the item IDs so the operator can resolve them
// via `nxd approvals approve <id>`. A rejected approval does NOT block here —
// rejection is surfaced by the caller as a story reset.
func MergeBlockedByApproval(q *approvals.Queue, reqID, storyID string) (bool, string) {
	if q == nil {
		return false, ""
	}
	var pending []approvals.Item
	if storyID == "" {
		pending = q.Pending(reqID)
	} else {
		pending = q.PendingForStory(reqID, storyID, "")
	}
	if len(pending) == 0 {
		return false, ""
	}
	parts := make([]string, 0, len(pending))
	for _, it := range pending {
		parts = append(parts, fmt.Sprintf("%s (%s)", it.ID, it.Kind))
	}
	return true, fmt.Sprintf("merge blocked: %d approval(s) pending — %s; resolve with `nxd approvals approve|reject <id>`",
		len(pending), strings.Join(parts, ", "))
}

// ApprovalRejected reports whether the story has a rejected item of kind
// (empty kind = any) — the caller resets the story instead of merging.
func ApprovalRejected(q *approvals.Queue, reqID, storyID string, kind approvals.Kind) (bool, approvals.Item) {
	if q == nil {
		return false, approvals.Item{}
	}
	for _, it := range q.All(reqID) {
		if it.StoryID == storyID && it.Status == approvals.StatusRejected && (kind == "" || it.Kind == kind) {
			return true, it
		}
	}
	return false, approvals.Item{}
}
