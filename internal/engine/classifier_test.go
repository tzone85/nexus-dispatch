package engine_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// initGitRepo creates a git repository in dir with the given number of commits.
// Each commit creates a unique file so the commit count is verifiable.
func initGitRepo(t *testing.T, dir string, commits int) {
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
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	for i := 0; i < commits; i++ {
		name := fmt.Sprintf("commit_%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("content %d", i)), 0644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", fmt.Sprintf("commit %d", i))
	}
}

// --- ClassifyRepo tests ---

func TestClassifyRepo_ExistingProject(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 15)

	// Create go.mod for language detection
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create source files (more than 5 to qualify as "existing")
	srcDir := filepath.Join(dir, "cmd")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("file%d.go", i))
		if err := os.WriteFile(name, []byte("package cmd"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create test files
	for i := 0; i < 3; i++ {
		name := filepath.Join(srcDir, fmt.Sprintf("file%d_test.go", i))
		if err := os.WriteFile(name, []byte("package cmd"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create CI and Docker
	ghDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(ghDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghDir, "ci.yml"), []byte("on: push"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch"), 0644); err != nil {
		t.Fatal(err)
	}

	profile := engine.ClassifyRepo(dir)

	if !profile.IsExisting {
		t.Fatal("expected IsExisting=true for project with >5 source files and >10 commits")
	}
	if profile.Language != "go" {
		t.Fatalf("expected language 'go', got %q", profile.Language)
	}
	if profile.BuildTool != "go" {
		t.Fatalf("expected build tool 'go', got %q", profile.BuildTool)
	}
	if profile.SourceFileCount < 8 {
		t.Fatalf("expected >=8 source files, got %d", profile.SourceFileCount)
	}
	if profile.TestFileCount < 3 {
		t.Fatalf("expected >=3 test files, got %d", profile.TestFileCount)
	}
	if profile.CommitCount < 15 {
		t.Fatalf("expected >=15 commits, got %d", profile.CommitCount)
	}
	if !profile.HasCI {
		t.Fatal("expected HasCI=true")
	}
	if !profile.HasDocker {
		t.Fatal("expected HasDocker=true")
	}
	if !profile.TestsExist {
		t.Fatal("expected TestsExist=true")
	}
}

func TestClassifyRepo_GreenFieldProject(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 2)

	// Only 2 source files and 2 commits -> not "existing"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("console.log('hello')"), 0644); err != nil {
		t.Fatal(err)
	}

	profile := engine.ClassifyRepo(dir)

	if profile.IsExisting {
		t.Fatal("expected IsExisting=false for greenfield project")
	}
	if profile.Language != "javascript" {
		t.Fatalf("expected language 'javascript', got %q", profile.Language)
	}
	if profile.CommitCount < 2 {
		t.Fatalf("expected >=2 commits, got %d", profile.CommitCount)
	}
	if profile.HasCI {
		t.Fatal("expected HasCI=false")
	}
	if profile.HasDocker {
		t.Fatal("expected HasDocker=false")
	}
}

func TestClassifyRepo_TopDirs(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 1)

	for _, d := range []string{"src", "internal", "pkg", "cmd"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}

	profile := engine.ClassifyRepo(dir)

	if len(profile.TopDirs) == 0 {
		t.Fatal("expected at least one top-level directory")
	}
	// .git should be excluded
	for _, d := range profile.TopDirs {
		if d == ".git" {
			t.Fatal("TopDirs should not include .git")
		}
	}
}

func TestClassifyRepo_GitLabCI(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 1)

	if err := os.WriteFile(filepath.Join(dir, ".gitlab-ci.yml"), []byte("stages: [build]"), 0644); err != nil {
		t.Fatal(err)
	}

	profile := engine.ClassifyRepo(dir)
	if !profile.HasCI {
		t.Fatal("expected HasCI=true for .gitlab-ci.yml")
	}
}

func TestClassifyRepo_DockerCompose(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 1)

	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("version: '3'"), 0644); err != nil {
		t.Fatal(err)
	}

	profile := engine.ClassifyRepo(dir)
	if !profile.HasDocker {
		t.Fatal("expected HasDocker=true for docker-compose.yml")
	}
}

func TestClassifyRepo_PythonProject(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir, 1)

	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[tool.poetry]"), 0644); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.py"), []byte("print('hi')"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "test_main.py"), []byte("def test_main(): pass"), 0644); err != nil {
		t.Fatal(err)
	}

	profile := engine.ClassifyRepo(dir)
	if profile.Language != "python" {
		t.Fatalf("expected language 'python', got %q", profile.Language)
	}
	if profile.SourceFileCount < 1 {
		t.Fatalf("expected >=1 source file, got %d", profile.SourceFileCount)
	}
	if profile.TestFileCount < 1 {
		t.Fatalf("expected >=1 test file, got %d", profile.TestFileCount)
	}
}

func TestClassifyRepo_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	// No git init — should not panic, just return zero commits
	profile := engine.ClassifyRepo(dir)
	if profile.CommitCount != 0 {
		t.Fatalf("expected 0 commits for non-git dir, got %d", profile.CommitCount)
	}
	if profile.IsExisting {
		t.Fatal("expected IsExisting=false for non-git dir")
	}
}

// --- ClassifyRequirement tests ---

func TestClassifyRequirement_Feature(t *testing.T) {
	response := `{
		"type": "feature",
		"confidence": 0.9,
		"signals": ["new endpoint", "add functionality"]
	}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{
		Language:  "go",
		BuildTool: "go",
	}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Add a new user registration endpoint", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "feature" {
		t.Fatalf("expected type 'feature', got %q", result.Type)
	}
	if result.Confidence < 0.8 {
		t.Fatalf("expected confidence >= 0.8, got %f", result.Confidence)
	}
	if len(result.Signals) != 2 {
		t.Fatalf("expected 2 signals, got %d", len(result.Signals))
	}
}

func TestClassifyRequirement_Bugfix(t *testing.T) {
	response := `{
		"type": "bugfix",
		"confidence": 0.85,
		"signals": ["error handling", "crash fix"]
	}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Fix the null pointer crash in user service", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "bugfix" {
		t.Fatalf("expected type 'bugfix', got %q", result.Type)
	}
}

func TestClassifyRequirement_Refactor(t *testing.T) {
	response := `{
		"type": "refactor",
		"confidence": 0.75,
		"signals": ["code cleanup", "restructure"]
	}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Refactor the database layer to use repository pattern", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "refactor" {
		t.Fatalf("expected type 'refactor', got %q", result.Type)
	}
}

func TestClassifyRequirement_Infrastructure(t *testing.T) {
	response := `{
		"type": "infrastructure",
		"confidence": 0.88,
		"signals": ["CI/CD", "deployment pipeline"]
	}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Set up GitHub Actions CI/CD pipeline", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "infrastructure" {
		t.Fatalf("expected type 'infrastructure', got %q", result.Type)
	}
}

func TestClassifyRequirement_LLMError_DefaultsToFeature(t *testing.T) {
	client := llm.NewErrorClient(fmt.Errorf("api down"))

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Do something", profile)
	if err != nil {
		t.Fatalf("expected no error on fallback, got: %v", err)
	}

	if result.Type != "feature" {
		t.Fatalf("expected fallback type 'feature', got %q", result.Type)
	}
	if result.Confidence != 0.5 {
		t.Fatalf("expected fallback confidence 0.5, got %f", result.Confidence)
	}
}

func TestClassifyRequirement_InvalidJSON_DefaultsToFeature(t *testing.T) {
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "this is not json at all",
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Do something", profile)
	if err != nil {
		t.Fatalf("expected no error on fallback, got: %v", err)
	}

	if result.Type != "feature" {
		t.Fatalf("expected fallback type 'feature', got %q", result.Type)
	}
	if result.Confidence != 0.5 {
		t.Fatalf("expected fallback confidence 0.5, got %f", result.Confidence)
	}
}

func TestClassifyRequirement_MarkdownWrappedJSON(t *testing.T) {
	response := "```json\n{\"type\": \"bugfix\", \"confidence\": 0.9, \"signals\": [\"fix\"]}\n```"

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Fix the bug", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "bugfix" {
		t.Fatalf("expected type 'bugfix', got %q", result.Type)
	}
}

func TestClassifyRequirement_PromptIncludesProfile(t *testing.T) {
	response := `{"type": "feature", "confidence": 0.8, "signals": ["new"]}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{
		Language:        "typescript",
		BuildTool:       "npm",
		SourceFileCount: 42,
		TestFileCount:   10,
		CommitCount:     100,
		HasCI:           true,
		HasDocker:       true,
		IsExisting:      true,
	}

	_, err := engine.ClassifyRequirement(context.Background(), client, "Add search", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the LLM was called and the prompt includes profile info
	if client.CallCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", client.CallCount())
	}

	req := client.CallAt(0)
	if len(req.Messages) == 0 {
		t.Fatal("expected at least one message in LLM request")
	}
	msg := req.Messages[0].Content
	if !strings.Contains(msg, "typescript") {
		t.Fatal("expected prompt to include language 'typescript'")
	}
	if !strings.Contains(msg, "42") {
		t.Fatal("expected prompt to include source file count")
	}
}

// --- RequirementContext tests ---

func TestRequirementContext_DerivedBooleans(t *testing.T) {
	tests := []struct {
		name       string
		classType  string
		isExisting bool
		wantBugFix bool
		wantRefact bool
		wantInfra  bool
	}{
		{"feature on existing", "feature", true, false, false, false},
		{"bugfix", "bugfix", true, true, false, false},
		{"refactor", "refactor", true, false, true, false},
		{"infrastructure", "infrastructure", true, false, false, true},
		{"greenfield feature", "feature", false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := engine.NewRequirementContext(
				engine.RepoProfile{IsExisting: tt.isExisting},
				engine.RequirementClassification{Type: tt.classType},
			)

			if ctx.IsExisting != tt.isExisting {
				t.Fatalf("IsExisting: got %v, want %v", ctx.IsExisting, tt.isExisting)
			}
			if ctx.IsBugFix != tt.wantBugFix {
				t.Fatalf("IsBugFix: got %v, want %v", ctx.IsBugFix, tt.wantBugFix)
			}
			if ctx.IsRefactor != tt.wantRefact {
				t.Fatalf("IsRefactor: got %v, want %v", ctx.IsRefactor, tt.wantRefact)
			}
			if ctx.IsInfra != tt.wantInfra {
				t.Fatalf("IsInfra: got %v, want %v", ctx.IsInfra, tt.wantInfra)
			}
		})
	}
}

func TestClassifyRequirement_InvalidType_DefaultsToFeature(t *testing.T) {
	response := `{"type": "unknown_garbage", "confidence": 0.9, "signals": ["weird"]}`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: response,
		Model:   "test-model",
	})

	profile := engine.RepoProfile{Language: "go", BuildTool: "go"}

	result, err := engine.ClassifyRequirement(context.Background(), client, "Do something", profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != "feature" {
		t.Fatalf("expected fallback type 'feature' for invalid type, got %q", result.Type)
	}
	if result.Confidence != 0.5 {
		t.Fatalf("expected fallback confidence 0.5, got %f", result.Confidence)
	}
}
