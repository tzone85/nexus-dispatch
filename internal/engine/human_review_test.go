package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newHumanReviewMonitor(t *testing.T) (*Monitor, state.EventStore, *state.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(dir, "proj.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		es.Close()
		ps.Close()
	})
	cfg := config.DefaultConfig()
	m := NewMonitor(nil, nil, nil, nil, nil, cfg, es, ps)
	return m, es, ps
}

func mostRecentHumanReview(t *testing.T, es state.EventStore) map[string]any {
	t.Helper()
	events, err := es.List(state.EventFilter{Type: state.EventHumanReviewNeeded})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no HUMAN_REVIEW_NEEDED event emitted")
	}
	last := events[len(events)-1]
	var payload map[string]any
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestEmitHumanReviewNeeded_DetectsEmptyDiff(t *testing.T) {
	m, es, _ := newHumanReviewMonitor(t)
	story := state.Story{ID: "s-1", ReqID: "r-1"}
	m.emitHumanReviewNeeded(story, "agent produced no code changes")

	got := mostRecentHumanReview(t, es)
	if got["failure_pattern"] != "empty_diff" {
		t.Errorf("pattern = %v, want empty_diff", got["failure_pattern"])
	}
	suggestions, _ := got["suggested_directives"].([]any)
	if len(suggestions) == 0 {
		t.Error("expected non-empty suggestions")
	}
}

func TestEmitHumanReviewNeeded_DetectsMergeError(t *testing.T) {
	m, es, _ := newHumanReviewMonitor(t)
	story := state.Story{ID: "s-2", ReqID: "r-1"}
	m.emitHumanReviewNeeded(story, "merge/rebase error: cannot rebase")

	got := mostRecentHumanReview(t, es)
	if got["failure_pattern"] != "merge_error" {
		t.Errorf("pattern = %v, want merge_error", got["failure_pattern"])
	}
}

func TestEmitHumanReviewNeeded_DetectsReviewRejection(t *testing.T) {
	m, es, _ := newHumanReviewMonitor(t)
	story := state.Story{ID: "s-3", ReqID: "r-1"}
	// Seed multiple review failures.
	for i := 0; i < 3; i++ {
		ev := state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-3", nil)
		es.Append(ev)
	}
	m.emitHumanReviewNeeded(story, "review rejected: missing tests")

	got := mostRecentHumanReview(t, es)
	if got["failure_pattern"] != "review_rejection" {
		t.Errorf("pattern = %v, want review_rejection", got["failure_pattern"])
	}
}

func TestEmitHumanReviewNeeded_FallbackUnknown(t *testing.T) {
	m, es, _ := newHumanReviewMonitor(t)
	story := state.Story{ID: "s-4", ReqID: "r-1"}
	m.emitHumanReviewNeeded(story, "weird thing happened")

	got := mostRecentHumanReview(t, es)
	if got["failure_pattern"] != "unknown" {
		t.Errorf("pattern = %v, want unknown", got["failure_pattern"])
	}
	// Suggestions still include the canonical recovery commands.
	suggestions, _ := got["suggested_directives"].([]any)
	joined := ""
	for _, s := range suggestions {
		joined += s.(string) + "\n"
	}
	if !strings.Contains(joined, "nxd status") {
		t.Errorf("expected nxd status suggestion, got: %s", joined)
	}
	if !strings.Contains(joined, "nxd direct") {
		t.Errorf("expected nxd direct suggestion, got: %s", joined)
	}
}
