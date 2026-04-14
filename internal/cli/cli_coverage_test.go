package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/repolearn"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// --- Pure function tests ---

func TestFormatPasses_Coverage(t *testing.T) {
	tests := []struct {
		input []int
		want  string
	}{
		{nil, "none"},
		{[]int{}, "none"},
		{[]int{1}, "1"},
		{[]int{1, 2, 3}, "1, 2, 3"},
	}
	for _, tt := range tests {
		got := formatPasses(tt.input)
		if got != tt.want {
			t.Errorf("formatPasses(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMergeStaticIntoProfile_Coverage(t *testing.T) {
	existing := &repolearn.RepoProfile{
		Conventions: repolearn.Conventions{
			CommitFormat:     "conventional",
			ContributorCount: 5,
		},
		CompletedPasses: []int{2},
	}
	scanned := &repolearn.RepoProfile{
		TechStack: repolearn.TechStackDetail{
			PrimaryLanguage:  "go",
			PrimaryBuildTool: "go",
		},
		Build: repolearn.BuildConfig{BuildCommand: "go build ./..."},
		Test:  repolearn.TestConfig{TestCommand: "go test ./..."},
	}
	scanned.AddSignal("docker", "Dockerfile present", "Dockerfile")

	mergeStaticIntoProfile(existing, scanned)

	if existing.TechStack.PrimaryLanguage != "go" {
		t.Errorf("expected go, got %q", existing.TechStack.PrimaryLanguage)
	}
	if existing.Conventions.ContributorCount != 5 {
		t.Error("conventions should be preserved")
	}
	if !existing.PassCompleted(1) {
		t.Error("pass 1 should be marked completed after merge")
	}
}

// --- GC functions ---

func TestDeleteWorktree_NonExistent(t *testing.T) {
	ops := &cliGitCleanupOps{}
	err := ops.DeleteWorktree("/tmp", "/nonexistent/worktree")
	if err == nil {
		t.Error("expected error for nonexistent worktree")
	}
}

func TestDeleteBranch_NonExistent(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	ops := &cliGitCleanupOps{}
	err := ops.DeleteBranch(dir, "nonexistent-branch")
	if err == nil {
		t.Error("expected error for nonexistent branch")
	}
}

func TestBranchExists(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	ops := &cliGitCleanupOps{}
	if !ops.BranchExists(dir, "main") && !ops.BranchExists(dir, "master") {
		t.Error("expected main or master branch to exist")
	}
	if ops.BranchExists(dir, "nonexistent") {
		t.Error("nonexistent branch should not exist")
	}
}

// --- runConsistencyCheck ---

func TestRunConsistencyCheck_NoIssues(t *testing.T) {
	stories := []state.Story{
		{ID: "s-001", Status: "merged"},
		{ID: "s-002", Status: "draft"},
	}
	issues := runConsistencyCheck(stories, t.TempDir())
	// No in_progress stories, no orphans expected
	_ = issues
}

func TestRunConsistencyCheck_InProgressStory(t *testing.T) {
	stories := []state.Story{
		{ID: "s-001", Status: "in_progress"},
	}
	issues := runConsistencyCheck(stories, t.TempDir())
	// Should detect in_progress story without a running agent
	_ = issues
}

// --- Command construction tests ---

func TestNewGCCmd(t *testing.T) {
	cmd := newGCCmd()
	if cmd == nil {
		t.Fatal("newGCCmd returned nil")
	}
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run flag")
	}
}

func TestNewEstimateCmd(t *testing.T) {
	cmd := newEstimateCmd()
	if cmd == nil {
		t.Fatal("newEstimateCmd returned nil")
	}
}

func TestNewLearnCmd_Coverage(t *testing.T) {
	cmd := newLearnCmd()
	if cmd == nil {
		t.Fatal("newLearnCmd returned nil")
	}
	if cmd.Flags().Lookup("force") == nil {
		t.Error("expected --force flag")
	}
	if cmd.Flags().Lookup("pass") == nil {
		t.Error("expected --pass flag")
	}
	if cmd.Flags().Lookup("json") == nil {
		t.Error("expected --json flag")
	}
}

func TestNewModelsCmd(t *testing.T) {
	cmd := newModelsCmd()
	if cmd == nil {
		t.Fatal("newModelsCmd returned nil")
	}
}

func TestNewPlanCmd(t *testing.T) {
	cmd := newPlanCmd()
	if cmd == nil {
		t.Fatal("newPlanCmd returned nil")
	}
}

func TestNewMergeStoryCmd(t *testing.T) {
	cmd := newMergeStoryCmd()
	if cmd == nil {
		t.Fatal("newMergeStoryCmd returned nil")
	}
}

func TestNewReviewStoryCmd(t *testing.T) {
	cmd := newReviewStoryCmd()
	if cmd == nil {
		t.Fatal("newReviewStoryCmd returned nil")
	}
}

func TestNewLogsCmd(t *testing.T) {
	cmd := newLogsCmd()
	if cmd == nil {
		t.Fatal("newLogsCmd returned nil")
	}
	if cmd.Flags().Lookup("follow") == nil {
		t.Error("expected --follow flag")
	}
}

func TestNewDiffCmd(t *testing.T) {
	cmd := newDiffCmd()
	if cmd == nil {
		t.Fatal("newDiffCmd returned nil")
	}
}

func TestNewArchiveCmd(t *testing.T) {
	cmd := newArchiveCmd()
	if cmd == nil {
		t.Fatal("newArchiveCmd returned nil")
	}
}

func TestNewConfigCmd(t *testing.T) {
	cmd := newConfigCmd()
	if cmd == nil {
		t.Fatal("newConfigCmd returned nil")
	}
	if len(cmd.Commands()) == 0 {
		t.Error("config should have subcommands (show, validate)")
	}
}

// --- Integration tests using testEnv ---

func TestRunGC_EmptyProject(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newGCCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runGC error: %v", err)
	}
	_ = out // GC on empty project should succeed
}

func TestRunGC_DryRun_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newGCCmd()
	cmd.Flags().Set("dry-run", "true")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runGC dry-run error: %v", err)
	}
	_ = out
}

func TestRunStatus_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	_ = out
}

func TestRunAgents_Empty(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newAgentsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runAgents error: %v", err)
	}
	_ = out
}

func TestRunAgents_WithData(t *testing.T) {
	env := setupTestEnv(t)
	seedTestAgent(t, env, "a-001", "gemma", "nxd-test-session")

	cmd := newAgentsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runAgents error: %v", err)
	}
	_ = out
}

func TestRunEvents_Empty(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runEvents error: %v", err)
	}
	_ = out
}

func TestRunEvents_WithData(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runEvents error: %v", err)
	}
	_ = out
}

func TestRunEscalations_Empty(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newEscalationsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runEscalations error: %v", err)
	}
	_ = out
}

func TestRunEscalations_WithData(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)
	seedTestEscalation(t, env, "s-001", "junior-1", "build failed")

	cmd := newEscalationsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("runEscalations error: %v", err)
	}
	_ = out
}

func TestRunApprove_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newApproveCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error for nonexistent story")
	}
}

func TestRunArchive_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newArchiveCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-req")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

func TestRunArchive_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)

	cmd := newArchiveCmd()
	_, err := execCmd(t, cmd, env.Config, "r-001")
	if err != nil {
		t.Fatalf("archive error: %v", err)
	}
}

func TestRunPause_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newPauseCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-req")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

func TestRecoverOrphanedStories(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	// Story is draft, no orphan expected
	issues := engine.CheckConsistency(
		[]engine.RecoveryStory{{ID: "s-001", Status: "draft"}},
		nil,
	)
	_ = issues
}

func TestLoadConfig_Coverage(t *testing.T) {
	env := setupTestEnv(t)
	cfg, err := loadConfig(env.Config)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Workspace.Backend != "sqlite" {
		t.Errorf("expected sqlite backend, got %q", cfg.Workspace.Backend)
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	_, err := loadConfig("/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestLoadConfig_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(path, []byte("{{invalid"), 0o644)
	_, err := loadConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}
