package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

func TestGemmaRuntime_Name(t *testing.T) {
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 20})
	if rt.Name() != "gemma" {
		t.Errorf("Name() = %q, want %q", rt.Name(), "gemma")
	}
}

func TestGemmaRuntime_SupportedModels(t *testing.T) {
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{})
	found := false
	for _, m := range rt.SupportedModels() {
		if m == "gemma4" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'gemma4' in supported models")
	}
}

func TestCodingTools(t *testing.T) {
	tools := CodingTools()
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"read_file", "write_file", "edit_file", "run_command", "task_complete"} {
		if !names[name] {
			t.Errorf("missing coding tool %q", name)
		}
	}
}

func TestGemmaRuntime_ReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "hello.go"), []byte("package main\n"), 0644)

	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 20})
	call := llm.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path": "hello.go"}`)}
	result := rt.executeTool(call, tmpDir)
	if result.IsError {
		t.Fatalf("read_file failed: %s", result.Content)
	}
	if result.Content != "package main\n" {
		t.Errorf("content = %q", result.Content)
	}
}

func TestGemmaRuntime_WriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 20})
	call := llm.ToolCall{Name: "write_file", Arguments: json.RawMessage(`{"path": "new.go", "content": "package main\n"}`)}
	result := rt.executeTool(call, tmpDir)
	if result.IsError {
		t.Fatalf("write_file failed: %s", result.Content)
	}
	content, _ := os.ReadFile(filepath.Join(tmpDir, "new.go"))
	if string(content) != "package main\n" {
		t.Errorf("file content = %q", string(content))
	}
}

func TestGemmaRuntime_EditFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc hello() {}\n"), 0644)

	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 20})
	call := llm.ToolCall{
		Name:      "edit_file",
		Arguments: json.RawMessage(`{"path": "main.go", "old_text": "func hello() {}", "new_text": "func hello() string { return \"hi\" }"}`),
	}
	result := rt.executeTool(call, tmpDir)
	if result.IsError {
		t.Fatalf("edit_file failed: %s", result.Content)
	}
	content, _ := os.ReadFile(filepath.Join(tmpDir, "main.go"))
	if expected := "package main\n\nfunc hello() string { return \"hi\" }\n"; string(content) != expected {
		t.Errorf("content = %q, want %q", string(content), expected)
	}
}

func TestGemmaRuntime_PathTraversalBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{MaxIterations: 20})
	call := llm.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path": "../../../etc/passwd"}`)}
	result := rt.executeTool(call, tmpDir)
	if !result.IsError {
		t.Error("expected path traversal to be blocked")
	}
}

func TestGemmaRuntime_ProgressCallback(t *testing.T) {
	tmpDir := t.TempDir()

	// Replay: model calls write_file, then task_complete.
	writeArgs, _ := json.Marshal(map[string]string{"path": "main.go", "content": "package main\n"})
	completeArgs, _ := json.Marshal(map[string]string{"summary": "created main.go"})

	client := llm.NewReplayClient(
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{ID: "1", Name: "write_file", Arguments: writeArgs},
			},
		},
		llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{ID: "2", Name: "task_complete", Arguments: completeArgs},
			},
		},
	)

	rt := NewGemmaRuntime(client, GemmaRuntimeConfig{MaxIterations: 5})

	var events []ProgressEvent
	rt.OnProgress = func(evt ProgressEvent) {
		events = append(events, evt)
	}

	result := rt.Execute(t.Context(), tmpDir, "gemma4:e4b", "", "create main.go")
	if result.Error != nil {
		t.Fatalf("Execute failed: %v", result.Error)
	}
	if result.Summary != "created main.go" {
		t.Errorf("summary = %q, want %q", result.Summary, "created main.go")
	}

	// Expect progress events: thinking(1), tool_call(write), tool_result(write),
	// thinking(2), completed(task_complete).
	if len(events) < 5 {
		t.Fatalf("got %d progress events, want at least 5", len(events))
	}

	// Verify coarse: first event is "thinking" for iteration 1.
	if events[0].Phase != PhaseThinking || events[0].Iteration != 1 {
		t.Errorf("event[0] = %+v, want thinking iter 1", events[0])
	}

	// Verify fine: tool_call for write_file with file path.
	if events[1].Phase != PhaseToolCall || events[1].Tool != "write_file" || events[1].File != "main.go" {
		t.Errorf("event[1] = %+v, want tool_call write_file main.go", events[1])
	}

	// Verify fine: tool_result for write_file.
	if events[2].Phase != PhaseToolResult || events[2].Tool != "write_file" {
		t.Errorf("event[2] = %+v, want tool_result write_file", events[2])
	}

	// Verify MaxIter is populated.
	if events[0].MaxIter != 5 {
		t.Errorf("MaxIter = %d, want 5", events[0].MaxIter)
	}

	// Verify last event is completed.
	last := events[len(events)-1]
	if last.Phase != PhaseCompleted {
		t.Errorf("last event phase = %q, want completed", last.Phase)
	}
}

func TestGemmaRuntime_ProgressCallbackOnError(t *testing.T) {
	tmpDir := t.TempDir()

	client := llm.NewErrorClient(fmt.Errorf("connection refused"))
	rt := NewGemmaRuntime(client, GemmaRuntimeConfig{MaxIterations: 3})

	var events []ProgressEvent
	rt.OnProgress = func(evt ProgressEvent) {
		events = append(events, evt)
	}

	result := rt.Execute(t.Context(), tmpDir, "gemma4:e4b", "", "do something")
	if result.Error == nil {
		t.Fatal("expected error")
	}

	// Should get: thinking(1), error.
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2", len(events))
	}
	if events[0].Phase != PhaseThinking {
		t.Errorf("event[0].Phase = %q, want thinking", events[0].Phase)
	}
	if events[1].Phase != PhaseError || !events[1].IsError {
		t.Errorf("event[1] = %+v, want error phase with IsError=true", events[1])
	}
}

func TestGemmaRuntime_CommandAllowlist(t *testing.T) {
	tmpDir := t.TempDir()
	rt := NewGemmaRuntime(nil, GemmaRuntimeConfig{
		MaxIterations:    20,
		CommandAllowlist: []string{"echo hello"},
	})

	allowed := llm.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command": "echo hello"}`)}
	result := rt.executeTool(allowed, tmpDir)
	if result.IsError {
		t.Errorf("allowed command failed: %s", result.Content)
	}

	blocked := llm.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command": "rm -rf /"}`)}
	result = rt.executeTool(blocked, tmpDir)
	if !result.IsError {
		t.Error("expected blocked command to be rejected")
	}
}
