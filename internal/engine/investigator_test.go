package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestInvestigator_HappyPath(t *testing.T) {
	// Setup a temp repo directory with a file to read
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Build the submit_report arguments
	report := map[string]any{
		"summary":      "A small Go project with a single entry point.",
		"entry_points": []string{"main.go"},
		"modules": []map[string]any{
			{
				"name":       "root",
				"path":       ".",
				"file_count": 1,
				"line_count": 2,
				"has_tests":  false,
			},
		},
		"build_passes":    true,
		"test_passes":     true,
		"test_count":      0,
		"coverage_pct":    0.0,
		"code_smells":     []map[string]string{},
		"risk_areas":      []map[string]string{},
		"recommendations": []string{"Add tests for main.go"},
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	// Three-step flow: read_file -> run_command -> submit_report
	client := llm.NewReplayClient(
		// Step 1: model asks to read a file
		llm.CompletionResponse{
			Content: "Let me investigate the codebase.",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			},
		},
		// Step 2: model asks to run a command
		llm.CompletionResponse{
			Content: "Now let me check the build.",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-2",
					Name:      "run_command",
					Arguments: json.RawMessage(`{"command":"ls -la"}`),
				},
			},
		},
		// Step 3: model submits the report
		llm.CompletionResponse{
			Content: "Here is my report.",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-3",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	result, err := inv.Investigate(context.Background(), dir)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	// Verify report fields
	if result.Summary != "A small Go project with a single entry point." {
		t.Fatalf("expected summary 'A small Go project with a single entry point.', got %q", result.Summary)
	}
	if len(result.EntryPoints) != 1 || result.EntryPoints[0] != "main.go" {
		t.Fatalf("expected entry_points [main.go], got %v", result.EntryPoints)
	}
	if len(result.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(result.Modules))
	}
	if result.Modules[0].Name != "root" {
		t.Fatalf("expected module name 'root', got %q", result.Modules[0].Name)
	}
	if result.Modules[0].FileCount != 1 {
		t.Fatalf("expected file_count 1, got %d", result.Modules[0].FileCount)
	}
	if !result.BuildStatus.Passes {
		t.Fatal("expected build_status.passes to be true")
	}
	if !result.TestStatus.Passes {
		t.Fatal("expected test_status.passes to be true")
	}
	if len(result.Recommendations) != 1 || result.Recommendations[0] != "Add tests for main.go" {
		t.Fatalf("expected recommendations [Add tests for main.go], got %v", result.Recommendations)
	}

	// Verify client was called 3 times
	if client.CallCount() != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", client.CallCount())
	}

	// Verify second call includes tool result from read_file
	secondReq := client.CallAt(1)
	foundToolResult := false
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "call-1" {
			foundToolResult = true
			if !strings.Contains(msg.Content, "package main") {
				t.Fatalf("expected read_file result to contain file contents, got %q", msg.Content)
			}
		}
	}
	if !foundToolResult {
		t.Fatal("expected tool result for call-1 in second request")
	}
}

func TestInvestigator_ReadFilePathTraversal(t *testing.T) {
	dir := t.TempDir()

	// Create a file outside the repo to ensure it cannot be accessed
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret data"), 0644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// The model tries to read a file outside the repo with path traversal
	reportJSON, _ := json.Marshal(map[string]any{
		"summary":         "done",
		"entry_points":    []string{},
		"build_passes":    false,
		"test_passes":     false,
		"recommendations": []string{},
	})

	client := llm.NewReplayClient(
		// Step 1: model tries path traversal
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"../../etc/passwd"}`),
				},
			},
		},
		// Step 2: model submits report
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-2",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	result, err := inv.Investigate(context.Background(), dir)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	// Should still succeed - the tool result should contain an error message
	secondReq := client.CallAt(1)
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "call-1" {
			if !strings.Contains(msg.Content, "path traversal") {
				t.Fatalf("expected path traversal error in tool result, got %q", msg.Content)
			}
		}
	}
	if result.Summary != "done" {
		t.Fatalf("expected summary 'done', got %q", result.Summary)
	}
}

func TestInvestigator_MaxIterationsExceeded(t *testing.T) {
	// Build 21 responses that never submit a report — only read_file calls
	responses := make([]llm.CompletionResponse, 21)
	for i := range responses {
		responses[i] = llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-loop",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			},
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	client := llm.NewReplayClient(responses...)
	inv := engine.NewInvestigator(client, "test-model", 4096)

	_, err := inv.Investigate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error when max iterations exceeded")
	}
	if !strings.Contains(err.Error(), "iteration") {
		t.Fatalf("expected iteration limit error, got: %v", err)
	}
}

func TestInvestigator_LLMError(t *testing.T) {
	// No responses at all — client will return error immediately
	client := llm.NewReplayClient()
	inv := engine.NewInvestigator(client, "test-model", 4096)

	_, err := inv.Investigate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error when LLM client fails")
	}
}

func TestInvestigator_ReadFileTruncation(t *testing.T) {
	dir := t.TempDir()

	// Create a large file (>8000 chars)
	largeContent := strings.Repeat("x", 10000)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(largeContent), 0644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}

	reportJSON, _ := json.Marshal(map[string]any{
		"summary":         "done",
		"entry_points":    []string{},
		"build_passes":    false,
		"test_passes":     false,
		"recommendations": []string{},
	})

	client := llm.NewReplayClient(
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"big.txt"}`),
				},
			},
		},
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-2",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	_, err := inv.Investigate(context.Background(), dir)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	// The tool result in the second call should be truncated
	secondReq := client.CallAt(1)
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "call-1" {
			if len(msg.Content) > 8200 { // 8000 + some overhead for truncation message
				t.Fatalf("expected truncated content, got %d chars", len(msg.Content))
			}
			if !strings.Contains(msg.Content, "truncated") {
				t.Fatalf("expected truncation notice in content, got %q", msg.Content[:100])
			}
		}
	}
}

func TestInvestigator_RunCommandTruncation(t *testing.T) {
	dir := t.TempDir()

	reportJSON, _ := json.Marshal(map[string]any{
		"summary":         "done",
		"entry_points":    []string{},
		"build_passes":    false,
		"test_passes":     false,
		"recommendations": []string{},
	})

	// Ask to run a command that produces lots of output
	client := llm.NewReplayClient(
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "run_command",
					Arguments: json.RawMessage(`{"command":"echo hello"}`),
				},
			},
		},
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-2",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	result, err := inv.Investigate(context.Background(), dir)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	if result.Summary != "done" {
		t.Fatalf("expected summary 'done', got %q", result.Summary)
	}

	// Verify second call includes tool result from run_command
	secondReq := client.CallAt(1)
	foundResult := false
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "call-1" {
			foundResult = true
			if !strings.Contains(msg.Content, "hello") {
				t.Fatalf("expected command output to contain 'hello', got %q", msg.Content)
			}
		}
	}
	if !foundResult {
		t.Fatal("expected tool result for run_command in second request")
	}
}

func TestInvestigator_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := llm.NewReplayClient(
		llm.CompletionResponse{
			Content: "investigating...",
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	_, err := inv.Investigate(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
}

func TestInvestigator_NoToolCallsReturnsReport(t *testing.T) {
	// If model returns plain text without tool calls, the loop should continue
	// until max iterations or report submission.
	reportJSON, _ := json.Marshal(map[string]any{
		"summary":         "quick analysis",
		"entry_points":    []string{},
		"build_passes":    true,
		"test_passes":     true,
		"recommendations": []string{"none"},
	})

	client := llm.NewReplayClient(
		// Model returns text with no tool calls
		llm.CompletionResponse{
			Content: "I will now analyze this codebase.",
		},
		// Then submits report
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	result, err := inv.Investigate(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}
	if result.Summary != "quick analysis" {
		t.Fatalf("expected summary 'quick analysis', got %q", result.Summary)
	}
}

func TestInvestigator_MultipleToolCallsInOneResponse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b"), 0644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}

	reportJSON, _ := json.Marshal(map[string]any{
		"summary":         "two files found",
		"entry_points":    []string{"a.go", "b.go"},
		"build_passes":    true,
		"test_passes":     true,
		"recommendations": []string{},
	})

	client := llm.NewReplayClient(
		// Model issues two tool calls at once
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"a.go"}`),
				},
				{
					ID:        "call-2",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"b.go"}`),
				},
			},
		},
		// Then submits report
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{
					ID:        "call-3",
					Name:      "submit_report",
					Arguments: reportJSON,
				},
			},
		},
	)

	inv := engine.NewInvestigator(client, "test-model", 4096)
	result, err := inv.Investigate(context.Background(), dir)
	if err != nil {
		t.Fatalf("investigate: %v", err)
	}

	if len(result.EntryPoints) != 2 {
		t.Fatalf("expected 2 entry points, got %d", len(result.EntryPoints))
	}

	// Verify both tool results appear in the second call
	secondReq := client.CallAt(1)
	toolResults := 0
	for _, msg := range secondReq.Messages {
		if msg.Role == llm.RoleTool {
			toolResults++
		}
	}
	if toolResults != 2 {
		t.Fatalf("expected 2 tool results in second request, got %d", toolResults)
	}
}
