package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// failingEvents is an EventStore whose Append always fails.
type failingEvents struct{ state.EventStore }

func (failingEvents) Append(state.Event) error { return errors.New("disk full") }

func TestEmit_AppendErrorIsReported(t *testing.T) {
	env := setupTestEnv(t)
	s := stores{Events: failingEvents{env.Events}, Proj: env.Proj}
	err := emit(s, state.NewEvent(state.EventStoryProgress, "a", "s", nil))
	if err == nil || !strings.Contains(err.Error(), "append STORY_PROGRESS: disk full") {
		t.Fatalf("got %v", err)
	}
}

func TestEmit_ProjectErrorIsReported(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "t", "/r")
	// Duplicate REQ_SUBMITTED violates the primary key → Project fails.
	dup := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1", "title": "t", "description": "d"})
	err := emit(stores{Events: env.Events, Proj: env.Proj}, dup)
	if err == nil || !strings.Contains(err.Error(), "project REQ_SUBMITTED") {
		t.Fatalf("got %v", err)
	}
}

func TestCancelStory_EmitFailureAborts(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "t", "/r")
	seedTestStory(t, env, "s1", "r1", "s", 1)
	setStoryStatus(t, env, "s1", "assigned")
	ft := &fakeTmux{alive: map[string]bool{"nxd-s1": true}}
	useFakeTmux(t, ft)

	var out bytes.Buffer
	story, _ := env.Proj.GetStory("s1")
	err := cancelStory(&out, stores{Events: failingEvents{env.Events}, Proj: env.Proj}, story, "")
	if err == nil || !strings.Contains(err.Error(), "append STORY_RESET") {
		t.Fatalf("got %v", err)
	}
	if len(ft.killed) != 0 {
		t.Error("session must not be killed when the events could not be recorded")
	}
}

func TestCancelRequirement_StoryFailurePropagates(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "t", "/r")
	seedTestStory(t, env, "s1", "r1", "s", 1)
	setStoryStatus(t, env, "s1", "assigned")
	useFakeTmux(t, &fakeTmux{})

	req, _ := env.Proj.GetRequirement("r1")
	var out bytes.Buffer
	err := cancelRequirement(&out, stores{Events: failingEvents{env.Events}, Proj: env.Proj}, req, "")
	if err == nil || !strings.Contains(err.Error(), "STORY_RESET") {
		t.Fatalf("got %v", err)
	}
	got, _ := env.Proj.GetRequirement("r1")
	if got.Status == "paused" {
		t.Error("requirement must not be paused when a story cancel failed")
	}
}

func TestCancelRequirement_ListStoriesError(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "t", "/r")
	req, _ := env.Proj.GetRequirement("r1")
	env.Proj.Close()
	var out bytes.Buffer
	err := cancelRequirement(&out, stores{Events: env.Events, Proj: env.Proj}, req, "")
	if err == nil || !strings.Contains(err.Error(), "list stories") {
		t.Fatalf("got %v", err)
	}
}

func TestRealTmux_KillMissingSessionErrors(t *testing.T) {
	// Either tmux is absent or the session does not exist: both are errors.
	if err := (realTmux{}).KillSession("nxd-definitely-not-a-session-xyz"); err == nil {
		t.Error("killing a non-existent session should error")
	}
}

func TestLoadStoresLocked_OpenFailureReleasesLock(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nxd")
	os.MkdirAll(stateDir, 0o755)
	// A directory where nxd.db should be makes the SQLite open fail.
	os.MkdirAll(filepath.Join(stateDir, "nxd.db"), 0o755)
	cfg := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(cfg, []byte("version: \"1.0\"\nworkspace:\n  state_dir: "+stateDir+"\n"), 0o644)

	if _, _, err := loadStoresLocked(cfg); err == nil {
		t.Fatal("expected open error")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "nxd.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Error("lock must be released when opening the stores fails")
	}
}

func TestLoadStoresLocked_CorruptLogFails(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 1)
	appendRawLine(t, eventsPath(env), "{bad\n")
	if _, _, err := loadStoresLocked(env.Config); err == nil || !strings.Contains(err.Error(), "rebuild projection") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadStores_CorruptLogFails(t *testing.T) {
	env := setupTestEnv(t)
	seedBehindProjection(t, env, 1)
	appendRawLine(t, eventsPath(env), "{bad\n")
	if _, err := loadStores(env.Config); err == nil || !strings.Contains(err.Error(), "rebuild projection") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadStoresLocked_StateDirParentIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, []byte("x"), 0o644)
	cfg := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(cfg, []byte("version: \"1.0\"\nworkspace:\n  state_dir: "+filepath.Join(blocker, "sub")+"\n"), 0o644)
	if _, _, err := loadStoresLocked(cfg); err == nil || !strings.Contains(err.Error(), "create state directory") {
		t.Fatalf("got %v", err)
	}
}

func TestRunInit_WriteConfigFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := chdirTemp(t)
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := runInitCmd(t); err == nil || !strings.Contains(err.Error(), "write nxd.yaml") {
		t.Fatalf("got %v", err)
	}
}

func TestRunInit_StateDirUnwritable(t *testing.T) {
	dir := chdirTemp(t)
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0o644)
	t.Cleanup(func() { stateDirOverride = "" })
	stateDirOverride = filepath.Join(blocker, "state")
	if _, err := runInitCmd(t); err == nil || !strings.Contains(err.Error(), "create directory") {
		t.Fatalf("got %v", err)
	}
}

func TestRunInit_StoreInitFailures(t *testing.T) {
	t.Run("events.jsonl is a directory", func(t *testing.T) {
		dir := chdirTemp(t)
		os.MkdirAll(filepath.Join(dir, ".nxd", "events.jsonl"), 0o755)
		if _, err := runInitCmd(t, "--local-state"); err == nil || !strings.Contains(err.Error(), "initialize event store") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nxd.db is a directory", func(t *testing.T) {
		dir := chdirTemp(t)
		os.MkdirAll(filepath.Join(dir, ".nxd", "nxd.db"), 0o755)
		if _, err := runInitCmd(t, "--local-state"); err == nil || !strings.Contains(err.Error(), "initialize projection store") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("gitignore unwritable", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores permissions")
		}
		dir := chdirTemp(t)
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("x\n"), 0o400)
		if _, err := runInitCmd(t, "--local-state"); err == nil || !strings.Contains(err.Error(), ".gitignore") {
			t.Fatalf("got %v", err)
		}
	})
}
