package state_test

import (
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestStoryStarted: the projection is the evidence. A status list answered
// this question wrongly in both directions, which is why there is no status
// in here at all.
func TestStoryStarted(t *testing.T) {
	merged := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		story state.Story
		want  bool
	}{
		{"no branch, never merged", state.Story{ID: "s-1", Status: "draft"}, false},
		{"branch projected", state.Story{ID: "s-1", Status: "draft", Branch: "nxd/s-1"}, true},
		{"merged with no projected branch", state.Story{ID: "s-1", Status: "merged", MergedAt: merged}, true},
		{"archived, branch kept", state.Story{ID: "s-1", Status: "archived", Branch: "nxd/s-1"}, true},
		{"split parent", state.Story{ID: "s-1", Status: "split"}, false},
	} {
		if got := state.StoryStarted(tc.story); got != tc.want {
			t.Errorf("%s: StoryStarted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestStoryBranch_FallsBackToCanonical: a row projected without a branch
// still resolves to the name the dispatcher would have created, so cleanup
// never runs against a branch that does not exist — nor skips one that does.
func TestStoryBranch_FallsBackToCanonical(t *testing.T) {
	if got := state.StoryBranch(state.Story{ID: "s-1"}); got != "nxd/s-1" {
		t.Errorf("StoryBranch with no projected branch = %q, want nxd/s-1", got)
	}
	if got := state.StoryBranch(state.Story{ID: "s-1", Branch: "feature/x"}); got != "feature/x" {
		t.Errorf("a projected branch wins: %q", got)
	}
	if got := state.StoryBranch(state.Story{}); got != "" {
		t.Errorf("a story with no ID has no branch: %q", got)
	}
}
