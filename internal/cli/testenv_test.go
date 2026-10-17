package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// testEnv holds the temp directory, stores, and config path for CLI tests.
type testEnv struct {
	Dir    string
	Events state.EventStore
	Proj   *state.SQLiteStore
	Config string // path to nxd.yaml
}

// setupTestEnv creates a temp directory with a minimal nxd.yaml and opens
// event + projection stores. The caller should defer cleanup.
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()

	stateDir := filepath.Join(dir, ".nxd")
	os.MkdirAll(stateDir, 0o755)

	// Write minimal nxd.yaml pointing to temp state dir.
	cfgContent := "version: \"1.0\"\nworkspace:\n  state_dir: " + stateDir + "\n  backend: sqlite\nmerge:\n  base_branch: main\n  mode: local\ncleanup:\n  branch_retention_days: 7\n"
	cfgPath := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(cfgPath, []byte(cfgContent), 0o644)

	es, err := state.NewFileStore(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(stateDir, "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		es.Close()
		ps.Close()
	})

	return &testEnv{Dir: dir, Events: es, Proj: ps, Config: cfgPath}
}

// seedTestReq creates a requirement and returns its ID.
func seedTestReq(t *testing.T, env *testEnv, id, title, repoPath string) {
	t.Helper()
	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": id, "title": title, "description": "Test requirement", "repo_path": repoPath,
	})
	env.Events.Append(evt)
	env.Proj.Project(evt)
}

// seedTestStory creates a story under the given requirement.
func seedTestStory(t *testing.T, env *testEnv, storyID, reqID, title string, complexity int) {
	t.Helper()
	evt := state.NewEvent(state.EventStoryCreated, "system", storyID, map[string]any{
		"id": storyID, "req_id": reqID, "title": title,
		"description": "Test story", "complexity": complexity,
	})
	env.Events.Append(evt)
	env.Proj.Project(evt)
}

// seedTestAgent inserts an agent directly into the projection store.
// AGENT_SPAWNED events are not projected by SQLiteStore.Project, so we
// use InsertAgent which wraps a direct SQL INSERT.
func seedTestAgent(t *testing.T, env *testEnv, agentID, agentType, session string) {
	t.Helper()
	env.Proj.InsertAgent(agentID, agentType, "gemma4:26b", "gemma", session)
}

// seedTestEscalation creates an escalation event for a story.
func seedTestEscalation(t *testing.T, env *testEnv, storyID, fromAgent, reason string) {
	t.Helper()
	evt := state.NewEvent(state.EventStoryEscalated, fromAgent, storyID, map[string]any{
		"from_tier": 0, "to_tier": 1, "reason": reason,
	})
	env.Events.Append(evt)
	env.Proj.Project(evt)
}

// execCmd creates a command, sets config flag and output buffer, runs it,
// and returns the output and error.
func execCmd(t *testing.T, cmd *cobra.Command, cfgPath string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Ensure the --config flag exists (normally inherited from root).
	if cmd.Flags().Lookup("config") == nil {
		cmd.Flags().String("config", "", "")
	}
	cmd.Flags().Set("config", cfgPath)

	if len(args) > 0 {
		cmd.SetArgs(args)
	}

	err := cmd.Execute()
	return buf.String(), err
}

// seedStartedStory projects a story with a branch (STORY_STARTED) that was
// merged at mergedAt, or is still in progress when mergedAt is zero.
func seedStartedStory(t *testing.T, env *testEnv, reqID, storyID, branch string, mergedAt time.Time) {
	t.Helper()
	created := state.NewEvent(state.EventStoryCreated, "system", storyID, map[string]any{
		"id": storyID, "req_id": reqID, "title": "S", "description": "d", "complexity": 1,
	})
	created.Timestamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	started := state.NewEvent(state.EventStoryStarted, "a", storyID, map[string]any{"branch": branch})
	events := []state.Event{created, started}
	if !mergedAt.IsZero() {
		merged := state.NewEvent(state.EventStoryMerged, "system", storyID, map[string]any{})
		merged.Timestamp = mergedAt
		events = append(events, merged)
	}
	for _, e := range events {
		if err := env.Events.Append(e); err != nil {
			t.Fatalf("append %s: %v", e.Type, err)
		}
		if err := env.Proj.Project(e); err != nil {
			t.Fatalf("project %s: %v", e.Type, err)
		}
	}
}

// initTestRepoAt creates a git repo at <dir>/<name> and returns its path.
func initTestRepoAt(t *testing.T, dir, name string) string {
	t.Helper()
	repo := filepath.Join(dir, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestRepo(t, repo)
	return repo
}

// addTestWorktree checks branch out in a new worktree OUTSIDE the repo (as
// the executor does, under a state dir) and returns the worktree path.
func addTestWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "worktrees", filepath.Base(branch))
	add := exec.Command("git", "worktree", "add", "-b", branch, wt)
	add.Dir = repo
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return wt
}

// gitOutput runs git in repo and returns its output; a git failure fails
// the test, so an assertion never passes on an error message.
func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
