package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/routing"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestEnsureGitignorePatterns_CreatesNew(t *testing.T) {
	dir := t.TempDir()

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	for _, pat := range []string{"CLAUDE.md", ".nxd-prompts/", ".serena/", ".nxd-db/"} {
		if !strings.Contains(string(content), pat) {
			t.Errorf("expected .gitignore to contain %q", pat)
		}
	}
}

func TestEnsureGitignorePatterns_ExistingPatterns(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// Pre-create .gitignore with the FULL extended pattern set (Phase 1.3).
	existing := "CLAUDE.md\nWAVE_CONTEXT.md\nREQUIREMENT.md\nnxd.yaml\n.nxd-prompts/\n.nxd-fix-gaps.md\n.serena/\n.nxd-db/\n"
	os.WriteFile(giPath, []byte(existing), 0o644)

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	// Should not add duplicates
	if string(content) != existing {
		t.Errorf("expected no changes, but .gitignore was modified to:\n%s", string(content))
	}
}

func TestEnsureGitignorePatterns_PartialExisting(t *testing.T) {
	dir := t.TempDir()
	giPath := filepath.Join(dir, ".gitignore")

	// Only has CLAUDE.md
	os.WriteFile(giPath, []byte("CLAUDE.md\nnode_modules/\n"), 0o644)

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	s := string(content)
	// Should keep existing content and add missing patterns
	if !strings.Contains(s, "node_modules/") {
		t.Error("expected existing node_modules/ to be preserved")
	}
	if !strings.Contains(s, ".nxd-prompts/") {
		t.Error("expected .nxd-prompts/ to be added")
	}
	if !strings.Contains(s, ".serena/") {
		t.Error("expected .serena/ to be added")
	}

	// Count occurrences of CLAUDE.md -- should only appear once
	count := strings.Count(s, "CLAUDE.md")
	if count != 1 {
		t.Errorf("expected CLAUDE.md to appear once, appeared %d times", count)
	}
}

func TestEnsureGitignorePatterns_NXDArtifactHeader(t *testing.T) {
	dir := t.TempDir()

	ensureGitignorePatterns(dir)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	if !strings.Contains(string(content), "# NXD agent artifacts") {
		t.Error("expected NXD artifact header comment in .gitignore")
	}
}

// TestFindDependents_FindsDirectDependents covers the helper used by
// the tech-lead split path to know which stories need their dep edges
// rewritten when a parent is replaced.
func TestFindDependents_FindsDirectDependents(t *testing.T) {
	stories := []PlannedStory{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A", "B"}},
		{ID: "D", DependsOn: []string{"E"}},
	}
	got := FindDependents(stories, "A")
	if len(got) != 2 {
		t.Fatalf("expected 2 dependents (B, C), got %d: %v", len(got), got)
	}
	want := map[string]bool{"B": true, "C": true}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected dependent %s", id)
		}
	}
}

// TestFindDependents_NoMatches returns nil for unrelated stories so
// callers can range over the result without a length check.
func TestFindDependents_NoMatches(t *testing.T) {
	stories := []PlannedStory{{ID: "A"}, {ID: "B", DependsOn: []string{"C"}}}
	got := FindDependents(stories, "Z")
	if got != nil {
		t.Errorf("expected nil for no matches, got %v", got)
	}
}

// TestEscalateToTier_EmitsEvent covers the event-emission contract:
// the dashboard's escalation panel relies on every tier transition
// producing a STORY_ESCALATED with from_tier + to_tier + reason.
func TestEscalateToTier_EmitsEvent(t *testing.T) {
	m := minimalMonitor(t)
	m.escalateToTier("STORY-ESC", 3, "tech lead takeover")

	evts, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryEscalated})
	if len(evts) != 1 {
		t.Fatalf("expected 1 STORY_ESCALATED, got %d", len(evts))
	}
	payload := state.DecodePayload(evts[0].Payload)
	if payload["to_tier"] != float64(3) {
		t.Errorf("to_tier = %v, want 3", payload["to_tier"])
	}
	if payload["reason"] != "tech lead takeover" {
		t.Errorf("reason missing: %v", payload)
	}
}

// TestRecordBayesianSuccess_NilRouterNoOps confirms the recorder is
// safe to call when routing is unwired (e.g. in dry-run or early
// init). Without the guard, monitor would NPE every successful merge.
func TestRecordBayesianSuccess_NilRouterNoOps(t *testing.T) {
	m := minimalMonitor(t)
	m.bayesian = nil
	m.recordBayesianSuccess("STORY-NIL", agent.RoleJunior)
}

// TestRecordBayesianSuccess_FullVsPartial covers the partial/full
// distinction: a story with prior STORY_REVIEW_FAILED events counts
// as partial (the retry recovered); without prior failures it's full.
// The Bayesian router uses this to update Beta priors correctly.
func TestRecordBayesianSuccess_FullVsPartial(t *testing.T) {
	m := minimalMonitor(t)
	m.bayesian = routing.NewBayesianRouter()
	m.bayesian.InitDefaults()

	if err := m.projStore.Project(state.NewEvent(state.EventStoryCreated, "test", "STORY-FULL", map[string]any{
		"id": "STORY-FULL", "req_id": "R", "title": "t", "complexity": 3,
	})); err != nil {
		t.Fatalf("seed full: %v", err)
	}
	m.recordBayesianSuccess("STORY-FULL", agent.RoleJunior)

	if err := m.projStore.Project(state.NewEvent(state.EventStoryCreated, "test", "STORY-PART", map[string]any{
		"id": "STORY-PART", "req_id": "R", "title": "t", "complexity": 3,
	})); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	if err := m.eventStore.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "STORY-PART", map[string]any{
		"reason": "review rejected",
	})); err != nil {
		t.Fatalf("seed fail: %v", err)
	}
	m.recordBayesianSuccess("STORY-PART", agent.RoleJunior)
}

// TestRecordBayesianEscalation_RecordsFailure exercises the failure
// outcome path — an escalation marks the role-at-current-tier with a
// failure so future Bayesian routing avoids it for similar work.
func TestRecordBayesianEscalation_RecordsFailure(t *testing.T) {
	m := minimalMonitor(t)
	m.bayesian = routing.NewBayesianRouter()
	m.bayesian.InitDefaults()

	if err := m.projStore.Project(state.NewEvent(state.EventStoryCreated, "test", "STORY-ESC", map[string]any{
		"id": "STORY-ESC", "req_id": "R", "title": "t", "complexity": 5,
	})); err != nil {
		t.Fatalf("seed: %v", err)
	}
	m.recordBayesianEscalation("STORY-ESC", 0)
}

// TestRecordBayesianEscalation_NilRouterNoOps mirrors the success
// guard — must be safe under unwired router.
func TestRecordBayesianEscalation_NilRouterNoOps(t *testing.T) {
	m := minimalMonitor(t)
	m.bayesian = nil
	m.recordBayesianEscalation("STORY-NIL", 1)
}
