package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestPollOnce_NativeAgent_AttemptScopedCompletion drives pollNativeAgent:
// a STORY_COMPLETED from an earlier attempt must not finish the polled one;
// the polled attempt's own completion must.
func TestPollOnce_NativeAgent_AttemptScopedCompletion(t *testing.T) {
	es, ps := newAttemptStores(t)
	ps.Project(state.NewEvent(state.EventStoryCreated, "tl", "s-nat", map[string]any{
		"id": "s-nat", "req_id": "r", "title": "t", "description": "d", "complexity": 1,
	}))
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{
		"gemma": {Native: true, Models: []string{"gemma4"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	mon := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	active := map[string]ActiveAgent{
		"native-s-nat": {
			Assignment:   Assignment{StoryID: "s-nat", AgentID: "junior-2", SessionName: "native-s-nat", AttemptID: "s-nat-a2"},
			RuntimeName:  "gemma",
			WorktreePath: t.TempDir(),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup

	// Stale completion from attempt 1.
	es.Append(state.NewEventForAttempt(state.EventStoryCompleted, "junior-1", "s-nat", "s-nat-a1", nil))
	mon.pollOnce(ctx, &wg, active, t.TempDir())
	if len(active) != 1 {
		t.Fatal("attempt 1's completion must not finish attempt 2")
	}

	// Attempt 2 completes.
	es.Append(state.NewEventForAttempt(state.EventStoryCompleted, "junior-2", "s-nat", "s-nat-a2", nil))
	mon.pollOnce(ctx, &wg, active, t.TempDir())
	wg.Wait()
	if len(active) != 0 {
		t.Fatal("attempt 2's completion must finish the agent")
	}
}

// erroringRuntime fails ReadOutput while reporting StatusWorking.
type erroringRuntime struct{ stuckRuntime }

func (r *erroringRuntime) ReadOutput(string, int) (string, error) { return "", errors.New("pane gone") }

func TestWatchdog_CheckAgent_ReadOutputErrorIsNotStuck(t *testing.T) {
	wd, _, es := newClockedWatchdog(t, 0)
	r := wd.CheckAgent("sess", &erroringRuntime{stuckRuntime{status: runtime.StatusWorking}}, AgentIdentity{})
	if r.Action != "none" || r.Status != runtime.StatusWorking {
		t.Fatalf("unreadable output must not be judged stuck, got %+v", r)
	}
	if evts, _ := es.List(state.EventFilter{Type: state.EventAgentStuck}); len(evts) != 0 {
		t.Fatal("no AGENT_STUCK expected")
	}
}

func TestReportNoDispatch_NothingPendingLogsOnly(t *testing.T) {
	es, ps := newAttemptStores(t)
	m := newWaveMonitor(t, es, ps)
	m.reportNoDispatch(&RunContext{ReqID: "r"}, waveProgress{completed: map[string]bool{"a": true}}, nil)
	if n, _ := es.Count(state.EventFilter{}); n != 0 {
		t.Fatalf("no events expected when nothing is pending, got %d", n)
	}
}

func TestCheckIntegration_DefaultBuildRunnerOnUnknownProject(t *testing.T) {
	es, ps := integrationStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	m := newIntegrationMonitor(t, es, ps, true, nil, llm.NewReplayClient())
	m.integrationBuild = nil // use runIntegrationBuild: unknown project kind → no-op
	if m.checkIntegration(context.Background(), "s-int", "", t.TempDir()) {
		t.Fatal("unknown build system is a no-op, must not pause")
	}
}

// TestCompleteRequirement_GatePaths covers the completion-gate branch of the
// end-of-requirement path: green → REQ_COMPLETED, red → REQ_BLOCKED.
func TestCompleteRequirement_GatePaths(t *testing.T) {
	tests := []struct {
		name   string
		verify VerificationResult
		want   state.EventType
		notSet state.EventType
	}{
		{"green gate completes", green(), state.EventReqCompleted, state.EventReqBlocked},
		{"red gate blocks", red(), state.EventReqBlocked, state.EventReqCompleted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			es, ps := newAttemptStores(t)
			seedWaveReq(t, es, ps, "r-gate", map[string]string{"a": "merged"})
			m := newWaveMonitor(t, es, ps)
			verify, _ := scriptedVerify(tc.verify)
			gate := NewCompletionGate(nil, "", 0, -1, "main", es, ps) // nil client ⇒ hard gate
			gate.verify = verify
			gate.pull = func(string, string) {}
			m.SetCompletionGate(gate)

			stories, _ := ps.ListStories(state.StoryFilter{ReqID: "r-gate"})
			m.completeRequirement(context.Background(), &RunContext{ReqID: "r-gate", DAG: graph.New()}, t.TempDir(), stories)

			if got, _ := es.List(state.EventFilter{Type: tc.want}); len(got) != 1 {
				t.Fatalf("expected 1 %s, got %d", tc.want, len(got))
			}
			if got, _ := es.List(state.EventFilter{Type: tc.notSet}); len(got) != 0 {
				t.Fatalf("did not expect %s", tc.notSet)
			}
		})
	}
}

// ── rebaseAndMerge on a real local repo ─────────────────────────────────────

type fakeLocalMerge struct {
	calls []string
	err   error
}

func (f *fakeLocalMerge) Merge(featureBranch, baseBranch string) (git.MergeResult, error) {
	f.calls = append(f.calls, featureBranch+"->"+baseBranch)
	return git.MergeResult{MergedSHA: "abc"}, f.err
}
func (f *fakeLocalMerge) CanMerge(string, string) (bool, []string, error) { return true, nil, nil }

// rebaseRepo builds a local repo on main with a story worktree on nxd/s-rb.
// The story branch changes story.txt; the base advances after the branch was
// cut so a rebase is required. conflict=true makes main edit the same file.
func rebaseRepo(t *testing.T, conflict bool) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(repo, "init", "-q", "--initial-branch=main")
	run(repo, "config", "user.email", "t@t")
	run(repo, "config", "user.name", "t")
	write(repo, "story.txt", "base\n")
	run(repo, "add", ".")
	run(repo, "commit", "-qm", "base")

	worktree = filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-q", "-b", "nxd/s-rb", worktree, "main")
	run(worktree, "config", "user.email", "t@t")
	run(worktree, "config", "user.name", "t")
	write(worktree, "story.txt", "story version\n")
	run(worktree, "add", ".")
	run(worktree, "commit", "-qm", "story work")

	if conflict {
		write(repo, "story.txt", "main version\n")
	} else {
		write(repo, "other.txt", "unrelated\n")
	}
	run(repo, "add", ".")
	run(repo, "commit", "-qm", "main moved on")
	return repo, worktree
}

func newRebaseMonitor(t *testing.T, runner CommandRunner, merge *fakeLocalMerge, resolver llm.Client) (*Monitor, state.EventStore) {
	t.Helper()
	es, ps := newControllerTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-RB", "s-rb")
	cfg := config.DefaultConfig()
	cfg.Merge.BaseBranch = "main"
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	qa := NewQA(QAConfig{TestCommand: "go test ./..."}, runner, es, ps)
	merger := NewLocalMerger(cfg.Merge, merge, es, ps)
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, qa, merger, cfg, es, ps)
	if resolver != nil {
		m.SetConflictResolver(NewConflictResolver(resolver, "m", 100, es))
	}
	return m, es
}

func TestRebaseAndMerge_CleanRebaseSkipsQAAndMerges(t *testing.T) {
	repo, wt := rebaseRepo(t, false)
	runner := &scriptedRunner{output: "ok"}
	merge := &fakeLocalMerge{}
	m, _ := newRebaseMonitor(t, runner, merge, nil)

	res, err := m.rebaseAndMerge(context.Background(), "s-rb", "s-rb-a1", "nxd/s-rb", repo, wt)
	if err != nil {
		t.Fatalf("rebaseAndMerge: %v", err)
	}
	if !res.Merged || len(merge.calls) != 1 {
		t.Fatalf("expected one local merge, got merged=%v calls=%v", res.Merged, merge.calls)
	}
	if runner.calls != 0 {
		t.Fatalf("clean rebase must not re-run QA, ran %d", runner.calls)
	}
	// The rebased branch now contains main's commit.
	if _, err := os.Stat(filepath.Join(wt, "other.txt")); err != nil {
		t.Fatalf("worktree not rebased onto main: %v", err)
	}
}

func TestRebaseAndMerge_ResolvedConflictRedQABlocksMerge(t *testing.T) {
	repo, wt := rebaseRepo(t, true)
	runner := &scriptedRunner{output: "--- FAIL: TestX", err: errors.New("exit 1")}
	merge := &fakeLocalMerge{}
	resolverLLM := llm.NewReplayClient(llm.CompletionResponse{Content: "merged version\n"})
	m, es := newRebaseMonitor(t, runner, merge, resolverLLM)

	_, err := m.rebaseAndMerge(context.Background(), "s-rb", "s-rb-a1", "nxd/s-rb", repo, wt)
	if !errors.Is(err, errPostRebaseQA) {
		t.Fatalf("expected errPostRebaseQA, got %v", err)
	}
	if len(merge.calls) != 0 {
		t.Fatalf("red post-rebase QA must block the merge, got %v", merge.calls)
	}
	if runner.calls != 1 {
		t.Fatalf("QA must re-run once after conflict resolution, ran %d", runner.calls)
	}
	if n := conflictsResolvedCount(es, "s-rb"); n != 1 {
		t.Fatalf("resolver should have recorded one resolution, got %d", n)
	}
	body, _ := os.ReadFile(filepath.Join(wt, "story.txt"))
	if !contains(string(body), "merged version") || contains(string(body), "<<<<<<<") {
		t.Fatalf("worktree should hold the LLM resolution, got %q", body)
	}
}

func TestRebaseAndMerge_ResolvedConflictGreenQAMerges(t *testing.T) {
	repo, wt := rebaseRepo(t, true)
	runner := &scriptedRunner{output: "ok"}
	merge := &fakeLocalMerge{}
	m, _ := newRebaseMonitor(t, runner, merge, llm.NewReplayClient(llm.CompletionResponse{Content: "merged version\n"}))

	res, err := m.rebaseAndMerge(context.Background(), "s-rb", "s-rb-a1", "nxd/s-rb", repo, wt)
	if err != nil || !res.Merged {
		t.Fatalf("green post-rebase QA must merge, got merged=%v err=%v", res.Merged, err)
	}
	if runner.calls != 1 || len(merge.calls) != 1 {
		t.Fatalf("expected 1 QA run and 1 merge, got qa=%d merge=%v", runner.calls, merge.calls)
	}
}

func TestRebaseAndMerge_ConflictWithoutResolverFails(t *testing.T) {
	repo, wt := rebaseRepo(t, true)
	merge := &fakeLocalMerge{}
	m, _ := newRebaseMonitor(t, &scriptedRunner{output: "ok"}, merge, nil)

	_, err := m.rebaseAndMerge(context.Background(), "s-rb", "", "nxd/s-rb", repo, wt)
	if err == nil || len(merge.calls) != 0 {
		t.Fatalf("conflict without a resolver must fail before merging, err=%v calls=%v", err, merge.calls)
	}
}
