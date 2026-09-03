package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// seedBehindProjection appends events to the log WITHOUT projecting them so
// the watermark is behind, then closes the env's stores so loadStores can
// reopen them.
func seedBehindProjection(t *testing.T, env *testEnv, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
			"id": "behind-" + string(rune('a'+i)), "title": "t", "description": "d", "repo_path": "/r",
		})
		if err := env.Events.Append(evt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadStores_RebuildsWhenLockFree(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 3)

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores: %v", err)
	}
	defer s.Close()
	applied, _ := s.Proj.AppliedEventCount()
	if applied != 3 {
		t.Errorf("watermark = %d, want 3 (rebuild should have run)", applied)
	}
	reqs, _ := s.Proj.ListRequirements()
	if len(reqs) != 3 {
		t.Errorf("rows = %d, want 3", len(reqs))
	}
}

// Defect 4(c): a live pipeline holds the lock → loadStores must NOT rebuild
// underneath it; it proceeds read-only against the stale projection.
func TestLoadStores_SkipsRebuildWhenPipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 2)
	stateDir := filepath.Join(env.Dir, ".nxd")

	holder, err := engine.AcquireLock(stateDir) // fake running pipeline (our own live pid)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores must degrade, not fail: %v", err)
	}
	defer s.Close()
	applied, _ := s.Proj.AppliedEventCount()
	if applied != 0 {
		t.Errorf("watermark = %d; rebuild must be skipped while the lock is held", applied)
	}
	// The holder's lock is untouched.
	if _, err := os.Stat(filepath.Join(stateDir, "nxd.lock")); err != nil {
		t.Errorf("lock file should still exist: %v", err)
	}
}

func TestRebuildProjectionIfBehind_OtherLockErrorPropagates(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 1)
	acquire := func(string) (func(), error) { return nil, errors.New("disk on fire") }
	err := rebuildProjectionIfBehind(env.Events, env.Proj, env.Dir, acquire)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("expected lock error to propagate, got %v", err)
	}
}

func TestRebuildProjectionIfBehind_NoopWhenInSync(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "t", "/r")
	called := false
	acquire := func(string) (func(), error) { called = true; return func() {}, nil }
	if err := rebuildProjectionIfBehind(env.Events, env.Proj, env.Dir, acquire); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("lock must not be taken when the projection is already in sync")
	}
}

func TestLoadStoresLocked_HoldsLockAndRebuilds(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 2)
	stateDir := filepath.Join(env.Dir, ".nxd")

	s, lock, err := loadStoresLocked(env.Config)
	if err != nil {
		t.Fatalf("loadStoresLocked: %v", err)
	}
	defer s.Close()
	applied, _ := s.Proj.AppliedEventCount()
	if applied != 2 {
		t.Errorf("watermark = %d, want 2", applied)
	}
	if _, err := engine.TryAcquireLock(stateDir); !errors.Is(err, engine.ErrLockHeld) {
		t.Errorf("lock should be held by loadStoresLocked, got %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if l, err := engine.TryAcquireLock(stateDir); err != nil {
		t.Errorf("lock should be free after Release: %v", err)
	} else {
		l.Release()
	}
}

func TestLoadStoresLocked_FailsWhenPipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, _, err := loadStoresLocked(env.Config); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected pipeline-running error, got %v", err)
	}
}

func TestLoadStoresLocked_BadConfig(t *testing.T) {
	if _, _, err := loadStoresLocked(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected config error")
	}
}

func TestApplyStateDirOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = "/from/config"
	if got := applyStateDirOverride(cfg).Workspace.StateDir; got != "/from/config" {
		t.Errorf("no override: %q", got)
	}
	t.Cleanup(func() { stateDirOverride = "" })
	stateDirOverride = "/flag/dir"
	if got := applyStateDirOverride(cfg).Workspace.StateDir; got != "/flag/dir" {
		t.Errorf("override: %q", got)
	}
	stateDirOverride = "rel"
	wd, _ := os.Getwd()
	if got := applyStateDirOverride(cfg).Workspace.StateDir; got != filepath.Join(wd, "rel") {
		t.Errorf("relative override should resolve against cwd: %q", got)
	}
}

// --state-dir wins over the config file's workspace.state_dir.
func TestLoadStores_StateDirFlagOverridesConfig(t *testing.T) {
	env := setupTestEnv(t)
	alt := filepath.Join(env.Dir, "alt-state")
	t.Cleanup(func() { stateDirOverride = "" })
	stateDirOverride = alt

	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Config.Workspace.StateDir != alt {
		t.Errorf("StateDir = %q, want %q", s.Config.Workspace.StateDir, alt)
	}
	if _, err := os.Stat(filepath.Join(alt, "events.jsonl")); err != nil {
		t.Errorf("event log should be created under the override dir: %v", err)
	}
}

// The event store opened by loadStores honours the durability keys.
func TestLoadStores_AppliesMaxEventBytes(t *testing.T) {
	env := setupTestEnv(t)
	cfg := "version: \"1.0\"\nworkspace:\n  state_dir: " + filepath.Join(env.Dir, ".nxd") +
		"\n  backend: sqlite\n  max_event_bytes: 2048\n  fsync_events: false\n"
	if err := os.WriteFile(env.Config, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	big := strings.Repeat("z", 10_000)
	if err := s.Events.Append(state.NewEvent(state.EventStoryQAFailed, "qa", "s1", map[string]any{"output": big})); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events.List(state.EventFilter{Type: state.EventStoryQAFailed})
	if err != nil || len(events) != 1 {
		t.Fatalf("List = %d, %v", len(events), err)
	}
	if state.DecodePayload(events[0].Payload)["truncated"] != true {
		t.Error("payload above max_event_bytes should be truncated")
	}
}
