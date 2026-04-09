package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newWiringTestStores creates event and projection stores for internal wiring
// tests. The caller must invoke the returned cleanup function.
func newWiringTestStores(t *testing.T) (state.EventStore, state.ProjectionStore, func()) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	cleanup := func() {
		es.Close()
		ps.Close()
	}
	return es, ps, cleanup
}

// --- Test 14: ReviewerToolCalling ---
// Prove: Reviewer with gemma4 model + google+ollama provider sends tools
// in the LLM request (reviewWithTools path).

func TestWiring_ReviewerToolCalling(t *testing.T) {
	es, ps, cleanup := newWiringTestStores(t)
	defer cleanup()

	// Pre-populate story so event projection succeeds
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Auth", "description": "desc", "complexity": 3,
	}))

	// Build a tool-call response matching submit_review schema
	submitArgs, _ := json.Marshal(map[string]any{
		"verdict": "approve",
		"summary": "Code looks good",
		"file_comments": []map[string]any{
			{"file": "main.go", "line": 10, "severity": "info", "message": "Nice work"},
		},
	})

	client := llm.NewReplayClient(llm.CompletionResponse{
		Model: "gemma4:26b",
		ToolCalls: []llm.ToolCall{
			{Name: "submit_review", Arguments: submitArgs},
		},
	})

	reviewer := NewReviewer(client, "google+ollama", "gemma4:26b", 8000, es, ps)

	result, err := reviewer.Review(
		context.Background(),
		"s-001",
		"Add auth module",
		"Auth works",
		"diff --git a/main.go b/main.go\n+func Auth() {}",
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	if !result.Passed {
		t.Error("expected review to pass with approve verdict")
	}
	if result.Summary != "Code looks good" {
		t.Errorf("expected summary 'Code looks good', got %q", result.Summary)
	}

	// Inspect the captured LLM request to verify tools were attached
	captured := client.CallAt(0)
	if len(captured.Tools) == 0 {
		t.Fatal("expected Tools field populated in LLM request for gemma4 + google+ollama")
	}

	foundSubmitReview := false
	for _, tool := range captured.Tools {
		if tool.Name == "submit_review" {
			foundSubmitReview = true
		}
	}
	if !foundSubmitReview {
		t.Error("expected submit_review tool in captured request Tools")
	}

	if captured.ToolChoice != "required" {
		t.Errorf("expected ToolChoice 'required', got %q", captured.ToolChoice)
	}
}

// --- Test 15: PlannerToolCalling ---
// Prove: Planner with gemma4 model (DefaultConfig) includes PlannerTools
// in the LLM request.

func TestWiring_PlannerToolCalling(t *testing.T) {
	es, ps, cleanup := newWiringTestStores(t)
	defer cleanup()

	// Create a repo dir with go.mod so ScanRepo works
	repoDir := t.TempDir()
	os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module test"), 0644)

	// Build a tool-call response with create_story calls
	storyArgs, _ := json.Marshal(map[string]any{
		"title":               "Setup project",
		"description":         "Scaffold directory structure",
		"complexity":          2,
		"acceptance_criteria": "Project compiles",
	})

	client := llm.NewReplayClient(llm.CompletionResponse{
		Model: "gemma4:26b",
		ToolCalls: []llm.ToolCall{
			{Name: "create_story", Arguments: storyArgs},
		},
	})

	cfg := config.DefaultConfig()
	planner := NewPlanner(client, cfg, es, ps)

	result, err := planner.Plan(context.Background(), "r-100", "Build auth system", repoDir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if len(result.Stories) != 1 {
		t.Fatalf("expected 1 story, got %d", len(result.Stories))
	}
	if result.Stories[0].Title != "Setup project" {
		t.Errorf("expected story title 'Setup project', got %q", result.Stories[0].Title)
	}

	// Inspect captured LLM request to verify PlannerTools were attached
	captured := client.CallAt(0)
	if len(captured.Tools) == 0 {
		t.Fatal("expected Tools field populated in LLM request for gemma4 DefaultConfig")
	}

	foundCreateStory := false
	for _, tool := range captured.Tools {
		if tool.Name == "create_story" {
			foundCreateStory = true
		}
	}
	if !foundCreateStory {
		t.Error("expected create_story tool in captured request Tools")
	}

	if captured.ToolChoice != "required" {
		t.Errorf("expected ToolChoice 'required', got %q", captured.ToolChoice)
	}
}

// --- Test 16: NativeRuntimeSelects ---
// Prove: gemma4 model -> native runtime registered and detectable via
// DefaultConfig runtimes.

func TestWiring_NativeRuntimeSelects(t *testing.T) {
	cfg := config.DefaultConfig()

	reg, err := runtime.NewRegistry(cfg.Runtimes)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	if !reg.IsNative("gemma") {
		t.Error("expected gemma to be a native runtime")
	}

	nativeCfg, ok := reg.NativeConfig("gemma")
	if !ok {
		t.Fatal("expected NativeConfig to return config for gemma")
	}
	if nativeCfg.MaxIterations <= 0 {
		t.Errorf("expected MaxIterations > 0, got %d", nativeCfg.MaxIterations)
	}
	if !nativeCfg.Native {
		t.Error("expected Native flag to be true")
	}
	if len(nativeCfg.CommandAllowlist) == 0 {
		t.Error("expected non-empty CommandAllowlist for native runtime")
	}

	// Non-native runtimes should not be detected as native
	if reg.IsNative("claude-code") {
		t.Error("expected claude-code to NOT be a native runtime")
	}
	if reg.IsNative("aider") {
		t.Error("expected aider to NOT be a native runtime")
	}
}
