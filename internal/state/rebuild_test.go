package state_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestRebuildFrom_RecoversDesyncedProjection models the exact failure that
// engine.emitEventOrLog documents but that had no implementation: an event is
// durably appended to the log, but its Project call fails (or is skipped by a
// crash) so the projection is left stale. The documented contract — "replaying
// the event log will recover the projection on next startup" — requires a
// RebuildFrom that truncates and replays every event. Without it, a merged
// story keeps showing its pre-merge status forever, and resume re-dispatches
// work that is already merged.
func TestRebuildFrom_RecoversDesyncedProjection(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	eventsPath := filepath.Join(resolved, "events.jsonl")
	dbPath := filepath.Join(resolved, "nxd.db")

	es, err := state.NewFileStore(eventsPath)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer es.Close()

	const reqID, storyID = "r-900", "s-900"

	// Drive a story all the way to merged. Every event is appended to the log.
	// STORY_MERGED is deliberately NOT projected into store #1, modelling the
	// Append-ok / Project-failed desync from emitEventOrLog.
	events := []state.Event{
		state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
			"id": reqID, "title": "Recover me", "description": "desc",
		}),
		state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
			"id": storyID, "req_id": reqID, "title": "Mergeable story",
			"description": "desc", "complexity": 2,
		}),
		state.NewEvent(state.EventStoryStarted, "jr-1", storyID, nil),
		state.NewEvent(state.EventStoryCompleted, "jr-1", storyID, nil),
		state.NewEvent(state.EventStoryReviewPassed, "sr-1", storyID, nil),
		state.NewEvent(state.EventStoryQAPassed, "qa-1", storyID, nil),
		state.NewEvent(state.EventStoryPRCreated, "merger", storyID, map[string]any{"pr_number": 7}),
		state.NewEvent(state.EventStoryMerged, "system", storyID, nil),
	}

	ps1, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new sqlite store #1: %v", err)
	}
	for _, evt := range events {
		if err := es.Append(evt); err != nil {
			t.Fatalf("append %s: %v", evt.Type, err)
		}
		// Simulate the projection failure on the merge: append succeeds but the
		// projection never applies STORY_MERGED.
		if evt.Type == state.EventStoryMerged {
			continue
		}
		if err := ps1.Project(evt); err != nil {
			t.Fatalf("project %s: %v", evt.Type, err)
		}
	}
	ps1.Close()

	// Restart: reopen the SAME (desynced) projection db. A naive reopen does
	// NOT recover — the story still shows its pre-merge status.
	ps2, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer ps2.Close()

	stale, err := ps2.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story after reopen: %v", err)
	}
	if stale.Status == "merged" {
		t.Fatalf("precondition failed: expected a stale (desynced) status after reopen, got already-merged")
	}

	// Recover via replay of the event log.
	if err := ps2.RebuildFrom(context.Background(), es); err != nil {
		t.Fatalf("RebuildFrom: %v", err)
	}

	recovered, err := ps2.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story after rebuild: %v", err)
	}
	if recovered.Status != "merged" {
		t.Fatalf("RebuildFrom did not recover the projection: story status = %q, want %q",
			recovered.Status, "merged")
	}
}
