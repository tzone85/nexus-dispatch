package engine

import (
	"testing"
)

// TestConvertToolResult_SplitChildrenGetUniqueSuffixes reproduces the bug
// where tool-call splits built SplitChildConfig without a Suffix, so
// ValidateSplit rejected every ≥2-child split as "duplicate child suffix".
func TestConvertToolResult_SplitChildrenGetUniqueSuffixes(t *testing.T) {
	tr := ManagerToolResult{Split: &StorySplit{
		OriginalStoryID: "s-9",
		NewStories: []ManagerSplitChild{
			{Title: "Add model", Description: "d1", Complexity: 2},
			{Title: "Add handler", Description: "d2", Complexity: 3},
		},
	}}

	action := convertToolResultToManagerAction(tr)
	if action.Action != "split" || action.SplitConfig == nil {
		t.Fatalf("expected split action, got %+v", action)
	}
	children := action.SplitConfig.Children
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	if children[0].Suffix != "a" || children[1].Suffix != "b" {
		t.Fatalf("suffixes = %q,%q; want a,b", children[0].Suffix, children[1].Suffix)
	}

	// The same children must now pass ValidateSplit and produce the ids the
	// monitor derives (<story>-<suffix>).
	esc := NewEscalationMachine(nil, defaultRoutingConfig())
	split := make([]SplitChild, 0, len(children))
	for _, c := range children {
		split = append(split, SplitChild{ID: "s-9-" + c.Suffix, Suffix: c.Suffix, Title: c.Title, Complexity: c.Complexity})
	}
	if err := esc.ValidateSplit(0, split, 5); err != nil {
		t.Fatalf("ValidateSplit rejected tool-result split: %v", err)
	}
	if split[0].ID != "s-9-a" || split[1].ID != "s-9-b" {
		t.Fatalf("child ids = %q,%q; want s-9-a,s-9-b", split[0].ID, split[1].ID)
	}
}

func TestSplitChildSuffix_Sequence(t *testing.T) {
	tests := []struct {
		index int
		want  string
	}{
		{0, "a"}, {1, "b"}, {25, "z"}, {26, "aa"}, {27, "ab"}, {52, "ba"},
	}
	for _, tc := range tests {
		if got := splitChildSuffix(tc.index); got != tc.want {
			t.Errorf("splitChildSuffix(%d) = %q, want %q", tc.index, got, tc.want)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s := splitChildSuffix(i)
		if seen[s] {
			t.Fatalf("suffix %q repeated at index %d", s, i)
		}
		seen[s] = true
	}
}
