package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestParseManagerAction_Retry(t *testing.T) {
	raw := `{
		"diagnosis": "codex CLI missing --full-auto flag",
		"category": "environment",
		"action": "retry",
		"retry_config": {
			"target_role": "junior",
			"reset_tier": 0,
			"worktree_reset": true,
			"env_fixes": ["update codex args in nxd.yaml"]
		}
	}`
	action, err := parseManagerAction([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if action.Action != "retry" {
		t.Errorf("expected retry, got %s", action.Action)
	}
	if action.RetryConfig == nil {
		t.Fatal("retry_config is nil")
	}
	if action.RetryConfig.TargetRole != "junior" {
		t.Errorf("expected junior, got %s", action.RetryConfig.TargetRole)
	}
	if !action.RetryConfig.WorktreeReset {
		t.Error("expected worktree_reset=true")
	}
	if len(action.RetryConfig.EnvFixes) != 1 {
		t.Errorf("expected 1 env_fix, got %d", len(action.RetryConfig.EnvFixes))
	}
}

func TestParseManagerAction_Split(t *testing.T) {
	raw := `{
		"diagnosis": "story too complex",
		"category": "structural",
		"action": "split",
		"split_config": {
			"children": [
				{"suffix": "a", "title": "Part A", "description": "First part", "acceptance_criteria": "AC A", "complexity": 2, "owned_files": ["a.go"]},
				{"suffix": "b", "title": "Part B", "description": "Second part", "acceptance_criteria": "AC B", "complexity": 3, "owned_files": ["b.go"]}
			],
			"dependency_edges": [["parent-a", "parent-b"]]
		}
	}`
	action, err := parseManagerAction([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if action.Action != "split" {
		t.Errorf("expected split, got %s", action.Action)
	}
	if action.SplitConfig == nil {
		t.Fatal("split_config is nil")
	}
	if len(action.SplitConfig.Children) != 2 {
		t.Errorf("expected 2 children, got %d", len(action.SplitConfig.Children))
	}
	if action.SplitConfig.Children[0].Suffix != "a" {
		t.Errorf("expected suffix 'a', got %s", action.SplitConfig.Children[0].Suffix)
	}
	if len(action.SplitConfig.DependencyEdges) != 1 {
		t.Errorf("expected 1 dependency edge, got %d", len(action.SplitConfig.DependencyEdges))
	}
}

func TestParseManagerAction_Rewrite(t *testing.T) {
	raw := `{
		"diagnosis": "wrong approach",
		"category": "structural",
		"action": "rewrite",
		"rewrite_config": {
			"title": "Better title",
			"description": "Better description",
			"acceptance_criteria": "Better AC",
			"complexity": 3,
			"owned_files": ["new.go"]
		}
	}`
	action, err := parseManagerAction([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if action.Action != "rewrite" {
		t.Errorf("expected rewrite, got %s", action.Action)
	}
	if action.RewriteConfig == nil {
		t.Fatal("rewrite_config is nil")
	}
	if action.RewriteConfig.Title != "Better title" {
		t.Errorf("wrong title: %s", action.RewriteConfig.Title)
	}
	if action.RewriteConfig.Complexity != 3 {
		t.Errorf("expected complexity 3, got %d", action.RewriteConfig.Complexity)
	}
	if len(action.RewriteConfig.OwnedFiles) != 1 {
		t.Errorf("expected 1 owned file, got %d", len(action.RewriteConfig.OwnedFiles))
	}
}

func TestParseManagerAction_EscalateToTechLead(t *testing.T) {
	raw := `{"diagnosis": "bad decomposition", "category": "structural", "action": "escalate_to_techlead"}`
	action, err := parseManagerAction([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if action.Action != "escalate_to_techlead" {
		t.Errorf("expected escalate_to_techlead, got %s", action.Action)
	}
	if action.Diagnosis != "bad decomposition" {
		t.Errorf("wrong diagnosis: %s", action.Diagnosis)
	}
	if action.Category != "structural" {
		t.Errorf("wrong category: %s", action.Category)
	}
}

func TestParseManagerAction_InvalidJSON(t *testing.T) {
	_, err := parseManagerAction([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseManagerAction_InvalidAction(t *testing.T) {
	_, err := parseManagerAction([]byte(`{"diagnosis": "x", "category": "y", "action": "unknown"}`))
	if err == nil {
		t.Error("expected error for invalid action")
	}
}

func TestParseManagerAction_EmptyAction(t *testing.T) {
	_, err := parseManagerAction([]byte(`{"diagnosis": "x", "category": "y", "action": ""}`))
	if err == nil {
		t.Error("expected error for empty action")
	}
}

func TestBuildPrompt_IncludesStoryDetails(t *testing.T) {
	m := &Manager{}
	dc := DiagnosticContext{
		StoryID:    "s-001",
		StoryTitle: "Test story",
		Complexity: 3,
		AgentLog:   "error: command not found",
	}
	prompt := m.buildPrompt(dc)
	if !strings.Contains(prompt, "s-001") {
		t.Error("prompt missing story ID")
	}
	if !strings.Contains(prompt, "Test story") {
		t.Error("prompt missing title")
	}
	if !strings.Contains(prompt, "command not found") {
		t.Error("prompt missing agent log")
	}
}

func TestBuildPrompt_IncludesWorktreeInfo(t *testing.T) {
	m := &Manager{}
	dc := DiagnosticContext{
		StoryID:        "s-002",
		StoryTitle:     "Worktree story",
		WorktreeStatus: "M main.go",
		WorktreeLog:    "abc1234 Initial commit",
		WorktreeFiles:  "main.go\ngo.mod",
	}
	prompt := m.buildPrompt(dc)
	if !strings.Contains(prompt, "M main.go") {
		t.Error("prompt missing worktree status")
	}
	if !strings.Contains(prompt, "abc1234") {
		t.Error("prompt missing worktree log")
	}
}

func TestBuildPrompt_IncludesEventHistory(t *testing.T) {
	m := &Manager{}
	dc := DiagnosticContext{
		StoryID:    "s-003",
		StoryTitle: "Events story",
		EventHistory: []eventSummary{
			{Type: "STORY_STARTED", AgentID: "agent-1"},
			{Type: "STORY_REVIEW_FAILED", AgentID: "reviewer"},
		},
	}
	prompt := m.buildPrompt(dc)
	if !strings.Contains(prompt, "STORY_STARTED") {
		t.Error("prompt missing event type")
	}
	if !strings.Contains(prompt, "agent-1") {
		t.Error("prompt missing agent ID")
	}
}

func TestManagerTruncate_Short(t *testing.T) {
	result := managerTruncate("short", 100)
	if result != "short" {
		t.Errorf("expected 'short', got %q", result)
	}
}

func TestManagerTruncate_Long(t *testing.T) {
	long := strings.Repeat("x", 200)
	result := managerTruncate(long, 50)
	if len(result) != 50 {
		t.Errorf("expected length 50, got %d", len(result))
	}
	// Should keep the tail (most recent output)
	if result != long[150:] {
		t.Error("managerTruncate should keep tail of string")
	}
}

func newManagerTestStores(t *testing.T) (state.EventStore, state.ProjectionStore, func()) {
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

func TestDiagnose_RetryAction(t *testing.T) {
	es, ps, cleanup := newManagerTestStores(t)
	defer cleanup()

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"diagnosis": "missing env var", "category": "environment", "action": "retry", "retry_config": {"target_role": "junior", "reset_tier": 0, "worktree_reset": true, "env_fixes": ["export FOO=bar"]}}`,
	})

	mgr := NewManager(client, "ollama", "qwen2.5-coder:14b", 4000, es, ps)
	action, err := mgr.Diagnose(context.Background(), DiagnosticContext{
		StoryID:    "s-001",
		StoryTitle: "Test story",
		AgentLog:   "env var FOO not set",
	})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if action.Action != "retry" {
		t.Errorf("expected retry, got %s", action.Action)
	}
	if action.RetryConfig == nil {
		t.Fatal("retry_config is nil")
	}
	if action.RetryConfig.TargetRole != "junior" {
		t.Errorf("expected junior, got %s", action.RetryConfig.TargetRole)
	}

	// Verify the LLM was called with correct model and system prompt.
	req := client.CallAt(0)
	if req.Model != "qwen2.5-coder:14b" {
		t.Errorf("expected model 'qwen2.5-coder:14b', got %s", req.Model)
	}
	if req.System != managerSystemPrompt {
		t.Error("system prompt mismatch")
	}
}

func TestDiagnose_MarkdownFences(t *testing.T) {
	es, ps, cleanup := newManagerTestStores(t)
	defer cleanup()

	// LLM wraps response in markdown fences.
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "```json\n{\"diagnosis\": \"wrapped\", \"category\": \"transient\", \"action\": \"escalate_to_techlead\"}\n```",
	})

	mgr := NewManager(client, "ollama", "qwen2.5-coder:14b", 4000, es, ps)
	action, err := mgr.Diagnose(context.Background(), DiagnosticContext{
		StoryID:    "s-002",
		StoryTitle: "Fenced response",
	})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if action.Action != "escalate_to_techlead" {
		t.Errorf("expected escalate_to_techlead, got %s", action.Action)
	}
}

func TestDiagnose_LLMError(t *testing.T) {
	es, ps, cleanup := newManagerTestStores(t)
	defer cleanup()

	client := llm.NewReplayClient() // no responses -- will error
	mgr := NewManager(client, "ollama", "qwen2.5-coder:14b", 4000, es, ps)

	_, err := mgr.Diagnose(context.Background(), DiagnosticContext{
		StoryID:    "s-003",
		StoryTitle: "Error story",
	})
	if err == nil {
		t.Fatal("expected LLM error")
	}
	if !strings.Contains(err.Error(), "manager LLM call") {
		t.Errorf("expected 'manager LLM call' in error, got: %s", err.Error())
	}
}

func TestDiagnose_InvalidLLMResponse(t *testing.T) {
	es, ps, cleanup := newManagerTestStores(t)
	defer cleanup()

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "I cannot help with that",
	})

	mgr := NewManager(client, "ollama", "qwen2.5-coder:14b", 4000, es, ps)
	_, err := mgr.Diagnose(context.Background(), DiagnosticContext{
		StoryID:    "s-004",
		StoryTitle: "Bad response",
	})
	if err == nil {
		t.Fatal("expected parse error for non-JSON response")
	}
}
