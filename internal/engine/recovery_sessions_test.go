package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newRecoveryFixture(t *testing.T) (state.EventStore, *state.SQLiteStore) {
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
	return es, ps
}

func project(t *testing.T, es state.EventStore, ps state.ProjectionStore, evt state.Event) {
	t.Helper()
	if err := es.Append(evt); err != nil {
		t.Fatal(err)
	}
	if err := ps.Project(evt); err != nil {
		t.Fatal(err)
	}
}

// shimTmux installs a fake tmux that lists the given sessions and records
// every kill-session it receives in <dir>/killed.txt.
func shimTmux(t *testing.T, sessions []string, listExit int) string {
	t.Helper()
	dir := t.TempDir()
	killed := filepath.Join(dir, "killed.txt")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  list-sessions) printf '" + strings.Join(sessions, "\\n") + "\\n'; exit " + itoa(listExit) + " ;;\n" +
		"  kill-session) echo \"$3\" >> \"" + killed + "\" ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return killed
}

func TestRecoverStaleSessions(t *testing.T) {
	seed := func(t *testing.T) (state.EventStore, *state.SQLiteStore) {
		t.Helper()
		es, ps := newRecoveryFixture(t)
		for id, status := range map[string]state.EventType{"s-merged": state.EventStoryMerged, "s-live": state.EventStoryStarted} {
			project(t, es, ps, state.NewEvent(state.EventStoryCreated, "tl", id, map[string]any{"id": id, "req_id": "r", "title": id, "complexity": 1}))
			project(t, es, ps, state.NewEvent(status, "", id, map[string]any{"pr_number": 1}))
		}
		project(t, es, ps, state.NewEvent(state.EventAgentSpawned, "agent-merged", "s-merged", map[string]any{"role": "junior", "session_name": "nxd-merged"}))
		project(t, es, ps, state.NewEvent(state.EventAgentSpawned, "agent-live", "s-live", map[string]any{"role": "junior", "session_name": "nxd-live"}))
		return es, ps
	}

	t.Run("kills only sessions of merged stories", func(t *testing.T) {
		_, ps := seed(t)
		killed := shimTmux(t, []string{"nxd-merged", "nxd-live", "nxd-unknown", "user-shell"}, 0)

		actions := recoverStaleSessions(ps)
		if len(actions) != 1 || actions[0].StoryID != "s-merged" || actions[0].Type != "stale_session" {
			t.Fatalf("actions = %+v", actions)
		}
		if !strings.Contains(actions[0].Description, "nxd-merged") {
			t.Errorf("description = %q", actions[0].Description)
		}
		got, _ := os.ReadFile(killed)
		if strings.TrimSpace(string(got)) != "nxd-merged" {
			t.Errorf("killed sessions = %q, want only nxd-merged", got)
		}
	})

	t.Run("no tmux server is not an error", func(t *testing.T) {
		_, ps := seed(t)
		killed := shimTmux(t, nil, 1)
		if actions := recoverStaleSessions(ps); len(actions) != 0 {
			t.Fatalf("actions = %+v", actions)
		}
		if _, err := os.Stat(killed); !os.IsNotExist(err) {
			t.Error("nothing may be killed when tmux is unavailable")
		}
	})

	t.Run("no nxd sessions", func(t *testing.T) {
		_, ps := seed(t)
		shimTmux(t, []string{"user-shell"}, 0)
		if actions := recoverStaleSessions(ps); len(actions) != 0 {
			t.Fatalf("actions = %+v", actions)
		}
	})
}

func TestRecoverStuckMerges_BranchAlreadyInMain(t *testing.T) {
	es, ps := newRecoveryFixture(t)
	repo, wt := newPipelineRepo(t, "nxd/s-stuck")
	// The story's branch was merged (fast-forward) but the pipeline crashed
	// before STORY_MERGED was recorded.
	gitIn(t, repo, "merge", "--ff-only", "nxd/s-stuck")
	// The agent's worktree is gone (cleaned up before the crash); the branch
	// itself remains and is fully contained in main.
	gitIn(t, repo, "worktree", "remove", "--force", wt)
	for _, id := range []string{"s-stuck", "s-open"} {
		project(t, es, ps, state.NewEvent(state.EventStoryCreated, "tl", id, map[string]any{"id": id, "req_id": "r", "title": id, "complexity": 1}))
		project(t, es, ps, state.NewEvent(state.EventStoryQAPassed, "qa", id, nil))
	}
	gitIn(t, repo, "branch", "nxd/s-open") // exists but points at main: also "merged"
	gitIn(t, repo, "checkout", "-q", "-b", "nxd/s-open-work", "nxd/s-open")
	if err := os.WriteFile(filepath.Join(repo, "open.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "wip")
	gitIn(t, repo, "branch", "-f", "nxd/s-open", "nxd/s-open-work") // now ahead of main
	gitIn(t, repo, "checkout", "-q", "main")

	actions := recoverStuckMerges(repo, ps, es)
	if len(actions) != 1 || actions[0].StoryID != "s-stuck" || actions[0].Type != "stuck_merge" {
		t.Fatalf("actions = %+v", actions)
	}
	merged, _ := es.List(state.EventFilter{Type: state.EventStoryMerged, StoryID: "s-stuck"})
	if len(merged) != 1 || state.DecodePayload(merged[0].Payload)["source"] != "recovery" {
		t.Fatalf("STORY_MERGED = %+v", merged)
	}
	if st, _ := ps.GetStory("s-stuck"); st.Status != "merged" {
		t.Errorf("s-stuck status = %q, want merged", st.Status)
	}
	if st, _ := ps.GetStory("s-open"); st.Status != "pr_submitted" {
		t.Errorf("s-open status = %q, want pr_submitted (branch not merged)", st.Status)
	}
}

func TestRunRecovery_CombinesStrategies(t *testing.T) {
	es, ps := newRecoveryFixture(t)
	repo, _ := newPipelineRepo(t, "nxd/s-other")
	// s-orphan is in progress with no worktree; s-wt has a valid worktree at
	// the canonical <...>/worktrees/<storyID> path.
	if err := nxdgit.CreateWorktree(repo, filepath.Join(t.TempDir(), "worktrees", "s-wt"), "nxd/s-wt"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s-orphan", "s-wt"} {
		project(t, es, ps, state.NewEvent(state.EventStoryCreated, "tl", id, map[string]any{"id": id, "req_id": "r", "title": id, "complexity": 1}))
		project(t, es, ps, state.NewEvent(state.EventStoryStarted, "", id, nil))
	}
	shimTmux(t, nil, 1)

	actions := RunRecovery(repo, es, ps)
	if len(actions) != 1 || actions[0].StoryID != "s-orphan" || actions[0].Type != "orphaned_worktree" {
		t.Fatalf("actions = %+v", actions)
	}
	if st, _ := ps.GetStory("s-orphan"); st.Status != "draft" {
		t.Errorf("s-orphan status = %q, want draft", st.Status)
	}
	if st, _ := ps.GetStory("s-wt"); st.Status != "in_progress" {
		t.Errorf("s-wt status = %q, want in_progress (worktree intact)", st.Status)
	}
}
