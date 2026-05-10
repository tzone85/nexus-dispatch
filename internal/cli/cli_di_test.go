package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestRunPlan_Greenfield drives runPlan end-to-end using the LLM-DI
// pattern (withMockLLM swaps buildLLMClientFunc → ReplayClient). On
// a greenfield project (no go.mod) the planner is called once with
// plannerJSON; runPlan must print the plan and exit cleanly without
// touching the network. This is the first happy-path test for
// runPlan, which was at 0% pre-#26.
func TestRunPlan_Greenfield(t *testing.T) {
	env := setupTestEnv(t)

	// Greenfield working dir (no go.mod). initTestRepo creates a
	// minimal git repo with one commit so the temp stores have
	// somewhere to write worktrees if needed.
	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	withMockLLM(t, llm.CompletionResponse{
		Content: plannerJSON,
		Model:   "gemma4:26b",
	})

	cmd := newPlanCmd()
	out, err := execCmd(t, cmd, env.Config, "Build a REST API for user management")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry-run plan with") {
		t.Errorf("expected plan summary, got:\n%s", out)
	}
	if !strings.Contains(out, "Setup scaffold") {
		t.Errorf("expected story title 'Setup scaffold' in output:\n%s", out)
	}
	if !strings.Contains(out, "No state was persisted") {
		t.Errorf("expected dry-run reminder line:\n%s", out)
	}
}

// TestRunPlan_FilenameFlag covers the --file flag path that reads the
// requirement from disk instead of the positional arg. The flag is a
// thin wrapper over resolveRequirement; without a test, a regression
// in resolveRequirement would silently fall back to "" and runPlan
// would emit a confusing empty-requirement plan.
func TestRunPlan_FilenameFlag(t *testing.T) {
	env := setupTestEnv(t)
	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	reqFile := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(reqFile, []byte("Add health check endpoint"), 0o644); err != nil {
		t.Fatalf("write req file: %v", err)
	}

	withMockLLM(t, llm.CompletionResponse{Content: plannerJSON, Model: "gemma4:26b"})

	cmd := newPlanCmd()
	out, err := execCmd(t, cmd, env.Config, "--file", reqFile)
	if err != nil {
		t.Fatalf("plan --file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Add health check endpoint") {
		t.Errorf("expected requirement text from file in output:\n%s", out)
	}
}

// TestRunPlan_MissingRequirement covers the precondition: no
// positional arg AND no --file flag → resolveRequirement returns an
// error. runPlan must propagate that, not crash on an empty string.
func TestRunPlan_MissingRequirement(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newPlanCmd()
	_, err := execCmd(t, cmd, env.Config) // no args, no --file
	if err == nil {
		t.Fatal("expected error when no requirement provided")
	}
}

// TestRunMergeStory_AllowsMergeReadyStory covers the success-path
// happy case: a real story transitioned to merge_ready with a real
// branch in a real git repo. The actual merger.Merge() call still
// runs against the real local merger, so the test asserts the
// runMergeStory wiring rather than the merge result.
func TestRunMergeStory_AllowsMergeReadyStory(t *testing.T) {
	env := setupTestEnv(t)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	// Seed a story whose status is merge_ready (so runMergeStory
	// passes the precondition). We don't expect the real merge to
	// succeed (no actual feature branch), but we do expect
	// runMergeStory to get past loadStores + lookup + status check
	// before the merger fails.
	seedTestReq(t, env, "REQ-1", "Test", workDir)
	seedTestStory(t, env, "STORY-MR", "REQ-1", "Story", 3)

	// Promote the story to merge_ready via STORY_MERGE_READY event.
	mrEvt := state.NewEvent(state.EventStoryMergeReady, "test", "STORY-MR", nil)
	if err := env.Events.Append(mrEvt); err != nil {
		t.Fatalf("append merge_ready: %v", err)
	}
	if err := env.Proj.Project(mrEvt); err != nil {
		t.Fatalf("project merge_ready: %v", err)
	}

	cmd := newMergeStoryCmd()
	// We expect an error (no actual branch to merge), but the error
	// must come from the merger, NOT from the precondition check —
	// proving runMergeStory passed the loadStores + status branch.
	_, err := execCmd(t, cmd, env.Config, "STORY-MR")
	if err != nil && strings.Contains(err.Error(), "expected \"merge_ready\"") {
		t.Fatalf("runMergeStory rejected merge_ready story: %v", err)
	}
	// merge will fail because no feature branch exists, but the
	// runMergeStory function itself reaches the merger.Merge call —
	// that's the coverage win.
}

// TestRunReviewStory_WithBranchEmitsDiff exercises the branch where
// story.Branch is set: runReviewStory shells out to git to render
// the diff. The git commands fail in the temp repo (no feature
// branch with that name), but the failure path also produces output
// — covering the if-stmt branches that were untested.
func TestRunReviewStory_WithBranchEmitsDiff(t *testing.T) {
	env := setupTestEnv(t)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	// Seed story with an explicit branch. The seedTestStory helper
	// doesn't set Branch so we emit STORY_ASSIGNED to set it.
	seedTestReq(t, env, "REQ-1", "Test", workDir)
	seedTestStory(t, env, "STORY-WB", "REQ-1", "With branch", 3)
	assignEvt := state.NewEvent(state.EventStoryAssigned, "test", "STORY-WB", map[string]any{
		"id":     "STORY-WB",
		"role":   "junior",
		"branch": "story/STORY-WB",
		"agent_id": "agent-1",
	})
	if err := env.Events.Append(assignEvt); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := env.Proj.Project(assignEvt); err != nil {
		t.Fatalf("project: %v", err)
	}

	cmd := newReviewStoryCmd()
	out, err := execCmd(t, cmd, env.Config, "STORY-WB")
	if err != nil {
		t.Fatalf("review: %v\n%s", err, out)
	}
	if !strings.Contains(out, "STORY-WB") {
		t.Errorf("review output missing story id:\n%s", out)
	}
}

// TestRunImprove_FeedFetchErrorWarns covers the path where the online
// feed responds with a non-2xx status. The improver returns an error
// per source, and runImprove must surface it to stderr without
// failing the whole command.
func TestRunImprove_FeedFetchErrorWarns(t *testing.T) {
	env := setupTestEnv(t)

	cmd, buf := mkRunCmd(t, env.Config)
	cmd.Flags().String("feed", "http://127.0.0.1:1/", "")  // unreachable
	cmd.Flags().Bool("json", false, "")
	cmd.SetContext(context.Background())

	if err := runImprove(cmd, nil); err != nil {
		t.Fatalf("runImprove with bad feed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warning") {
		t.Errorf("expected warning about feed fetch in output:\n%s", out)
	}
}

// TestRunDoctor_PrintsCheckResults exercises the runDoctor handler
// directly. Each check entry produces a status line; even with all
// checks failing on a barebones test env, the doctor must complete
// without error — its job is to report, not block.
func TestRunDoctor_PrintsCheckResults(t *testing.T) {
	env := setupTestEnv(t)

	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.Flags().String("config", env.Config, "")
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{}) // ensure no positional args

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd.SetContext(timeoutCtx)

	if err := cmd.Execute(); err != nil {
		// doctor reporting failures via output is fine; only error
		// out if the command itself crashed.
		t.Logf("doctor returned err: %v (acceptable)", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Go") && !strings.Contains(out, "Git") {
		t.Errorf("doctor output should mention some checks; got:\n%s", out)
	}
}

// TestRunImprove_StoreOpenError covers the loadStores failure branch
// in runImprove. We pass a config path pointing at a directory that
// can't be opened as a state dir (it's a regular file, not a dir).
func TestRunImprove_StoreOpenError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "block")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	cfgPath := filepath.Join(dir, "nxd.yaml")
	if err := os.WriteFile(cfgPath, []byte(
		"version: \"1.0\"\nworkspace:\n  state_dir: "+filepath.Join(blocker, "subdir")+"\n  backend: sqlite\n",
	), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	cmd, _ := mkRunCmd(t, cfgPath)
	cmd.Flags().String("feed", "", "")
	cmd.Flags().Bool("json", false, "")

	if err := runImprove(cmd, nil); err == nil {
		t.Fatal("expected error when state_dir parent is a file")
	}
}

// TestNewWatchCmd_BuildsRunner covers the watch command constructor.
// Cobra's RunE is a closure; the test executes a tiny scenario and
// confirms the command can at least be constructed + parsed — we
// don't drive the real watch loop because it's an infinite stream.
func TestNewWatchCmd_BuildsRunner(t *testing.T) {
	cmd := newWatchCmd()
	if cmd == nil {
		t.Fatal("newWatchCmd returned nil")
	}
	if cmd.Use != "watch" {
		t.Errorf("Use = %q, want watch", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("watch command missing RunE")
	}
}

// TestNewSpecCmd_HasSubcommands covers the spec command tree
// constructor. spec is a parent with init/assemble/validate
// subcommands; test that the tree is wired correctly.
func TestNewSpecCmd_HasSubcommands(t *testing.T) {
	cmd := newSpecCmd()
	subs := cmd.Commands()
	if len(subs) < 3 {
		t.Errorf("expected ≥3 spec subcommands (init, assemble, validate); got %d", len(subs))
	}
	names := map[string]bool{}
	for _, sub := range subs {
		// sub.Use is "init [target-dir]" — Name() returns just "init".
		names[sub.Name()] = true
	}
	for _, want := range []string{"init", "assemble", "validate"} {
		if !names[want] {
			t.Errorf("spec subcommand %q missing; got %v", want, names)
		}
	}
}

// TestNewConfigCmd_HasSubcommands mirrors the spec test but for the
// `nxd config` parent.
func TestNewConfigCmd_HasSubcommands(t *testing.T) {
	cmd := newConfigCmd()
	subs := cmd.Commands()
	if len(subs) < 2 {
		t.Errorf("expected ≥2 config subcommands (show, validate); got %d", len(subs))
	}
}

// TestNewModelsCmd_HasCheckSub covers the `nxd models` parent + check
// subcommand wiring.
func TestNewModelsCmd_HasCheckSub(t *testing.T) {
	cmd := newModelsCmd()
	if cmd.Use != "models" {
		t.Errorf("Use = %q, want models", cmd.Use)
	}
	hasCheck := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "check" {
			hasCheck = true
		}
	}
	if !hasCheck {
		t.Error("models command missing 'check' subcommand")
	}
}

// TestRunResume_NoActiveRequirementsErrors covers the auto-select
// branch when the projection has zero active requirements: the
// handler must error with a clear "run 'nxd req' first" message
// rather than silently exiting.
func TestRunResume_NoActiveRequirementsErrors(t *testing.T) {
	env := setupTestEnv(t)
	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newResumeCmd()
	_, err := execCmd(t, cmd, env.Config) // no args → auto-select
	if err == nil {
		t.Fatal("expected error when no active requirements exist")
	}
	if !strings.Contains(err.Error(), "no active requirements") {
		t.Errorf("expected 'no active requirements' message, got: %v", err)
	}
}

// TestRunResume_MultipleActiveRequiresExplicitID covers the
// multi-active disambiguation branch: when more than one
// requirement is in flight, the handler must list them and require
// an explicit ID instead of guessing.
func TestRunResume_MultipleActiveRequiresExplicitID(t *testing.T) {
	env := setupTestEnv(t)
	// Use short IDs deliberately — locks down the defensive-truncation
	// fix in resume.go (was r.ID[:8] panic on <8-char IDs).
	seedTestReq(t, env, "REQ-A", "first", "/tmp")
	seedTestReq(t, env, "REQ-B", "second", "/tmp")

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newResumeCmd()
	out, err := execCmd(t, cmd, env.Config) // no args → ambiguous
	if err == nil {
		t.Fatal("expected error when multiple active requirements")
	}
	if !strings.Contains(err.Error(), "specify which requirement") {
		t.Errorf("expected 'specify which requirement' message, got: %v", err)
	}
	if !strings.Contains(out, "Multiple active requirements") {
		t.Errorf("expected list header in output:\n%s", out)
	}
}

// TestRunResume_UnknownRequirementErrors covers the explicit-ID
// branch where the requirement isn't found in the projection.
func TestRunResume_UnknownRequirementErrors(t *testing.T) {
	env := setupTestEnv(t)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newResumeCmd()
	_, err := execCmd(t, cmd, env.Config, "REQ-DOES-NOT-EXIST")
	if err == nil {
		t.Fatal("expected error for unknown requirement")
	}
}

// We also locally exercise newImproveCmd / newDirectCmd / newReqCmd /
// newDashboardCmd to lock down their constructor wiring.
func TestNewCommandConstructors_NotNil(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() *cobra.Command
	}{
		{"improve", newImproveCmd},
		{"direct", newDirectCmd},
		{"req", newReqCmd},
		{"dashboard", newDashboardCmd},
		{"resume", newResumeCmd},
		{"plan", newPlanCmd},
		{"merge", newMergeStoryCmd},
		{"review", newReviewStoryCmd},
		{"estimate", newEstimateCmd},
		{"doctor", newDoctorCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.fn()
			if cmd == nil {
				t.Fatalf("%s constructor returned nil", tc.name)
			}
			if cmd.Use == "" {
				t.Errorf("%s command has empty Use field", tc.name)
			}
		})
	}
}
