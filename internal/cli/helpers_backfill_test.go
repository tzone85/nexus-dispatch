package cli

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestLoadStores_BackfillsStoryBranch: the wiring point in loadStores. A
// STORY_STARTED that reached the log but whose branch never reached the
// projection (watermark acked so no rebuild fires) is backfilled on load.
func TestLoadStores_BackfillsStoryBranch(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Req", env.Dir)
	created := state.NewEvent(state.EventStoryCreated, "system", "s-1", map[string]any{
		"id": "s-1", "req_id": "r-1", "title": "S", "description": "d", "complexity": 1,
	})
	if err := env.Events.Append(created); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(created); err != nil {
		t.Fatal(err)
	}
	// Log only — simulate a projection that pre-dates the branch handler.
	started := state.NewEvent(state.EventStoryStarted, "a", "s-1", map[string]any{"branch": "nxd/s-1"})
	if err := env.Events.Append(started); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.AckDirectWrite(1); err != nil {
		t.Fatal(err)
	}
	if pre, err := env.Proj.GetStory("s-1"); err != nil || pre.Branch != "" {
		t.Fatalf("precondition: want empty branch, got %q (err=%v)", pre.Branch, err)
	}

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores: %v", err)
	}
	defer s.Close()
	got, err := s.Proj.GetStory("s-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "nxd/s-1" {
		t.Errorf("loadStores must backfill the branch, got %q", got.Branch)
	}
}

// TestLoadStores_BackfillRunsOnce: after the first load the projection_meta
// flag is set, and a second load does NOT rescan the log. The witness is a
// STORY_STARTED that reaches the log only (watermark acked) after the first
// load: its projected branch is still empty after the second load — and
// state.StoryBranch still resolves it to the canonical name, so the
// once-only flag costs no data.
func TestLoadStores_BackfillRunsOnce(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-1", "Req", env.Dir)
	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores: %v", err)
	}
	needs, err := s.Proj.NeedsStoryBranchBackfill()
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Fatal("backfill flag must be set after the first loadStores")
	}

	created := state.NewEvent(state.EventStoryCreated, "system", "s-2", map[string]any{
		"id": "s-2", "req_id": "r-1", "title": "S", "description": "d", "complexity": 1,
	})
	if err := env.Events.Append(created); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(created); err != nil {
		t.Fatal(err)
	}
	started := state.NewEvent(state.EventStoryStarted, "a", "s-2", map[string]any{"branch": "nxd/s-2"})
	if err := env.Events.Append(started); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.AckDirectWrite(1); err != nil {
		t.Fatal(err)
	}

	s2, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("second loadStores: %v", err)
	}
	defer s2.Close()
	got, err := s2.Proj.GetStory("s-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "" {
		t.Fatalf("the backfill ran again on the second load (branch %q); the flag must stop it", got.Branch)
	}
	if state.StoryBranch(got) != "nxd/s-2" {
		t.Fatalf("a row the backfill did not reach must still resolve to its canonical branch, got %q", state.StoryBranch(got))
	}
}
