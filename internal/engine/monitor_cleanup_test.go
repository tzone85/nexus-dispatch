package engine

import (
	"sort"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestDanglingBranchesToClean(t *testing.T) {
	stories := []state.Story{
		{ID: "a-s-001", Status: "merged", Branch: "nxd/a-s-001"},
		{ID: "a-s-002", Status: "pr_submitted", Branch: "nxd/a-s-002"},
		{ID: "a-s-003", Status: "draft", Branch: ""},
		{ID: "a-s-004", Status: "split"},
		{ID: "a-s-005", Status: "failed", Branch: "nxd/a-s-005"},
		{ID: "a-s-002", Status: "pr_submitted", Branch: "nxd/a-s-002"},
	}

	got := danglingBranchesToClean(stories, "master")
	sort.Strings(got)
	// a-s-003 has an empty Branch, so it exercises the canonical-prefix
	// fallback: it must resolve to "nxd/a-s-003", matching the dispatcher.
	want := []string{"nxd/a-s-002", "nxd/a-s-003", "nxd/a-s-005"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDanglingBranchesToClean_NeverDeletesBaseBranch(t *testing.T) {
	stories := []state.Story{
		{ID: "x", Status: "pr_submitted", Branch: "master"},
		{ID: "y", Status: "pr_submitted", Branch: "nxd/y"},
	}
	got := danglingBranchesToClean(stories, "master")
	for _, b := range got {
		if b == "master" {
			t.Fatalf("base branch must never be cleaned, got %v", got)
		}
	}
	if len(got) != 1 || got[0] != "nxd/y" {
		t.Fatalf("expected only nxd/y, got %v", got)
	}
}

func TestDanglingBranchesToClean_AllMergedIsEmpty(t *testing.T) {
	stories := []state.Story{
		{ID: "a", Status: "merged", Branch: "nxd/a"},
		{ID: "b", Status: "merged", Branch: "nxd/b"},
	}
	if got := danglingBranchesToClean(stories, "master"); len(got) != 0 {
		t.Fatalf("all-merged requirement should have nothing to clean, got %v", got)
	}
}
