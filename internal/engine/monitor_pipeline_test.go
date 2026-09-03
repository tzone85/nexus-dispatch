package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/routing"
	"github.com/tzone85/nexus-dispatch/internal/security"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// pipelineFixture drives Monitor.postExecutionPipeline end to end against a
// real FileStore + SQLite projection, a real git repo with a worktree branch
// carrying one commit, a replay LLM for the reviewer, a scripted QA runner
// and a real local merger. Every knob defaults to the green path so a test
// only has to name the failure it wants.
type pipelineFixture struct {
	t        *testing.T
	es       state.EventStore
	ps       state.ProjectionStore
	repo     string
	worktree string
	branch   string
	req      string
	story    string
	attempt  string
	cfg      config.Config
	reviewer *Reviewer
	qa       *QA
	qaRunner *scriptedRunner
	merger   *Merger
	m        *Monitor
}

const (
	pipeReq     = "REQ-PIPE"
	pipeStory   = "s-pipe"
	pipeAttempt = "s-pipe-a01HZX0000000000000000000A"
)

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newPipelineRepo creates a main-branch repo with one base commit and a
// worktree on branch with one feature commit.
func newPipelineRepo(t *testing.T, branch string) (repo, worktree string) {
	t.Helper()
	repo = t.TempDir()
	gitIn(t, repo, "init", "-b", "main")
	gitIn(t, repo, "config", "user.email", "t@t.t")
	gitIn(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "base")

	worktree = filepath.Join(t.TempDir(), "wt")
	if err := nxdgit.CreateWorktree(repo, worktree, branch); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, worktree, "add", ".")
	gitIn(t, worktree, "commit", "-m", "feature")
	return repo, worktree
}

// reviewJSON is a text-mode reviewer reply (provider "test" has no tool support).
func reviewJSON(passed bool, summary string) llm.CompletionResponse {
	return llm.CompletionResponse{Content: fmt.Sprintf(`{"passed": %t, "comments": [], "summary": %q}`, passed, summary)}
}

// pipelineStores returns a file-backed event store and a file-backed SQLite
// projection (":memory:" would hand a second pooled connection an empty DB).
func pipelineStores(t *testing.T) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(dir, "proj.db"))
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	t.Cleanup(func() {
		es.Close()
		ps.Close()
	})
	return es, ps
}

func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	es, ps := pipelineStores(t)
	seedCapacityStory(t, es, ps, pipeReq, pipeStory)
	branch := "nxd/" + pipeStory
	repo, worktree := newPipelineRepo(t, branch)

	cfg := config.DefaultConfig()
	cfg.Merge.BaseBranch = "main"
	cfg.Merge.Mode = MergeModeLocal
	cfg.Merge.ReviewBeforeMerge = false
	cfg.Routing.MaxRetriesBeforeEscalation = 2
	cfg.Approvals.RequireFor = nil
	cfg.QA.SuccessCriteria = nil

	f := &pipelineFixture{
		t: t, es: es, ps: ps, repo: repo, worktree: worktree, branch: branch,
		req: pipeReq, story: pipeStory, attempt: pipeAttempt, cfg: cfg,
	}
	f.reviewer = NewReviewer(llm.NewReplayClient(reviewJSON(true, "looks good")), "test", "test-model", 4000, es, ps)
	f.qaRunner = &scriptedRunner{output: "ok"}
	f.qa = NewQA(QAConfig{TestCommand: "go test ./..."}, f.qaRunner, es, ps)
	f.merger = NewLocalMerger(cfg.Merge, nxdgit.NewLocalMerger(repo), es, ps)
	return f
}

// build wires the monitor from the fixture's current components.
func (f *pipelineFixture) build() *Monitor {
	f.m = NewMonitor(nil, nil, f.reviewer, f.qa, f.merger, f.cfg, f.es, f.ps)
	return f.m
}

func (f *pipelineFixture) agent() ActiveAgent {
	return ActiveAgent{
		Assignment: Assignment{
			StoryID: f.story, AgentID: "agent-1", SessionName: "nxd-" + f.story,
			Branch: f.branch, AttemptID: f.attempt, Role: agent.RoleJunior,
		},
		WorktreePath: f.worktree,
	}
}

func (f *pipelineFixture) run() {
	f.t.Helper()
	if f.m == nil {
		f.build()
	}
	f.m.postExecutionPipeline(context.Background(), f.agent(), f.repo)
}

func (f *pipelineFixture) events(typ state.EventType) []state.Event {
	f.t.Helper()
	evts, err := f.es.List(state.EventFilter{Type: typ, StoryID: f.story})
	if err != nil {
		f.t.Fatal(err)
	}
	return evts
}

func (f *pipelineFixture) allEvents(typ state.EventType) []state.Event {
	f.t.Helper()
	evts, err := f.es.List(state.EventFilter{Type: typ})
	if err != nil {
		f.t.Fatal(err)
	}
	return evts
}

// one asserts exactly one event of typ for the story and returns it.
func (f *pipelineFixture) one(typ state.EventType) state.Event {
	f.t.Helper()
	evts := f.events(typ)
	if len(evts) != 1 {
		f.t.Fatalf("expected exactly one %s for %s, got %d", typ, f.story, len(evts))
	}
	return evts[0]
}

func (f *pipelineFixture) none(typ state.EventType) {
	f.t.Helper()
	if evts := f.events(typ); len(evts) != 0 {
		f.t.Fatalf("expected no %s for %s, got %d", typ, f.story, len(evts))
	}
}

func (f *pipelineFixture) storyStatus() string {
	f.t.Helper()
	st, err := f.ps.GetStory(f.story)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Status
}

func (f *pipelineFixture) reqStatus() string {
	f.t.Helper()
	req, err := f.ps.GetRequirement(f.req)
	if err != nil {
		f.t.Fatal(err)
	}
	return req.Status
}

func (f *pipelineFixture) pauseReason() string {
	f.t.Helper()
	pauses := f.allEvents(state.EventReqPaused)
	if len(pauses) == 0 {
		f.t.Fatal("expected a REQ_PAUSED event")
	}
	reason, _ := state.DecodePayload(pauses[len(pauses)-1].Payload)["reason"].(string)
	return reason
}

// resetEvents returns the STORY_REVIEW_FAILED events emitted by a story
// reset (they carry a "reason"); the reviewer's own verdict event carries
// "passed" instead and is excluded.
func (f *pipelineFixture) resetEvents() []state.Event {
	f.t.Helper()
	var out []state.Event
	for _, evt := range f.events(state.EventStoryReviewFailed) {
		if _, ok := state.DecodePayload(evt.Payload)["reason"]; ok {
			out = append(out, evt)
		}
	}
	return out
}

// resetReason returns the reason of the single story reset, asserting the
// attempt stamp and the emitting agent.
func (f *pipelineFixture) resetReason(fromAgent string) string {
	f.t.Helper()
	resets := f.resetEvents()
	if len(resets) != 1 {
		f.t.Fatalf("expected exactly one story reset for %s, got %d", f.story, len(resets))
	}
	evt := resets[0]
	if evt.AttemptID != f.attempt {
		f.t.Fatalf("STORY_REVIEW_FAILED attempt_id = %q, want %q", evt.AttemptID, f.attempt)
	}
	if evt.AgentID != fromAgent {
		f.t.Fatalf("STORY_REVIEW_FAILED agent = %q, want %q", evt.AgentID, fromAgent)
	}
	reason, _ := state.DecodePayload(evt.Payload)["reason"].(string)
	return reason
}

// stageResults maps STAGE_COMPLETED stage -> result for the story.
func (f *pipelineFixture) stageResults() map[string]string {
	f.t.Helper()
	out := map[string]string{}
	for _, evt := range f.events(state.EventStageCompleted) {
		p := state.DecodePayload(evt.Payload)
		stage, _ := p["stage"].(string)
		result, _ := p["outcome"].(string)
		out[stage] = result
	}
	return out
}

func TestPostExecutionPipeline_GreenPath_MergesLocally(t *testing.T) {
	f := newPipelineFixture(t)
	f.run()

	for _, typ := range []state.EventType{state.EventStoryReviewPassed, state.EventStoryQAStarted, state.EventStoryQAPassed} {
		evt := f.one(typ)
		if evt.AttemptID != f.attempt {
			t.Errorf("%s attempt_id = %q, want %q", typ, evt.AttemptID, f.attempt)
		}
	}
	pr := f.one(state.EventStoryPRCreated)
	if url, _ := state.DecodePayload(pr.Payload)["pr_url"].(string); url != "local://merged" {
		t.Errorf("pr_url = %q, want local://merged", url)
	}
	merged := f.one(state.EventStoryMerged)
	mp := state.DecodePayload(merged.Payload)
	if mp["branch"] != f.branch {
		t.Errorf("merged branch = %v, want %s", mp["branch"], f.branch)
	}
	if sha, _ := mp["merged_sha"].(string); sha != gitIn(t, f.repo, "rev-parse", "main") {
		t.Errorf("merged_sha %q is not the new main HEAD", sha)
	}
	if got := f.storyStatus(); got != "merged" {
		t.Errorf("story status = %q, want merged", got)
	}
	if got := f.stageResults(); got["review"] != "success" || got["qa"] != "success" || got["merge"] != "success" {
		t.Errorf("stage results = %v, want review/qa/merge success", got)
	}
	// The feature landed on main and the worktree + branch were cleaned up.
	if _, err := os.Stat(filepath.Join(f.repo, "feature.txt")); err != nil {
		t.Errorf("feature.txt not on main after merge: %v", err)
	}
	if _, err := os.Stat(f.worktree); !os.IsNotExist(err) {
		t.Errorf("worktree %s should be removed after merge (err=%v)", f.worktree, err)
	}
	if nxdgit.BranchExists(f.repo, f.branch) {
		t.Errorf("branch %s should be deleted after merge", f.branch)
	}
	f.none(state.EventStoryReviewFailed)
	if f.qaRunner.calls != 1 {
		t.Errorf("QA runner calls = %d, want 1", f.qaRunner.calls)
	}
}

func TestPostExecutionPipeline_ArtifactsAndBayesian(t *testing.T) {
	f := newPipelineFixture(t)
	store, err := artifact.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router := routing.NewBayesianRouter()
	before := router.Route(3)
	m := f.build()
	m.SetArtifactStore(store)
	m.SetBayesianRouter(router)
	f.run()

	f.one(state.EventStoryMerged)
	for _, name := range []string{string(artifact.TypeGitDiff) + ".patch", string(artifact.TypeReviewResult) + ".json", string(artifact.TypeQAResult) + ".json"} {
		data, err := store.Read(f.story, name)
		if err != nil || len(data) == 0 {
			t.Errorf("artifact %s not written: %v", name, err)
		}
	}
	review, _ := store.Read(f.story, string(artifact.TypeReviewResult)+".json")
	if !strings.Contains(string(review), `"passed": true`) && !strings.Contains(string(review), `"passed":true`) {
		t.Errorf("review artifact = %s", review)
	}
	// A recorded success must not make the router prefer a higher tier for
	// the same complexity; the prior for the junior role only got stronger.
	if after := router.Route(3); after != before && after == agent.RoleSenior {
		t.Errorf("router escalated after a recorded success: before=%s after=%s", before, after)
	}
}

func TestPostExecutionPipeline_ReviewRejected_ResetsToDraft(t *testing.T) {
	f := newPipelineFixture(t)
	f.reviewer = NewReviewer(llm.NewReplayClient(reviewJSON(false, "missing tests")), "test", "test-model", 4000, f.es, f.ps)
	f.run()

	reason := f.resetReason("reviewer")
	if !strings.Contains(reason, "review rejected: missing tests") {
		t.Errorf("reset reason = %q", reason)
	}
	if got := f.storyStatus(); got != "draft" {
		t.Errorf("story status = %q, want draft", got)
	}
	if got := f.stageResults()["review"]; got != "failure" {
		t.Errorf("review stage = %q, want failure", got)
	}
	f.none(state.EventStoryQAStarted)
	f.none(state.EventStoryMerged)
	if f.qaRunner.calls != 0 {
		t.Errorf("QA must not run after a rejected review; calls = %d", f.qaRunner.calls)
	}
}

func TestPostExecutionPipeline_ReviewRejected_CriteriaAuthoritativeProceeds(t *testing.T) {
	f := newPipelineFixture(t)
	f.cfg.QA.CriteriaAuthoritative = true
	f.cfg.QA.SuccessCriteria = []config.SuccessCriterion{{Kind: "file_exists", Path: "feature.txt"}}
	f.reviewer = NewReviewer(llm.NewReplayClient(reviewJSON(false, "reviewer invents requirements")), "test", "test-model", 4000, f.es, f.ps)
	f.run()

	if resets := f.resetEvents(); len(resets) != 0 {
		t.Fatalf("advisory review must not reset the story, got %d resets", len(resets))
	}
	if got := f.stageResults()["review"]; got != "success" {
		t.Errorf("advisory review stage = %q, want success", got)
	}
	f.one(state.EventStoryMerged)
	if got := f.storyStatus(); got != "merged" {
		t.Errorf("story status = %q, want merged", got)
	}
}

func TestPostExecutionPipeline_ReviewErrors(t *testing.T) {
	t.Run("fatal API error pauses the requirement", func(t *testing.T) {
		f := newPipelineFixture(t)
		fatal := &llm.APIError{Provider: "anthropic", StatusCode: 401, Message: "invalid api key"}
		f.reviewer = NewReviewer(llm.NewErrorClient(fatal), "test", "test-model", 4000, f.es, f.ps)
		f.run()

		if got := f.reqStatus(); got != "paused" {
			t.Fatalf("requirement status = %q, want paused", got)
		}
		if reason := f.pauseReason(); !strings.Contains(reason, "fatal API error") {
			t.Errorf("pause reason = %q", reason)
		}
		f.none(state.EventStoryReviewFailed)
		f.none(state.EventStoryMerged)
	})

	t.Run("ordinary error resets the story with the error text", func(t *testing.T) {
		f := newPipelineFixture(t)
		f.reviewer = NewReviewer(llm.NewErrorClient(errors.New("malformed response body")), "test", "test-model", 4000, f.es, f.ps)
		f.run()

		reason := f.resetReason("reviewer")
		if !strings.Contains(reason, "review error:") || !strings.Contains(reason, "malformed response body") {
			t.Errorf("reset reason = %q", reason)
		}
		if got := f.reqStatus(); got == "paused" {
			t.Error("an ordinary review error must not pause the requirement")
		}
	})
}

func TestPostExecutionPipeline_QAFails_FeedbackAndReset(t *testing.T) {
	f := newPipelineFixture(t)
	f.qaRunner = &scriptedRunner{output: "--- FAIL: TestX\nundefined: Foo", err: errors.New("exit status 1")}
	f.qa = NewQA(QAConfig{TestCommand: "go test ./..."}, f.qaRunner, f.es, f.ps)
	f.run()

	f.one(state.EventStoryReviewPassed)
	qaFailed := f.events(state.EventStoryQAFailed)
	// One from QA.Run itself (agent "qa"), one carrying the retry feedback
	// (agent "monitor", source qa_failure).
	if len(qaFailed) != 2 {
		t.Fatalf("expected 2 STORY_QA_FAILED (qa result + feedback), got %d", len(qaFailed))
	}
	var feedback string
	for _, evt := range qaFailed {
		if evt.AttemptID != f.attempt {
			t.Errorf("STORY_QA_FAILED attempt_id = %q, want %q", evt.AttemptID, f.attempt)
		}
		p := state.DecodePayload(evt.Payload)
		if p["source"] == "qa_failure" {
			feedback, _ = p["feedback"].(string)
		}
	}
	if !strings.Contains(feedback, "QA FAILURE") || !strings.Contains(feedback, "undefined: Foo") || !strings.Contains(feedback, "Hint:") {
		t.Errorf("QA feedback = %q", feedback)
	}
	if reason := f.resetReason("qa"); reason != feedback {
		t.Errorf("reset reason must equal the QA feedback so the retry agent sees it\nreason=%q\nfeedback=%q", reason, feedback)
	}
	if got := f.stageResults()["qa"]; got != "failure" {
		t.Errorf("qa stage = %q, want failure", got)
	}
	if got := f.storyStatus(); got != "draft" {
		t.Errorf("story status = %q, want draft", got)
	}
	f.none(state.EventStoryMerged)
}

func TestPostExecutionPipeline_ConflictMarkers_ResetBeforeReview(t *testing.T) {
	f := newPipelineFixture(t)
	marked := "package main\n<<<<<<< HEAD\nvar x = 1\n=======\nvar x = 2\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(f.worktree, "main.go"), []byte(marked), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run()

	reason := f.resetReason("monitor")
	if !strings.Contains(reason, "unresolved conflict markers in 1 file(s)") {
		t.Errorf("reset reason = %q", reason)
	}
	f.none(state.EventStoryReviewPassed)
	f.none(state.EventStoryQAStarted)
	// autoCommit captured the dirty worktree before the check ran.
	if out := gitIn(t, f.worktree, "status", "--porcelain"); out != "" {
		t.Errorf("worktree should be clean after auto-commit, got %q", out)
	}
}

func TestPostExecutionPipeline_EmptyDiff_ResetsToDraft(t *testing.T) {
	f := newPipelineFixture(t)
	// Drop the feature commit so the branch equals main.
	gitIn(t, f.worktree, "reset", "--hard", "main")
	f.run()

	reason := f.resetReason("monitor")
	if reason != "agent produced no code changes" {
		t.Errorf("reset reason = %q", reason)
	}
	f.none(state.EventStoryReviewPassed)
	if got := f.reqStatus(); got == "paused" {
		t.Error("an empty diff without a capacity error must not pause")
	}
}

func TestPostExecutionPipeline_GitDiffError_ResetsToDraft(t *testing.T) {
	f := newPipelineFixture(t)
	f.worktree = filepath.Join(t.TempDir(), "does-not-exist")
	f.run()

	reason := f.resetReason("monitor")
	if !strings.HasPrefix(reason, "git diff error:") {
		t.Errorf("reset reason = %q, want git diff error prefix", reason)
	}
	f.none(state.EventStoryReviewPassed)
}

func TestPostExecutionPipeline_DryRun_SimulatesAndMerges(t *testing.T) {
	f := newPipelineFixture(t)
	gitIn(t, f.worktree, "reset", "--hard", "main") // agent produced nothing
	m := f.build()
	m.SetDryRun(true)
	f.run()

	f.one(state.EventStoryMerged)
	data, err := os.ReadFile(filepath.Join(f.repo, "dry-run-simulation.txt"))
	if err != nil {
		t.Fatalf("simulated file not merged to main: %v", err)
	}
	if !strings.Contains(string(data), "[DRY RUN] Simulated changes for story "+f.story) {
		t.Errorf("simulation content = %q", data)
	}
	if got := f.storyStatus(); got != "merged" {
		t.Errorf("story status = %q, want merged", got)
	}
}

func TestPostExecutionPipeline_ReviewBeforeMerge_ParksAsMergeReady(t *testing.T) {
	f := newPipelineFixture(t)
	f.cfg.Merge.ReviewBeforeMerge = true
	f.run()

	f.one(state.EventStoryQAPassed)
	f.one(state.EventStoryMergeReady)
	f.none(state.EventStoryMerged)
	if got := f.storyStatus(); got != "merge_ready" {
		t.Errorf("story status = %q, want merge_ready", got)
	}
	if nxdgit.BranchExists(f.repo, f.branch) != true {
		t.Error("branch must survive until a human merges it")
	}
}

func TestPostExecutionPipeline_MergeApprovalPending_PausesWithMergeReady(t *testing.T) {
	f := newPipelineFixture(t)
	f.cfg.Approvals.RequireFor = []string{"merge"}
	m := f.build()
	q, err := approvals.Load(f.es)
	if err != nil {
		t.Fatal(err)
	}
	m.SetApprovalQueue(q)
	f.run()

	pending := q.PendingForStory(f.req, f.story, approvals.KindMerge)
	if len(pending) != 1 {
		t.Fatalf("expected one pending merge approval, got %d", len(pending))
	}
	ready := f.one(state.EventStoryMergeReady)
	if ready.AttemptID != f.attempt {
		t.Errorf("STORY_MERGE_READY attempt_id = %q, want %q", ready.AttemptID, f.attempt)
	}
	if got := f.storyStatus(); got != "merge_ready" {
		t.Errorf("story status = %q, want merge_ready", got)
	}
	if got := f.reqStatus(); got != "paused" {
		t.Errorf("requirement status = %q, want paused", got)
	}
	reason := f.pauseReason()
	if !strings.Contains(reason, pending[0].ID) || !strings.Contains(reason, "nxd merge "+f.story) {
		t.Errorf("pause reason = %q", reason)
	}
	f.none(state.EventStoryMerged)
}

func TestPostExecutionPipeline_MergeApprovalRejected_ResetsToDraft(t *testing.T) {
	f := newPipelineFixture(t)
	f.cfg.Approvals.RequireFor = []string{"merge"}
	m := f.build()
	q, err := approvals.Load(f.es)
	if err != nil {
		t.Fatal(err)
	}
	m.SetApprovalQueue(q)
	it, err := q.Request(f.req, f.story, approvals.KindMerge, "merge of "+f.branch, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Resolve(it.ID, approvals.StatusRejected, "alice", "not this way"); err != nil {
		t.Fatal(err)
	}
	f.run()

	reason := f.resetReason("approvals")
	if !strings.Contains(reason, it.ID) || !strings.Contains(reason, "rejected by alice") || !strings.Contains(reason, "not this way") {
		t.Errorf("reset reason = %q", reason)
	}
	if got := f.storyStatus(); got != "draft" {
		t.Errorf("story status = %q, want draft", got)
	}
	f.none(state.EventStoryMerged)
	f.none(state.EventStoryMergeReady)
}

func TestPostExecutionPipeline_RebaseConflict_ResetsWithMergeError(t *testing.T) {
	f := newPipelineFixture(t)
	// main moves on with a conflicting edit to the same file.
	if err := os.WriteFile(filepath.Join(f.repo, "feature.txt"), []byte("main says otherwise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "add", ".")
	gitIn(t, f.repo, "commit", "-m", "main conflict")
	f.run()

	f.one(state.EventStoryQAPassed)
	reason := f.resetReason("merger")
	if !strings.Contains(reason, "merge/rebase error:") || !strings.Contains(reason, "rebase onto main") {
		t.Errorf("reset reason = %q", reason)
	}
	if got := f.stageResults()["merge"]; got != "failure" {
		t.Errorf("merge stage = %q, want failure", got)
	}
	f.none(state.EventStoryMerged)
	// The failed rebase was aborted: the worktree is usable again.
	if _, err := os.Stat(filepath.Join(f.worktree, ".git")); err != nil {
		t.Errorf("worktree gone after aborted rebase: %v", err)
	}
	if out := gitIn(t, f.worktree, "status", "--porcelain"); out != "" {
		t.Errorf("worktree dirty after aborted rebase: %q", out)
	}
}

func TestPostExecutionPipeline_SecurityGate(t *testing.T) {
	t.Run("critical scanner finding in a changed file pauses", func(t *testing.T) {
		f := newPipelineFixture(t)
		m := f.build()
		crit := security.Finding{Tool: "gitleaks", RuleID: "aws", Severity: security.SeverityCritical, File: "feature.txt", Line: 1, Title: "AWS key", Source: "scanner"}
		g := NewSecurityGate(nil, "test-model", 1000, filepath.Join(t.TempDir(), "kb.json"), security.SeverityHigh, false, f.es, f.ps)
		g.scan = fakeScan(crit)
		m.SetSecurityGate(g)
		f.run()

		f.one(state.EventStoryQAPassed)
		f.one(state.EventStorySecurityFailed)
		if got := f.reqStatus(); got != "paused" {
			t.Fatalf("requirement status = %q, want paused", got)
		}
		if reason := f.pauseReason(); !strings.Contains(reason, "security gate:") || !strings.Contains(reason, "AWS key") {
			t.Errorf("pause reason = %q", reason)
		}
		f.none(state.EventStoryMerged)
	})

	t.Run("clean scan merges", func(t *testing.T) {
		f := newPipelineFixture(t)
		m := f.build()
		g := NewSecurityGate(nil, "test-model", 1000, filepath.Join(t.TempDir(), "kb.json"), security.SeverityHigh, false, f.es, f.ps)
		g.scan = fakeScan()
		m.SetSecurityGate(g)
		f.run()

		f.one(state.EventStorySecurityPassed)
		f.one(state.EventStoryMerged)
	})
}

func TestPostExecutionPipeline_IntegrationBuildFails_PausesAfterMerge(t *testing.T) {
	f := newPipelineFixture(t)
	f.cfg.QA.PauseOnIntegrationFailure = true
	m := f.build()
	m.techLeadFixer = NewTechLeadFixer(llm.NewReplayClient(llm.CompletionResponse{Content: "fix the import"}), "test-model", 500, f.es, f.ps)
	m.integrationBuild = func(string) error { return errors.New("./main.go:3: undefined: Foo") }
	f.run()

	f.one(state.EventStoryMerged)
	// One from the monitor (attempt-stamped) and one from the fixer's record.
	var failed state.Event
	for _, evt := range f.events(state.EventStoryIntegrationFailed) {
		if evt.AgentID == "monitor" {
			failed = evt
		}
	}
	if failed.ID == "" {
		t.Fatal("expected a monitor STORY_INTEGRATION_FAILED")
	}
	if failed.AttemptID != f.attempt {
		t.Errorf("STORY_INTEGRATION_FAILED attempt_id = %q, want %q", failed.AttemptID, f.attempt)
	}
	p := state.DecodePayload(failed.Payload)
	if p["error"] != "./main.go:3: undefined: Foo" || p["paused"] != true {
		t.Errorf("integration payload = %v", p)
	}
	if got := f.reqStatus(); got != "paused" {
		t.Errorf("requirement status = %q, want paused", got)
	}
	if reason := f.pauseReason(); !strings.Contains(reason, "post-merge integration build failed after merging "+f.story) {
		t.Errorf("pause reason = %q", reason)
	}
}

func TestPostExecutionPipeline_BinaryStrippedBeforeReview(t *testing.T) {
	f := newPipelineFixture(t)
	bin := make([]byte, 64)
	for i := range bin {
		bin[i] = byte(i) // contains NUL bytes → git treats it as binary
	}
	if err := os.WriteFile(filepath.Join(f.worktree, "server"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.worktree, "add", "server")
	gitIn(t, f.worktree, "commit", "-m", "oops binary")
	f.run()

	f.one(state.EventStoryMerged)
	if _, err := os.Stat(filepath.Join(f.repo, "server")); !os.IsNotExist(err) {
		t.Errorf("binary must not reach main (err=%v)", err)
	}
	gi, err := os.ReadFile(filepath.Join(f.repo, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), "server") {
		t.Errorf(".gitignore on main should list the stripped binary; err=%v content=%q", err, gi)
	}
}

func TestStripBinariesFromBranch(t *testing.T) {
	t.Run("no binaries leaves the commit untouched", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-nobin")
		before := gitIn(t, wt, "rev-parse", "HEAD")
		stripBinariesFromBranch(wt, "s-nobin")
		if after := gitIn(t, wt, "rev-parse", "HEAD"); after != before {
			t.Errorf("HEAD changed from %s to %s with no binaries", before, after)
		}
	})

	t.Run("single-commit history is skipped", func(t *testing.T) {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-b", "main")
		gitIn(t, dir, "config", "user.email", "t@t.t")
		gitIn(t, dir, "config", "user.name", "t")
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte{0, 1, 2, 0}, 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", ".")
		gitIn(t, dir, "commit", "-m", "only")
		stripBinariesFromBranch(dir, "s-single")
		if out := gitIn(t, dir, "ls-files"); out != "a.bin" {
			t.Errorf("HEAD~1 does not exist so nothing may be stripped; ls-files = %q", out)
		}
	})

	t.Run("binary in last commit is removed and ignored", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-bin")
		if err := os.WriteFile(filepath.Join(wt, "app"), []byte{0, 1, 2, 3, 0}, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, wt, "add", ".")
		gitIn(t, wt, "commit", "-m", "binary + source")
		stripBinariesFromBranch(wt, "s-bin")

		files := gitIn(t, wt, "ls-files")
		if strings.Contains(files, "app\n") || strings.HasSuffix(files, "app") {
			t.Errorf("binary still tracked: %q", files)
		}
		if !strings.Contains(files, "main.go") {
			t.Errorf("source file must survive: %q", files)
		}
		gi, err := os.ReadFile(filepath.Join(wt, ".gitignore"))
		if err != nil || !strings.Contains(string(gi), "auto-detected binaries") || !strings.Contains(string(gi), "app") {
			t.Errorf(".gitignore = %q (err=%v)", gi, err)
		}
		if n := gitIn(t, wt, "rev-list", "--count", "HEAD"); n != "3" {
			t.Errorf("amend must not add a commit; count = %s", n)
		}
	})
}

func TestSimulateDryRunChanges(t *testing.T) {
	t.Run("commits the placeholder", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-dry")
		simulateDryRunChanges(wt, "s-dry")
		if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
			t.Errorf("worktree dirty after simulation: %q", out)
		}
		if msg := gitIn(t, wt, "log", "-1", "--pretty=%s"); msg != "[dry-run] simulated changes for s-dry" {
			t.Errorf("commit subject = %q", msg)
		}
	})

	t.Run("non-repo directory logs and leaves the file", func(t *testing.T) {
		dir := t.TempDir()
		simulateDryRunChanges(dir, "s-x")
		if _, err := os.Stat(filepath.Join(dir, "dry-run-simulation.txt")); err != nil {
			t.Errorf("simulation file should still be written: %v", err)
		}
	})

	t.Run("unwritable path returns early", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		simulateDryRunChanges(dir, "s-x")
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("directory must not be created: %v", err)
		}
	})
}

// resolvedFile wraps content in the resolver's output sentinels.
func resolvedFile(content string) llm.CompletionResponse {
	return llm.CompletionResponse{Content: resolvedFileSentinelStart + "\n" + content + resolvedFileSentinelEnd + "\n"}
}

func TestPostExecutionPipeline_ConflictResolved_ThenPostRebaseQA(t *testing.T) {
	f := newPipelineFixture(t)
	// main and the branch both edit feature.txt → rebase conflict.
	if err := os.WriteFile(filepath.Join(f.repo, "feature.txt"), []byte("main line one\nmain line two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "add", ".")
	gitIn(t, f.repo, "commit", "-m", "main edits feature")
	merged := "main line one\nmain line two\nfeature\n"
	resolver := NewConflictResolver(llm.NewReplayClient(resolvedFile(merged)), "resolver-model", 2000, f.es)
	m := f.build()
	m.SetConflictResolver(resolver)
	f.run()

	f.one(state.EventStoryMerged)
	got, err := os.ReadFile(filepath.Join(f.repo, "feature.txt"))
	// extractResolvedFileContent trims the trailing newline before the sentinel.
	if err != nil || strings.TrimSpace(string(got)) != strings.TrimSpace(merged) {
		t.Errorf("main feature.txt = %q (err=%v), want the LLM resolution", got, err)
	}
	// The resolver rewrote a file, so QA ran twice: once on the agent's tree
	// and once on the rebased tree.
	if f.qaRunner.calls != 2 {
		t.Errorf("QA runner calls = %d, want 2 (pre-merge + post-rebase)", f.qaRunner.calls)
	}
	stages := f.stageResults()
	if stages["qa_post_rebase"] != "success" || stages["merge"] != "success" {
		t.Errorf("stages = %v, want qa_post_rebase and merge success", stages)
	}
	var resolvedEvents int
	for _, evt := range f.events(state.EventStoryProgress) {
		if state.DecodePayload(evt.Payload)["action"] == "conflicts_resolved" {
			resolvedEvents++
		}
	}
	if resolvedEvents != 1 {
		t.Errorf("conflicts_resolved progress events = %d, want 1", resolvedEvents)
	}
}

func TestPostExecutionPipeline_ConflictResolved_PostRebaseQAFails(t *testing.T) {
	f := newPipelineFixture(t)
	if err := os.WriteFile(filepath.Join(f.repo, "feature.txt"), []byte("main line one\nmain line two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "add", ".")
	gitIn(t, f.repo, "commit", "-m", "main edits feature")
	resolver := NewConflictResolver(llm.NewReplayClient(resolvedFile("main line one\nmain line two\nfeature\n")), "resolver-model", 2000, f.es)
	// First QA call (pre-merge) passes, the post-rebase call fails.
	runner := &sequenceRunner{results: []runResult{{output: "ok"}, {output: "--- FAIL: TestMerged", err: errors.New("exit status 1")}}}
	f.qa = NewQA(QAConfig{TestCommand: "go test ./..."}, runner, f.es, f.ps)
	m := f.build()
	m.SetConflictResolver(resolver)
	f.run()

	f.none(state.EventStoryMerged)
	reason := f.resetReason("qa")
	if !strings.Contains(reason, "after rebase with conflict resolution") || !strings.Contains(reason, "TestMerged") {
		t.Errorf("reset reason = %q", reason)
	}
	stages := f.stageResults()
	if stages["qa_post_rebase"] != "failure" || stages["merge"] != "failure" {
		t.Errorf("stages = %v, want qa_post_rebase and merge failure", stages)
	}
	if got := f.storyStatus(); got != "draft" {
		t.Errorf("story status = %q, want draft", got)
	}
}

// runResult is one scripted CommandRunner outcome.
type runResult struct {
	output string
	err    error
}

// sequenceRunner returns scripted results in order (last one repeats).
type sequenceRunner struct {
	results []runResult
	calls   int
}

func (r *sequenceRunner) Run(context.Context, string, string, ...string) (string, error) {
	i := r.calls
	if i >= len(r.results) {
		i = len(r.results) - 1
	}
	r.calls++
	return r.results[i].output, r.results[i].err
}

func TestPostExecutionPipeline_ScrubsReasoningPreamble(t *testing.T) {
	f := newPipelineFixture(t)
	src := "Looking at the code, I'll add the handler.\nHere's the file:\n\npackage main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(f.worktree, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.worktree, "add", ".")
	gitIn(t, f.worktree, "commit", "-m", "agent output with preamble")
	commitsBefore := gitIn(t, f.worktree, "rev-list", "--count", "HEAD")
	f.run()

	f.one(state.EventStoryMerged)
	got, err := os.ReadFile(filepath.Join(f.repo, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package main\n\nfunc main() {}\n" {
		t.Errorf("main.go on main = %q, want the preamble stripped", got)
	}
	// The scrub was amended into the agent's commit, not added as a new one.
	if n := gitIn(t, f.repo, "rev-list", "--count", "main^2"); n != commitsBefore {
		t.Errorf("branch commit count after amend = %s, want %s", n, commitsBefore)
	}
}

func TestPostExecutionPipeline_BudgetExceeded_StopsBeforeReview(t *testing.T) {
	f := newPipelineFixture(t)
	metricsPath := filepath.Join(t.TempDir(), "metrics.jsonl")
	writeMetrics(t, metricsPath, metrics.MetricEntry{ReqID: f.req, Model: "m1", TokensIn: 3000, TokensOut: 1000}) // $5
	m := f.build()
	m.SetBudgetGuard(NewBudgetGuard(budgetBilling(4, 80), metricsPath))
	f.run()

	exceeded := f.one(state.EventReqBudgetExceeded)
	p := state.DecodePayload(exceeded.Payload)
	if p["id"] != f.req || p["spent_usd"].(float64) != 5 || p["budget_usd"].(float64) != 4 {
		t.Errorf("budget payload = %v", p)
	}
	if got := f.reqStatus(); got != "paused" {
		t.Errorf("requirement status = %q, want paused", got)
	}
	if reason := f.pauseReason(); !strings.Contains(reason, "LLM budget exceeded: spent $5.00 of $4.00") {
		t.Errorf("pause reason = %q", reason)
	}
	f.none(state.EventStoryReviewPassed)
	f.none(state.EventStoryMerged)
}

func TestPostExecutionPipeline_PausedRequirementStillMergesButStops(t *testing.T) {
	f := newPipelineFixture(t)
	pause := state.NewEvent(state.EventReqPaused, "cli", "", map[string]any{"id": f.req, "reason": "operator pause"})
	if err := f.es.Append(pause); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.Project(pause); err != nil {
		t.Fatal(err)
	}
	f.run()

	// The finished story still lands (its work is done), but the requirement
	// stays paused so no next wave is dispatched from here.
	f.one(state.EventStoryMerged)
	if got := f.reqStatus(); got != "paused" {
		t.Errorf("requirement status = %q, want paused", got)
	}
	if n := len(f.allEvents(state.EventReqPaused)); n != 1 {
		t.Errorf("REQ_PAUSED events = %d, want the operator's single pause", n)
	}
}
