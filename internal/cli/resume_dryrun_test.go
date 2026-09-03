package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// resumeEnv is a testEnv whose config keeps the pipeline fully local and
// fast: host sandbox, 20ms monitor polling, no dangling-branch cleanup and
// no factory stories. The requirement is bound to workDir (a real git repo)
// and the test's working directory is switched there.
func newResumeEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	cfg := "version: \"1.0\"\n" +
		"workspace:\n  state_dir: " + stateDir + "\n" +
		"merge:\n  mode: local\n" +
		"monitor:\n  poll_interval_ms: 20\n  pipeline_timeout_s: 120\n" +
		"sandbox:\n  mode: host\n" +
		"qa:\n  success_criteria: []\n" +
		"cleanup:\n  delete_dangling_branches: false\n" +
		"planning:\n  emit_integration_story: false\n  emit_scribe_story: false\n"
	if err := os.WriteFile(env.Config, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return env, workDir
}

func seedProjected(t *testing.T, env *testEnv, evt state.Event) {
	t.Helper()
	if err := env.Events.Append(evt); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(evt); err != nil {
		t.Fatal(err)
	}
}

func countEvents(t *testing.T, env *testEnv, typ state.EventType) int {
	t.Helper()
	n, err := env.Events.Count(state.EventFilter{Type: typ})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRunResume_DryRunCompletesRequirement drives `nxd resume --dry-run`
// through the whole pipeline: a native (in-process) agent fed by the dry-run
// LLM, the simulated diff, review, QA, local merge, auto-resume and the
// requirement completion summary.
func TestRunResume_DryRunCompletesRequirement(t *testing.T) {
	env, workDir := newResumeEnv(t)
	seedTestReq(t, env, "REQ-DRY", "Dry pipeline", workDir)
	seedTestStory(t, env, "s-dry", "REQ-DRY", "Only story", 1)
	// The requirement was paused by an operator: resume must unpause first.
	seedProjected(t, env, state.NewEvent(state.EventReqPaused, "cli", "", map[string]any{"id": "REQ-DRY", "reason": "operator"}))
	env.Proj.Close() // the command opens its own handles
	env.Events.Close()

	out, err := execCmd(t, newResumeCmd(), env.Config, "REQ-DRY", "--dry-run")
	if err != nil {
		t.Fatalf("resume --dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Unpaused requirement: Dry pipeline",
		"Resuming requirement: Dry pipeline (paused)",
		"Stories: 1 total, 0 completed",
		"Wave: dispatching 1 stories",
		"[DRY RUN] Using simulated LLM responses",
		"s-dry -> gemma",
		"1 agents working. Monitoring progress...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	es, err := state.NewFileStore(filepath.Join(env.Dir, ".nxd", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	reopened := &testEnv{Events: es}
	for _, typ := range []state.EventType{
		state.EventReqResumed, state.EventStoryStarted, state.EventStoryCompleted,
		state.EventStoryReviewPassed, state.EventStoryQAPassed, state.EventStoryMerged, state.EventReqCompleted,
	} {
		if n := countEvents(t, reopened, typ); n != 1 {
			t.Errorf("%s events = %d, want 1", typ, n)
		}
	}
	started, _ := es.List(state.EventFilter{Type: state.EventStoryStarted})
	merged, _ := es.List(state.EventFilter{Type: state.EventStoryMerged})
	if len(started) == 1 && started[0].AttemptID == "" {
		t.Error("STORY_STARTED must carry the attempt id")
	}
	if len(merged) == 1 && merged[0].StoryID != "s-dry" {
		t.Errorf("merged story = %q", merged[0].StoryID)
	}
	// The simulated change landed on the base branch and the worktree is gone.
	if _, err := os.Stat(filepath.Join(workDir, "dry-run-simulation.txt")); err != nil {
		t.Errorf("dry-run simulation file not merged into the repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".nxd", "worktrees", "s-dry")); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed after merge (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(env.Dir, ".nxd", "bayesian_priors.json")); err != nil {
		t.Errorf("bayesian priors not saved: %v", err)
	}
	if !strings.Contains(out, "1 stories merged locally") || !strings.Contains(out, "Wave 1  Only story") || !strings.Contains(out, "The requirement is complete.") {
		t.Errorf("completion summary missing from output:\n%s", out)
	}
}

func TestRunResume_EarlyExits(t *testing.T) {
	t.Run("no stories", func(t *testing.T) {
		env, workDir := newResumeEnv(t)
		seedTestReq(t, env, "REQ-EMPTY", "Empty", workDir)
		out, err := execCmd(t, newResumeCmd(), env.Config, "REQ-EMPTY")
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if !strings.Contains(out, "No stories found for this requirement.") {
			t.Errorf("output = %s", out)
		}
	})

	t.Run("all stories complete", func(t *testing.T) {
		env, workDir := newResumeEnv(t)
		seedTestReq(t, env, "REQ-DONE", "Done", workDir)
		seedTestStory(t, env, "s-done", "REQ-DONE", "Merged story", 1)
		seedProjected(t, env, state.NewEvent(state.EventStoryMerged, "merger", "s-done", map[string]any{"pr_number": 1}))
		out, err := execCmd(t, newResumeCmd(), env.Config, "REQ-DONE")
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if !strings.Contains(out, "Stories: 1 total, 1 completed") || !strings.Contains(out, "All stories are complete.") {
			t.Errorf("output = %s", out)
		}
		if n := countEvents(t, env, state.EventStoryAssigned); n != 0 {
			t.Errorf("nothing may be dispatched when everything is merged, got %d STORY_ASSIGNED", n)
		}
	})

	t.Run("nothing dispatchable", func(t *testing.T) {
		env, workDir := newResumeEnv(t)
		seedTestReq(t, env, "REQ-BLOCKED", "Blocked", workDir)
		seedTestStory(t, env, "s-parent", "REQ-BLOCKED", "Parent", 1)
		// The child depends on a story that is not part of this requirement
		// (never completed here), so nothing is ready; the parent is merged.
		seedProjected(t, env, state.NewEvent(state.EventStoryCreated, "system", "s-child", map[string]any{
			"id": "s-child", "req_id": "REQ-BLOCKED", "title": "Child", "description": "d", "complexity": 1,
			"depends_on": []string{"s-external"},
		}))
		seedProjected(t, env, state.NewEvent(state.EventStoryMerged, "merger", "s-parent", map[string]any{"pr_number": 1}))
		out, err := execCmd(t, newResumeCmd(), env.Config, "REQ-BLOCKED")
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if !strings.Contains(out, "Stories: 2 total, 1 completed") || !strings.Contains(out, "No stories ready for dispatch") {
			t.Errorf("output = %s", out)
		}
		if n := countEvents(t, env, state.EventStoryAssigned); n != 0 {
			t.Errorf("STORY_ASSIGNED = %d, want 0", n)
		}
	})

	t.Run("auto-selects the single active requirement", func(t *testing.T) {
		env, workDir := newResumeEnv(t)
		seedTestReq(t, env, "REQ-ONLY", "The only one", workDir)
		out, err := execCmd(t, newResumeCmd(), env.Config)
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if !strings.Contains(out, "Auto-selected requirement: The only one") {
			t.Errorf("output = %s", out)
		}
	})
}

// TestRunResume_RealPipelineWithStubbedLLM runs `nxd resume` without
// --dry-run: every LLM client (native agent, reviewer, security gate, doc
// generator, completion gate) is the dry-run stub, so the full production
// wiring — security gate, budget guard, notifications, completion gate and
// documentation — executes against a real repo and event log.
func TestRunResume_RealPipelineWithStubbedLLM(t *testing.T) {
	env, workDir := newResumeEnv(t)
	extra := "billing:\n  budget_usd: 25\nnotifications:\n  enabled: true\n  desktop: true\n"
	existing, _ := os.ReadFile(env.Config)
	if err := os.WriteFile(env.Config, append(existing, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	original := buildLLMClientFunc
	t.Cleanup(func() { buildLLMClientFunc = original })
	stub := &agentStubClient{fallback: llm.NewDryRunClient(0)}
	buildLLMClientFunc = func(llmBuildOpts, ...bool) (llm.Client, error) { return stub, nil }

	seedTestReq(t, env, "REQ-REAL", "Real pipeline", workDir)
	seedTestStory(t, env, "s-real", "REQ-REAL", "Real story", 2)
	env.Proj.Close()
	env.Events.Close()

	out, err := execCmd(t, newResumeCmd(), env.Config, "REQ-REAL", "--godmode")
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	if strings.Contains(out, "[DRY RUN]") {
		t.Errorf("dry-run banner must not appear without --dry-run:\n%s", out)
	}
	es, err := state.NewFileStore(filepath.Join(env.Dir, ".nxd", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	reopened := &testEnv{Events: es}
	for _, typ := range []state.EventType{
		state.EventStoryStarted, state.EventStoryCompleted, state.EventStoryReviewPassed,
		state.EventStoryQAPassed, state.EventStorySecurityPassed, state.EventStoryMerged, state.EventReqCompleted,
	} {
		if n := countEvents(t, reopened, typ); n != 1 {
			t.Errorf("%s events = %d, want 1", typ, n)
		}
	}
	if n := countEvents(t, reopened, state.EventReqBlocked); n != 0 {
		t.Errorf("REQ_BLOCKED = %d, want 0 (completion gate must pass on a clean tree)", n)
	}
	if st, err := reopenedStory(t, env.Dir, "s-real"); err != nil || st.Status != "merged" {
		t.Errorf("story status = %q (err=%v), want merged", st.Status, err)
	}
	// The doc generator committed a README to the repo.
	readme, err := os.ReadFile(filepath.Join(workDir, "README.md"))
	if err != nil || !strings.Contains(string(readme), "NXD") {
		t.Errorf("README.md = %q (err=%v), want the generated doc with NXD footer", readme, err)
	}
	if stub.agentCalls != 2 {
		t.Errorf("agent loop calls = %d, want 2 (write_file, then task_complete)", stub.agentCalls)
	}
	if _, err := os.Stat(filepath.Join(workDir, "feature.txt")); err != nil {
		t.Errorf("agent's file not merged into the repo: %v", err)
	}
}

// agentStubClient plays the native agent when the request carries the
// agent tool set (write one file, then task_complete) and otherwise defers to
// the dry-run stub (reviewer, security review, docs, gates).
type agentStubClient struct {
	fallback   llm.Client
	agentCalls int
}

func (c *agentStubClient) Complete(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	isAgent := false
	for _, tool := range req.Tools {
		if tool.Name == "task_complete" {
			isAgent = true
		}
	}
	if !isAgent {
		return c.fallback.Complete(ctx, req)
	}
	c.agentCalls++
	if c.agentCalls == 1 {
		return llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
			ID: "call-1", Name: "write_file",
			Arguments: json.RawMessage(`{"path": "feature.txt", "content": "feature\n"}`),
		}}}, nil
	}
	return llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
		ID: "call-2", Name: "task_complete",
		Arguments: json.RawMessage(`{"summary": "wrote feature.txt"}`),
	}}}, nil
}

// reopenedStory reads a story from the on-disk projection after the command
// closed its own handles.
func reopenedStory(t *testing.T, dir, id string) (state.Story, error) {
	t.Helper()
	ps, err := state.NewSQLiteStore(filepath.Join(dir, ".nxd", "nxd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	return ps.GetStory(id)
}
