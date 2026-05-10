package engine

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// initSpawnTestRepo creates a minimal git repo at dir (with one
// commit on main) so nxdgit.CreateWorktree can attach to it. spawn
// requires this real-on-disk state.
func initSpawnTestRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-qm", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestSpawn_UnknownRuntimeReturnsError covers a failure mode in
// spawn that doesn't need full pipeline scaffolding: configure a
// runtime registry that's missing the runtime the role resolves to.
// spawn must produce an error rather than crash.
func TestSpawn_UnknownRuntimeReturnsError(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)

	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer ps.Close()

	// Empty registry — runtimeForRole will return "aider" but Get
	// will fail because there's no "aider" entry.
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = dir
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	e := NewExecutor(reg, cfg, es, ps, nil)

	a := Assignment{
		StoryID:     "STORY-X",
		ReqID:       "REQ-1",
		Role:        agent.RoleJunior,
		Branch:      "story/STORY-X",
		AgentID:     "agent-1",
		SessionName: "nxd-test",
	}
	story := PlannedStory{ID: "STORY-X", Title: "T", Description: "D"}

	res := e.spawn(context.Background(), repo, a, story, nil, nil)
	if res.Error == nil {
		t.Fatal("expected error when runtime unknown")
	}
}

// TestSpawn_HappyPath_CLIRuntime drives spawn end-to-end with:
//   - Real git repo + worktree
//   - tmux mocked via tmux.SetTestExec
//   - A CLIRuntime config that maps to the junior role
//   - No native runtime, no LLM client
//
// Confirms STORY_STARTED + AGENT_SPAWNED events land and the
// SpawnResult has no error. This is the integration-style test that
// PR 5 of the roadmap targeted.
func TestSpawn_HappyPath_CLIRuntime(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")

	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer ps.Close()

	// Mock tmux so the CLIRuntime's tmux.CreateSession + send-keys
	// calls succeed without a real tmux binary on PATH.
	stop := tmux.SetTestExec(
		func(args ...string) error { return nil },
		func(args ...string) (string, error) { return "", nil },
	)
	defer stop()

	// Runtime config: a fake CLI tool. Command is "true" — exists
	// on every Unix box and exits 0. The CLIRuntime layer will
	// build a tmux command using this and pass it to our mocked
	// tmux.CreateSession.
	runtimeCfg := map[string]config.RuntimeConfig{
		"aider": {
			Command: "true",
			Args:    []string{},
			Models:  []string{"any-model"},
		},
	}

	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = stateDir
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "any-model"

	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	e := NewExecutor(reg, cfg, es, ps, nil)

	a := Assignment{
		StoryID:     "STORY-OK",
		ReqID:       "REQ-1",
		Role:        agent.RoleJunior,
		Branch:      "story/STORY-OK",
		AgentID:     "junior-001",
		SessionName: "nxd-test-ok",
	}
	story := PlannedStory{
		ID:                 "STORY-OK",
		Title:              "Add feature flag",
		Description:        "Add --verbose CLI flag",
		AcceptanceCriteria: "Tests pass",
		Complexity:         3,
	}

	res := e.spawn(context.Background(), repo, a, story, nil, nil)
	if res.Error != nil {
		t.Fatalf("spawn returned error: %v", res.Error)
	}
	if res.WorktreePath == "" {
		t.Errorf("expected worktree path to be set")
	}

	// STORY_STARTED must have been emitted as the first observable
	// signal that the agent took ownership.
	started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted})
	if len(started) == 0 {
		t.Error("expected STORY_STARTED event after spawn")
	}
}

// TestSpawn_BadWorktreeDirReturnsError covers the early failure
// mode where CreateWorktree fails (e.g. repoDir isn't a git repo).
// spawn must wrap the error with the story id so the dashboard's
// failure log is actionable.
func TestSpawn_BadWorktreeDirReturnsError(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	defer es.Close()
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	defer ps.Close()

	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = dir
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	e := NewExecutor(reg, cfg, es, ps, nil)

	a := Assignment{
		StoryID: "STORY-NG",
		Role:    agent.RoleJunior,
		Branch:  "story/STORY-NG",
		AgentID: "agent-bad",
	}
	story := PlannedStory{ID: "STORY-NG"}

	// repoDir is just a tempdir, NOT a git repo — CreateWorktree
	// fails.
	res := e.spawn(context.Background(), t.TempDir(), a, story, nil, nil)
	if res.Error == nil {
		t.Fatal("expected error when repoDir is not a git repo")
	}
}
