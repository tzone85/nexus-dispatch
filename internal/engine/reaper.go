package engine

import (
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
}

// NewReaper creates a Reaper wired to the given configuration, git
// operations, and event store.
func NewReaper(cfg config.CleanupConfig, gitOps GitCleanupOps, es state.EventStore) *Reaper {
	return &Reaper{
		config:     cfg,
		gitOps:     gitOps,
		eventStore: es,
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

		r.eventStore.Append(state.NewEvent(state.EventWorktreePruned, "reaper", storyID, map[string]any{
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

			r.eventStore.Append(state.NewEvent(state.EventBranchDeleted, "reaper", storyID, map[string]any{
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

	cutoff := time.Now().AddDate(0, 0, -r.config.BranchRetentionDays)
	deleted := 0

	for _, b := range branches {
		if b.MergedAt.Before(cutoff) && r.gitOps.BranchExists(repoDir, b.Name) {
			if err := r.gitOps.DeleteBranch(repoDir, b.Name); err != nil {
				return deleted, fmt.Errorf("gc delete branch %s: %w", b.Name, err)
			}
			deleted++

			r.eventStore.Append(state.NewEvent(state.EventBranchDeleted, "reaper", b.StoryID, map[string]any{
				"branch": b.Name,
				"reason": "gc_retention_expired",
			}))
		}
	}

	if deleted > 0 {
		r.eventStore.Append(state.NewEvent(state.EventGCCompleted, "reaper", "", map[string]any{
			"branches_deleted": deleted,
		}))
	}

	return deleted, nil
}

// BranchInfo holds metadata about a branch eligible for garbage collection.
type BranchInfo struct {
	Name     string
	StoryID  string
	MergedAt time.Time
}
