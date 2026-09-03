package state_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Every accessor must surface an error (never panic, never return stale
// success) once the underlying database is closed. This also pins the
// error-propagation branches that a healthy database never exercises.
func TestSQLiteStore_ClosedStoreReturnsErrors(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"), state.WithFsync(false))
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(filepath.Join(dir, "nxd.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = ps.Close()

	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1", "title": "t", "description": "d"})
	checks := map[string]func() error{
		"Project":            func() error { return ps.Project(evt) },
		"RebuildFrom":        func() error { return ps.RebuildFrom(context.Background(), es) },
		"AckDirectWrite":     func() error { return ps.AckDirectWrite(1) },
		"AppliedEventCount":  func() error { _, err := ps.AppliedEventCount(); return err },
		"GetRequirement":     func() error { _, err := ps.GetRequirement("r1"); return err },
		"GetStory":           func() error { _, err := ps.GetStory("s1"); return err },
		"ListStories":        func() error { _, err := ps.ListStories(state.StoryFilter{}); return err },
		"ListRequirements":   func() error { _, err := ps.ListRequirements(); return err },
		"ListAgents":         func() error { _, err := ps.ListAgents(state.AgentFilter{Status: "idle"}); return err },
		"ListEscalations":    func() error { _, err := ps.ListEscalations(); return err },
		"ListStoryDeps":      func() error { _, err := ps.ListStoryDeps("r1"); return err },
		"ListStoryDatabases": func() error { _, err := ps.ListStoryDatabases(state.StoryDBFilter{}); return err },
		"InsertAgent":        func() error { return ps.InsertAgent("a", "t", "m", "r", "s") },
		"ArchiveRequirement": func() error { return ps.ArchiveRequirement("r1") },
		"ArchiveStories":     func() error { return ps.ArchiveStoriesByReq("r1") },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s on a closed store should return an error", name)
		}
	}
}

func TestNewSQLiteStore_BadPath(t *testing.T) {
	if _, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "no", "such", "dir", "nxd.db")); err == nil {
		t.Fatal("expected error for unwritable database path")
	}
}

func TestNewFileStore_BadPath(t *testing.T) {
	if _, err := state.NewFileStore(filepath.Join(t.TempDir(), "no", "such", "dir", "events.jsonl")); err == nil {
		t.Fatal("expected error for unwritable path")
	}
}

// Informational and classification events project without error and the
// classification lands on the requirement row.
func TestSQLiteStore_ProjectsClassificationAndInformationalEvents(t *testing.T) {
	_, ps := openTxTestStores(t)
	sub := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1", "title": "t", "description": "d"})
	if err := ps.Project(sub); err != nil {
		t.Fatal(err)
	}
	events := []state.Event{
		state.NewEvent(state.EventReqClassified, "", "", map[string]any{"req_id": "r1", "req_type": "feature", "is_existing": true}),
		state.NewEvent(state.EventInvestigationCompleted, "", "", map[string]any{"req_id": "r1", "report": "{}"}),
		state.NewEvent(state.EventReqPendingReview, "", "", map[string]any{"id": "r1"}),
		state.NewEvent(state.EventStorySecurityPassed, "", "s1", nil),
		state.NewEvent(state.EventSecurityScanCompleted, "", "", nil),
		state.NewEvent(state.EventStoryConflictBinary, "", "s1", nil),
		state.NewEvent(state.EventStoryIntegrationFailed, "", "s1", nil),
		state.NewEvent(state.EventReqPlanningStarted, "", "", nil),
		state.NewEvent(state.EventStoryRecovery, "", "s1", map[string]any{}),
		{ID: "bad", Type: state.EventStoryProgress, Payload: []byte("{not json")},
	}
	for _, e := range events {
		if err := ps.Project(e); err != nil {
			t.Errorf("Project(%s): %v", e.Type, err)
		}
	}
	req, err := ps.GetRequirement("r1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != "pending_review" {
		t.Errorf("status = %s, want pending_review", req.Status)
	}
	n, _ := ps.AppliedEventCount()
	if n != len(events)+1 {
		t.Errorf("watermark = %d, want %d", n, len(events)+1)
	}
}
