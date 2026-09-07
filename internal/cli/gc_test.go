package cli

import (
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestMergedBranchInfos_UsesMergeTimeNotCreation proves branch-retention info
// is derived from a story's merge time, not its creation time. A story created
// long ago but merged recently must carry the recent MergedAt so the reaper
// keeps its branch for the full retention window.
func TestMergedBranchInfos_UsesMergeTimeNotCreation(t *testing.T) {
	created := time.Now().AddDate(0, 0, -30) // 30 days ago
	merged := time.Now().AddDate(0, 0, -1)   // yesterday

	stories := []state.Story{
		{ID: "s-1", Branch: "nxd/s-1", CreatedAt: created, MergedAt: merged},
		{ID: "s-2", Branch: "", CreatedAt: created, MergedAt: merged}, // no branch → skipped
	}

	got := mergedBranchInfos(stories)
	if len(got) != 1 {
		t.Fatalf("expected 1 branch (empty-branch story skipped), got %d", len(got))
	}
	if !got[0].MergedAt.Equal(merged) {
		t.Errorf("MergedAt must come from the merge time %v, got %v (creation was %v)", merged, got[0].MergedAt, created)
	}
	if got[0].MergedAt.Equal(created) {
		t.Error("MergedAt must not be the creation time")
	}
}

// TestMergedBranchInfos_FallsBackToCreatedAt covers pre-migration rows with no
// merged_at recorded: retention then falls back to creation time rather than a
// zero time (which would reap the branch immediately).
func TestMergedBranchInfos_FallsBackToCreatedAt(t *testing.T) {
	created := time.Now().AddDate(0, 0, -3)
	stories := []state.Story{
		{ID: "s-1", Branch: "nxd/s-1", CreatedAt: created}, // MergedAt zero
	}
	got := mergedBranchInfos(stories)
	if len(got) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(got))
	}
	if !got[0].MergedAt.Equal(created) {
		t.Errorf("zero MergedAt must fall back to CreatedAt %v, got %v", created, got[0].MergedAt)
	}
}
