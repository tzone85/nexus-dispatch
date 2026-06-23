package engine

import (
	"sort"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestDanglingBranchesToClean(t *testing.T) {
	stories := []state.Story{
		{ID: "a-s-001", Status: "merged", Branch: "vxd/a-s-001"},
		{ID: "a-s-002", Status: "pr_submitted", Branch: "vxd/a-s-002"},
		{ID: "a-s-003", Status: "draft", Branch: ""},
		{ID: "a-s-004", Status: "split"},
		{ID: "a-s-005", Status: "failed", Branch: "vxd/a-s-005"},
		{ID: "a-s-002", Status: "pr_submitted", Branch: "vxd/a-s-002"},
	}

	got := danglingBranchesToClean(stories, "master")
	sort.Strings(got)
	want := []string{"vxd/a-s-002", "vxd/a-s-003", "vxd/a-s-005"}

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
		{ID: "y", Status: "pr_submitted", Branch: "vxd/y"},
	}
	got := danglingBranchesToClean(stories, "master")
	for _, b := range got {
		if b == "master" {
			t.Fatalf("base branch must never be cleaned, got %v", got)
		}
	}
	if len(got) != 1 || got[0] != "vxd/y" {
		t.Fatalf("expected only vxd/y, got %v", got)
	}
}

func TestDanglingBranchesToClean_AllMergedIsEmpty(t *testing.T) {
	stories := []state.Story{
		{ID: "a", Status: "merged", Branch: "vxd/a"},
		{ID: "b", Status: "merged", Branch: "vxd/b"},
	}
	if got := danglingBranchesToClean(stories, "master"); len(got) != 0 {
		t.Fatalf("all-merged requirement should have nothing to clean, got %v", got)
	}
}
