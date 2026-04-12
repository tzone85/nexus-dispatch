package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// plannerJSON is a valid planner response that the ReplayClient returns.
// Matches the format expected by engine.Planner.parsePlannerResponse.
const plannerJSON = `[
	{"id": "s-001", "title": "Setup scaffold", "description": "Create project structure", "acceptance_criteria": "Scaffold exists", "complexity": 2, "depends_on": [], "owned_files": ["src/main.go"], "wave_hint": "sequential"},
	{"id": "s-002", "title": "Add core logic", "description": "Implement business rules", "acceptance_criteria": "Logic works", "complexity": 3, "depends_on": ["s-001"], "owned_files": ["src/core.go"], "wave_hint": "parallel"}
]`

// classifyJSON is a valid requirement classification response.
const classifyJSON = `{"type": "feature", "confidence": 0.95, "reasoning": "New feature request"}`

// withMockLLM sets up a ReplayClient that returns the given responses and
// restores the original buildLLMClientFunc on cleanup.
func withMockLLM(t *testing.T, responses ...llm.CompletionResponse) {
	t.Helper()
	original := buildLLMClientFunc
	t.Cleanup(func() { buildLLMClientFunc = original })

	client := llm.NewReplayClient(responses...)
	buildLLMClientFunc = func(provider string, godmode ...bool) (llm.Client, error) {
		return client, nil
	}
}

// initTestRepo creates a minimal git repo with one commit so that
// runReq/runResume can classify it and create worktrees.
func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644)
	run("add", ".")
	run("commit", "-m", "initial")
}

// ── runReq with mock LLM ─────────────────────────────────────────────

func TestReqCmd_Greenfield(t *testing.T) {
	env := setupTestEnv(t)

	// Use a clean directory as working dir (greenfield = no go.mod).
	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	os.Chdir(workDir)
	t.Cleanup(func() { os.Chdir(orig) })

	// For a greenfield project, the planner is called once (no classify, no investigate).
	withMockLLM(t, llm.CompletionResponse{
		Content: plannerJSON,
		Model:   "gemma4:26b",
	})

	cmd := newReqCmd()
	out, err := execCmd(t, cmd, env.Config, "Build a REST API for user management")
	if err != nil {
		t.Fatalf("req: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Planning requirement") {
		t.Error("expected 'Planning requirement' header")
	}
	if !strings.Contains(out, "Plan created with") {
		t.Errorf("expected plan summary, got:\n%s", out)
	}
	if !strings.Contains(out, "Setup scaffold") {
		t.Error("expected story title 'Setup scaffold' in output")
	}
}

func TestReqCmd_ReviewMode(t *testing.T) {
	env := setupTestEnv(t)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	os.Chdir(workDir)
	t.Cleanup(func() { os.Chdir(orig) })

	withMockLLM(t, llm.CompletionResponse{
		Content: plannerJSON,
		Model:   "gemma4:26b",
	})

	cmd := newReqCmd()
	out, err := execCmd(t, cmd, env.Config, "--review", "Build auth module")
	if err != nil {
		t.Fatalf("req --review: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Plan ready for review") {
		t.Errorf("expected review mode message, got:\n%s", out)
	}
	if !strings.Contains(out, "nxd approve") {
		t.Error("expected approve hint in review mode output")
	}
}

// ── runReq --dry-run (uses DryRunClient directly, no mock override) ────

func TestReqCmd_DryRun(t *testing.T) {
	env := setupTestEnv(t)

	workDir := t.TempDir()
	initTestRepo(t, workDir)
	orig, _ := os.Getwd()
	os.Chdir(workDir)
	t.Cleanup(func() { os.Chdir(orig) })

	// --dry-run replaces the LLM client internally, no need for withMockLLM.
	cmd := newReqCmd()
	out, err := execCmd(t, cmd, env.Config, "--dry-run", "Build a REST API with CRUD endpoints")
	if err != nil {
		t.Fatalf("req --dry-run: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "[DRY RUN]") {
		t.Error("expected [DRY RUN] banner")
	}
	if !strings.Contains(out, "Plan created with") {
		t.Errorf("expected plan summary, got:\n%s", out)
	}
	if !strings.Contains(out, "scaffold") {
		t.Error("expected scaffold story from DryRunClient")
	}
}

// ── runArchive ────────────────────────────────────────────────

func TestArchiveCmd_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	// initTestRepo so cleanup doesn't fail on git ops.
	initTestRepo(t, env.Dir)
	orig, _ := os.Getwd()
	os.Chdir(env.Dir)
	t.Cleanup(func() { os.Chdir(orig) })

	cmd := newArchiveCmd()
	out, err := execCmd(t, cmd, env.Config, "req-00100")
	if err != nil {
		t.Fatalf("archive: %v\nOutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Archived requirement") {
		t.Errorf("expected archive confirmation, got:\n%s", out)
	}

	// Verify requirement is archived.
	req, _ := env.Proj.GetRequirement("req-00100")
	if req.Status != "archived" {
		t.Errorf("expected status=archived, got %q", req.Status)
	}

	// Verify stories are archived.
	stories, _ := env.Proj.ListStories(state.StoryFilter{ReqID: "req-00100"})
	for _, s := range stories {
		if s.Status != "archived" {
			t.Errorf("story %s: expected status=archived, got %q", s.ID, s.Status)
		}
	}
}

func TestArchiveCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newArchiveCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

// ── buildLLMClient providers ─────────────────────────────────────────

func TestBuildLLMClient_OllamaProvider(t *testing.T) {
	// Restore the default after test (we're testing the real function).
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	client, err := buildLLMClient("ollama")
	if err != nil {
		t.Fatalf("buildLLMClient(ollama): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for ollama")
	}
}

func TestBuildLLMClient_UnsupportedProvider(t *testing.T) {
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	_, err := buildLLMClient("nonexistent-provider")
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported' in error, got: %v", err)
	}
}

func TestBuildLLMClient_AnthropicNoKey(t *testing.T) {
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := buildLLMClient("anthropic")
	if err == nil {
		t.Error("expected error when ANTHROPIC_API_KEY is empty")
	}
}

func TestBuildLLMClient_OpenAINoKey(t *testing.T) {
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	t.Setenv("OPENAI_API_KEY", "")
	_, err := buildLLMClient("openai")
	if err == nil {
		t.Error("expected error when OPENAI_API_KEY is empty")
	}
}

func TestBuildLLMClient_GoogleNoKey(t *testing.T) {
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	t.Setenv("GOOGLE_AI_API_KEY", "")
	_, err := buildLLMClient("google")
	if err == nil {
		t.Error("expected error when GOOGLE_AI_API_KEY is empty")
	}
}

func TestBuildLLMClient_GoogleOllamaFallback(t *testing.T) {
	original := buildLLMClientFunc
	buildLLMClientFunc = buildLLMClientDefault
	t.Cleanup(func() { buildLLMClientFunc = original })

	// No Google key — should degrade to Ollama-only (not an error).
	t.Setenv("GOOGLE_AI_API_KEY", "")
	client, err := buildLLMClient("google+ollama")
	if err != nil {
		t.Fatalf("buildLLMClient(google+ollama): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// ── runWatch with context cancellation ───────────────────────────────

func TestWatchCmd_CancelledByContext(t *testing.T) {
	env := setupTestEnv(t)

	// Seed some events so watch has something to display initially.
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)

	cmd := newWatchCmd()
	if cmd.Flags().Lookup("config") == nil {
		cmd.Flags().String("config", "", "")
	}
	cmd.Flags().Set("config", env.Config)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Watching for events") {
		t.Errorf("expected watch header, got:\n%s", out)
	}
}
