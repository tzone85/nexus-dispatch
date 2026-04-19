package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func testEscalationStore(t *testing.T) *state.FileStore {
	t.Helper()
	dir := t.TempDir()
	fs, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func defaultRoutingConfig() config.RoutingConfig {
	return config.RoutingConfig{
		MaxRetriesBeforeEscalation: 2,
		MaxSeniorRetries:           2,
		MaxManagerAttempts:         2,
	}
}

func TestCurrentTier_NoEscalation(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	tier, err := esc.CurrentTier("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if tier != 0 {
		t.Errorf("expected tier 0, got %d", tier)
	}
}

func TestCurrentTier_AfterEscalation(t *testing.T) {
	fs := testEscalationStore(t)
	if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 0, "to_tier": 1,
	})); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	tier, err := esc.CurrentTier("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if tier != 1 {
		t.Errorf("expected tier 1, got %d", tier)
	}
}

func TestCurrentTier_MultipleEscalations(t *testing.T) {
	fs := testEscalationStore(t)
	if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 0, "to_tier": 1,
	})); err != nil {
		t.Fatal(err)
	}
	if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 1, "to_tier": 2,
	})); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	tier, err := esc.CurrentTier("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if tier != 2 {
		t.Errorf("expected tier 2, got %d", tier)
	}
}

func TestRetryCountAtCurrentTier(t *testing.T) {
	fs := testEscalationStore(t)

	// Two failures at tier 0.
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}

	// Escalate to tier 1.
	if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
		"from_tier": 0, "to_tier": 1,
	})); err != nil {
		t.Fatal(err)
	}

	// Small sleep so the next failure timestamp is strictly after the escalation.
	time.Sleep(5 * time.Millisecond)

	// One failure at tier 1.
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	count, err := esc.RetryCountAtCurrentTier("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 retry at tier 1, got %d", count)
	}
}

func TestRetryCountAtCurrentTier_NoEscalation(t *testing.T) {
	fs := testEscalationStore(t)

	// One failure at tier 0 (no escalation yet).
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	count, err := esc.RetryCountAtCurrentTier("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 retry at tier 0, got %d", count)
	}
}

func TestShouldEscalate_Yes(t *testing.T) {
	fs := testEscalationStore(t)
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, config.RoutingConfig{
		MaxRetriesBeforeEscalation: 2,
		MaxSeniorRetries:           2,
		MaxManagerAttempts:         2,
	})

	shouldEsc, nextTier, err := esc.ShouldEscalate("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldEsc {
		t.Error("expected escalation needed")
	}
	if nextTier != 1 {
		t.Errorf("expected next tier 1, got %d", nextTier)
	}
}

func TestShouldEscalate_No(t *testing.T) {
	fs := testEscalationStore(t)
	if err := fs.Append(state.NewEvent(state.EventStoryReviewFailed, "agent", "s-001", nil)); err != nil {
		t.Fatal(err)
	}

	esc := NewEscalationMachine(fs, config.RoutingConfig{
		MaxRetriesBeforeEscalation: 2,
		MaxSeniorRetries:           2,
		MaxManagerAttempts:         2,
	})

	shouldEsc, _, err := esc.ShouldEscalate("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if shouldEsc {
		t.Error("should not escalate with only 1 failure")
	}
}

func TestShouldEscalate_Tier4Pause(t *testing.T) {
	fs := testEscalationStore(t)

	// Escalate through tiers 0 -> 1 -> 2 -> 3 -> ready for tier 4.
	for fromTier := 0; fromTier < 4; fromTier++ {
		if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-001", map[string]any{
			"from_tier": fromTier, "to_tier": fromTier + 1,
		})); err != nil {
			t.Fatal(err)
		}
	}

	// Wait so failure timestamp is after the last escalation.
	time.Sleep(5 * time.Millisecond)

	// Tier 4 has 0 max retries, so any state at tier 4 means pause.
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	shouldEsc, nextTier, err := esc.ShouldEscalate("s-001")
	if err != nil {
		t.Fatal(err)
	}
	if !shouldEsc {
		t.Error("expected escalation needed at tier 4")
	}
	if nextTier != 5 {
		t.Errorf("expected next tier 5, got %d", nextTier)
	}
}

func TestMaxRetriesForTier(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, config.RoutingConfig{
		MaxRetriesBeforeEscalation: 2,
		MaxSeniorRetries:           3,
		MaxManagerAttempts:         1,
	})

	cases := []struct {
		tier     int
		expected int
	}{
		{0, 2},
		{1, 3},
		{2, 1},
		{3, 1},
		{4, 0},
		{99, 0},
	}
	for _, tc := range cases {
		got := esc.MaxRetriesForTier(tc.tier)
		if got != tc.expected {
			t.Errorf("tier %d: expected %d, got %d", tc.tier, tc.expected, got)
		}
	}
}

func TestValidateSplit_OverlappingFiles(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	children := []SplitChild{
		{Suffix: "a", OwnedFiles: []string{"src/main.go"}, Complexity: 2},
		{Suffix: "b", OwnedFiles: []string{"src/main.go"}, Complexity: 2},
	}
	if err := esc.ValidateSplit(0, children, 5); err == nil {
		t.Error("expected error for overlapping files")
	}
}

func TestValidateSplit_MaxDepth(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	children := []SplitChild{{OwnedFiles: []string{"a.go"}, Complexity: 2}}
	if err := esc.ValidateSplit(2, children, 5); err == nil {
		t.Error("expected error for max split depth")
	}
}

func TestValidateSplit_ExceedsComplexity(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	children := []SplitChild{{OwnedFiles: []string{"a.go"}, Complexity: 10}}
	if err := esc.ValidateSplit(0, children, 5); err == nil {
		t.Error("expected error for exceeding max complexity")
	}
}

func TestValidateSplit_Valid(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	children := []SplitChild{
		{Suffix: "a", OwnedFiles: []string{"src/a.go"}, Complexity: 3},
		{Suffix: "b", OwnedFiles: []string{"src/b.go"}, Complexity: 2},
	}
	if err := esc.ValidateSplit(0, children, 5); err != nil {
		t.Errorf("expected valid split, got error: %v", err)
	}
}

func TestApplySplit(t *testing.T) {
	fs := testEscalationStore(t)
	esc := NewEscalationMachine(fs, defaultRoutingConfig())

	dag := graph.New()
	dag.AddNode("parent-001")
	dag.AddNode("dep-001")
	dag.AddEdge("parent-001", "dep-001")

	rc := &RunContext{
		PlannedStories: []PlannedStory{
			{ID: "parent-001", Title: "Parent"},
		},
	}

	children := []SplitChild{
		{
			ID: "child-001", Title: "Child A",
			Description: "First part", AcceptanceCriteria: "AC-A",
			Complexity: 2, OwnedFiles: []string{"a.go"},
		},
		{
			ID: "child-002", Title: "Child B",
			Description: "Second part", AcceptanceCriteria: "AC-B",
			Complexity: 3, OwnedFiles: []string{"b.go"},
		},
	}
	depEdges := [][]string{{"child-002", "child-001"}}
	parentDeps := []string{"dep-001"}
	dependents := []string{}

	esc.ApplySplit(dag, rc, "parent-001", children, depEdges, parentDeps, dependents)

	// Verify children were added to DAG.
	ready := dag.ReadyNodes(map[string]bool{"dep-001": true})
	if len(ready) == 0 {
		t.Error("expected at least one ready child node after split")
	}

	// Verify children were added to PlannedStories.
	if len(rc.PlannedStories) != 3 {
		t.Errorf("expected 3 planned stories, got %d", len(rc.PlannedStories))
	}

	// Verify the child IDs exist in planned stories.
	found := map[string]bool{}
	for _, ps := range rc.PlannedStories {
		found[ps.ID] = true
	}
	if !found["child-001"] || !found["child-002"] {
		t.Error("expected both child IDs in planned stories")
	}
}
