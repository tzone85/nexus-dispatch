package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// minimalMonitor builds a Monitor with just the stores needed for the
// manager-action helper tests below. The full Monitor wiring is heavy
// (Dispatcher, Reviewer, QA, Merger, …) — most action helpers only
// touch eventStore + projStore, so a stub is enough.
func minimalMonitor(t *testing.T) *Monitor {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	t.Cleanup(func() { es.Close() })

	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	cfg := config.DefaultConfig()
	return &Monitor{
		eventStore: es,
		projStore:  ps,
		config:     cfg,
		escalation: NewEscalationMachine(es, cfg.Routing),
	}
}

// TestExecuteRetryAction_EmitsEscalatedAndReset covers the side-effect
// contract of executeRetryAction: two events land — STORY_ESCALATED
// (back down to the configured tier) and STORY_REVIEW_FAILED (so the
// next dispatcher wave picks the story back up). Without these events
// the manager's "retry" verdict would be a silent no-op.
func TestExecuteRetryAction_EmitsEscalatedAndReset(t *testing.T) {
	m := minimalMonitor(t)
	m.executeRetryAction("STORY-RETRY", ManagerAction{
		Diagnosis:   "transient build failure",
		Action:      "retry",
		RetryConfig: &RetryConfig{ResetTier: 0, WorktreeReset: false},
	}, "/tmp/nonexistent-worktree")

	esc, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryEscalated})
	if len(esc) != 1 {
		t.Fatalf("expected 1 STORY_ESCALATED, got %d", len(esc))
	}
	payload := state.DecodePayload(esc[0].Payload)
	if payload["to_tier"] != float64(0) {
		t.Errorf("to_tier = %v, want 0", payload["to_tier"])
	}
	if payload["reason"] == "" {
		t.Error("reason should include the diagnosis")
	}

	failed, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(failed) != 1 {
		t.Fatalf("expected 1 STORY_REVIEW_FAILED, got %d", len(failed))
	}
}

// TestExecuteRetryAction_WorktreeResetCleansDir covers the optional
// WorktreeReset branch — when set, the worktree directory is wiped so
// the next agent gets a clean state. We use a real tempdir to verify
// removal happened.
func TestExecuteRetryAction_WorktreeResetCleansDir(t *testing.T) {
	m := minimalMonitor(t)
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed worktree file: %v", err)
	}

	m.executeRetryAction("STORY-WIPE", ManagerAction{
		Diagnosis:   "stale state",
		Action:      "retry",
		RetryConfig: &RetryConfig{ResetTier: 1, WorktreeReset: true},
	}, wt)

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed when WorktreeReset=true; stat err=%v", err)
	}
}

// TestExecuteRewriteAction_NoConfigResetsToDraft covers the guard:
// the manager said "rewrite" but didn't supply a RewriteConfig. The
// helper must reset the story to draft (with a clear reason) rather
// than silently doing nothing.
func TestExecuteRewriteAction_NoConfigResetsToDraft(t *testing.T) {
	m := minimalMonitor(t)
	m.executeRewriteAction("STORY-NO-CFG", ManagerAction{
		Diagnosis: "incomplete rewrite payload",
		Action:    "rewrite",
		// RewriteConfig deliberately nil
	})
	// resetStoryToDraft emits STORY_REVIEW_FAILED (with the reset
	// reason in the payload) — that's the observable signal that the
	// guard fired and re-queued the story rather than silently
	// emitting an empty rewrite.
	failed, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(failed) == 0 {
		t.Fatal("expected STORY_REVIEW_FAILED reset when RewriteConfig is nil")
	}
	// The rewritten event must NOT have been emitted.
	rew, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryRewritten})
	if len(rew) != 0 {
		t.Errorf("expected 0 STORY_REWRITTEN on nil config, got %d", len(rew))
	}
}

// TestExecuteRewriteAction_PartialConfigEmitsRewritten exercises the
// happy path with a partial RewriteConfig (only Title set). The
// emitted STORY_REWRITTEN event must contain the partial change set,
// not the empty fields, so the projection can apply a minimal patch.
func TestExecuteRewriteAction_PartialConfigEmitsRewritten(t *testing.T) {
	m := minimalMonitor(t)
	m.executeRewriteAction("STORY-RW", ManagerAction{
		Diagnosis:     "needs clearer title",
		Action:        "rewrite",
		RewriteConfig: &RewriteConfig{Title: "Refined: do thing precisely", Complexity: 0},
	})

	rew, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryRewritten})
	if len(rew) != 1 {
		t.Fatalf("expected 1 STORY_REWRITTEN, got %d", len(rew))
	}
	payload := state.DecodePayload(rew[0].Payload)
	changes, ok := payload["changes"].(map[string]any)
	if !ok {
		t.Fatalf("changes payload missing or wrong shape: %v", payload)
	}
	if changes["title"] != "Refined: do thing precisely" {
		t.Errorf("title not in changes: %v", changes)
	}
	// Complexity=0 must NOT be present (skipped by `> 0` guard).
	if _, present := changes["complexity"]; present {
		t.Error("complexity=0 should be omitted from changes")
	}
}

// TestExecuteRewriteAction_FullConfigEmitsAllFields confirms every
// non-empty field flows through to the changes map.
func TestExecuteRewriteAction_FullConfigEmitsAllFields(t *testing.T) {
	m := minimalMonitor(t)
	m.executeRewriteAction("STORY-FULL", ManagerAction{
		Diagnosis: "rewrite everything",
		Action:    "rewrite",
		RewriteConfig: &RewriteConfig{
			Title:              "T",
			Description:        "D",
			AcceptanceCriteria: "AC",
			Complexity:         5,
		},
	})

	rew, _ := m.eventStore.List(state.EventFilter{Type: state.EventStoryRewritten})
	if len(rew) != 1 {
		t.Fatal("expected 1 STORY_REWRITTEN")
	}
	payload := state.DecodePayload(rew[0].Payload)
	changes := payload["changes"].(map[string]any)
	for _, k := range []string{"title", "description", "acceptance_criteria", "complexity"} {
		if _, ok := changes[k]; !ok {
			t.Errorf("changes missing %q in: %v", k, changes)
		}
	}
}
