package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// GitCleanupOps abstracts git worktree and branch operations for testability.
type GitCleanupOps interface {
	DeleteWorktree(repoDir, worktreePath string) error
	DeleteBranch(repoDir, branch string) error
	BranchExists(repoDir, branch string) bool
}

// ReapResult holds the outcome of a single reaper cleanup operation.
type ReapResult struct {
	WorktreePruned bool
	BranchDeleted  bool
	Deferred       bool
}

// Reaper handles post-merge cleanup: removing worktrees and branches.
// It supports immediate and deferred cleanup modes based on configuration.
type Reaper struct {
	config     config.CleanupConfig
	gitOps     GitCleanupOps
	eventStore state.EventStore
	projStore  state.ProjectionStore
	now        func() time.Time // the retention clock; tests pin it
}

// SetNow replaces the clock the retention cutoff is measured from.
func (r *Reaper) SetNow(now func() time.Time) { r.now = now }

// emit appends evt and projects it, so the projection watermark stays level
// with the log (BRANCH_DELETED and GC_COMPLETED have no projection of their
// own; projecting them just advances the watermark). Without the projection a
// standalone gc run would leave the projection behind the log and the next
// command's RebuildFrom would replay every archive's REQ_COMPLETED as plain
// "completed" — which is why the projection store is a constructor argument,
// like every other engine component, and not optional.
func (r *Reaper) emit(evt state.Event) {
	emitEventOrLog(r.eventStore, r.projStore, evt)
}

// NewReaper creates a Reaper wired to the given configuration, git
// operations, event store and projection store.
func NewReaper(cfg config.CleanupConfig, gitOps GitCleanupOps, es state.EventStore, ps state.ProjectionStore) *Reaper {
	return &Reaper{
		config:     cfg,
		gitOps:     gitOps,
		eventStore: es,
		projStore:  ps,
		now:        time.Now,
	}
}

// Reap cleans up the worktree and optionally the branch for a completed story.
// Worktree deletion is controlled by the WorktreePrune config ("immediate" or
// "deferred"). Branch deletion is controlled by BranchRetentionDays (0 = delete
// immediately).
func (r *Reaper) Reap(storyID, repoDir, worktreePath, branch string) (ReapResult, error) {
	result := ReapResult{}

	// Worktree cleanup
	if r.config.WorktreePrune == "immediate" {
		if err := r.gitOps.DeleteWorktree(repoDir, worktreePath); err != nil {
			return result, fmt.Errorf("delete worktree %s: %w", worktreePath, err)
		}
		result.WorktreePruned = true

		r.emit(state.NewEvent(state.EventWorktreePruned, "reaper", storyID, map[string]any{
			"worktree_path": worktreePath,
			"mode":          "immediate",
		}))
	} else {
		result.Deferred = true
	}

	// Branch cleanup
	if r.config.BranchRetentionDays == 0 {
		if r.gitOps.BranchExists(repoDir, branch) {
			if err := r.gitOps.DeleteBranch(repoDir, branch); err != nil {
				return result, fmt.Errorf("delete branch %s: %w", branch, err)
			}
			result.BranchDeleted = true

			r.emit(state.NewEvent(state.EventBranchDeleted, "reaper", storyID, map[string]any{
				"branch": branch,
			}))
		}
	}

	return result, nil
}

// GarbageCollect removes branches that have exceeded the retention period.
// It checks each branch against the mergedAt timestamp and deletes branches
// older than BranchRetentionDays.
func (r *Reaper) GarbageCollect(repoDir string, branches []BranchInfo) (int, error) {
	if r.config.BranchRetentionDays <= 0 {
		return 0, nil
	}

	cutoff := r.now().AddDate(0, 0, -r.config.BranchRetentionDays)
	deleted := 0
	var errs []error

	for _, b := range branches {
		if b.MergedAt.Before(cutoff) && r.gitOps.BranchExists(repoDir, b.Name) {
			// Keep going on failure: one branch that git refuses to delete
			// must not skip every later branch, nor the GC_COMPLETED event
			// for the ones that were deleted.
			if err := r.gitOps.DeleteBranch(repoDir, b.Name); err != nil {
				errs = append(errs, fmt.Errorf("gc delete branch %s: %w", b.Name, err))
				continue
			}
			deleted++

			r.emit(state.NewEvent(state.EventBranchDeleted, "reaper", b.StoryID, map[string]any{
				"branch": b.Name,
				"reason": "gc_retention_expired",
			}))
		}
	}

	if deleted > 0 {
		r.emit(state.NewEvent(state.EventGCCompleted, "reaper", "", map[string]any{
			"branches_deleted": deleted,
			"repo_path":        repoDir, // one event per repo, so consumers can attribute it
		}))
	}

	return deleted, errors.Join(errs...)
}

// BranchInfo holds metadata about a branch eligible for garbage collection.
type BranchInfo struct {
	Name     string
	StoryID  string
	MergedAt time.Time
}
