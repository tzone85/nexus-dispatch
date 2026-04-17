package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
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

// ── describeToolCall ──────────────────────────────────────────────────

func TestDescribeToolCall_AllBranches(t *testing.T) {
	tests := []struct {
		tool    string
		file    string
		command string
		want    string
	}{
		{"read_file", "foo.go", "", "reading foo.go"},
		{"write_file", "bar.go", "", "writing bar.go"},
		{"edit_file", "baz.go", "", "editing baz.go"},
		{"run_command", "", "go build ./...", "running: go build ./..."},
		{"task_complete", "", "", "calling task_complete"},
		{"write_scratchboard", "", "", "calling write_scratchboard"},
		{"unknown_tool", "", "", "calling unknown_tool"},
	}
	for _, tt := range tests {
		got := describeToolCall(tt.tool, tt.file, tt.command)
		if got != tt.want {
			t.Errorf("describeToolCall(%q, %q, %q) = %q, want %q", tt.tool, tt.file, tt.command, got, tt.want)
		}
	}
}

// ── describeToolResult ────────────────────────────────────────────────

func TestDescribeToolResult_AllBranches(t *testing.T) {
	tests := []struct {
		tool    string
		file    string
		command string
		isErr   bool
		want    string
	}{
		{"read_file", "foo.go", "", false, "read foo.go: ok"},
		{"read_file", "foo.go", "", true, "read foo.go: failed"},
		{"write_file", "bar.go", "", false, "wrote bar.go: ok"},
		{"write_file", "bar.go", "", true, "wrote bar.go: failed"},
		{"edit_file", "baz.go", "", false, "edited baz.go: ok"},
		{"edit_file", "baz.go", "", true, "edited baz.go: failed"},
		{"run_command", "", "go test", false, "command go test: ok"},
		{"run_command", "", "go test", true, "command go test: failed"},
		{"task_complete", "", "", false, "task_complete: ok"},
		{"unknown_tool", "", "", true, "unknown_tool: failed"},
	}
	for _, tt := range tests {
		got := describeToolResult(tt.tool, tt.file, tt.command, tt.isErr)
		if got != tt.want {
			t.Errorf("describeToolResult(%q, %q, %q, %v) = %q, want %q",
				tt.tool, tt.file, tt.command, tt.isErr, got, tt.want)
		}
	}
}

// ── Execute — additional paths ────────────────────────────────────────

func TestGemmaRuntime_Execute_MaxIterationsReached(t *testing.T) {
	tmpDir := t.TempDir()

	// Each response returns a non-task-complete tool call so we exhaust iterations.
	writeArgs, _ := json.Marshal(map[string]string{"path": "a.go", "content": "x"})
	responses := make([]llm.CompletionResponse, 3)
	for i := range responses {
		responses[i] = llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{
				{ID: fmt.Sprintf("%d", i), Name: "write_file", Arguments: writeArgs},
			},
		}
	}

	client := llm.NewReplayClient(responses...)
	rt := NewGemmaRuntime(client, GemmaRuntimeConfig{MaxIterations: 3})

	var lastPhase ProgressPhase
	rt.OnProgress = func(evt ProgressEvent) { lastPhase = evt.Phase }

	result := rt.Execute(context.Background(), tmpDir, "gemma4", "", "keep writing")
	if result.Error == nil {
		t.Fatal("expected error when max iterations reached")
	}
	if !strings.Contains(result.Error.Error(), "max iterations") {
		t.Errorf("error = %v, expected 'max iterations'", result.Error)
	}
	if lastPhase != PhaseError {
		t.Errorf("last phase = %q, want PhaseError", lastPhase)
	}
}

func TestGemmaRuntime_Execute_NoToolCalls_ReturnsEarly(t *testing.T) {
	tmpDir := t.TempDir()

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content:   "I think we should consider the architecture",
		ToolCalls: nil, // no tool calls
	})
	rt := NewGemmaRuntime(client, GemmaRuntimeConfig{MaxIterations: 5})

	result := rt.Execute(context.Background(), tmpDir, "gemma4", "system prompt", "analyze code")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", result.Iterations)
	}
	if result.Summary != "I think we should consider the architecture" {
		t.Errorf("summary = %q", result.Summary)
	}
}

func TestGemmaRuntime_Execute_WithSystemPrompt(t *testing.T) {
	tmpDir := t.TempDir()

	completeArgs, _ := json.Marshal(map[string]string{"summary": "done with system prompt"})
	client := llm.NewReplayClient(llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "task_complete", Arguments: completeArgs},
		},
	})
	rt := NewGemmaRuntime(client, GemmaRuntimeConfig{MaxIterations: 5})

	result := rt.Execute(context.Background(), tmpDir, "gemma4", "you are a coding agent", "implement feature X")
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Summary != "done with system prompt" {
		t.Errorf("summary = %q", result.Summary)
	}
}

// ── execWriteFile — invalid args path ────────────────────────────────

func TestExecWriteFile_InvalidJSON(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execWriteFile(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`not valid json`)}, t.TempDir())
	if !result.IsError {
		t.Error("expected error for invalid JSON args")
	}
	if !strings.Contains(result.Content, "invalid arguments") {
		t.Errorf("content = %q, expected 'invalid arguments'", result.Content)
	}
}

// ── execEditFile — invalid args path ─────────────────────────────────

func TestExecEditFile_InvalidJSON(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execEditFile(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`not valid json`)}, t.TempDir())
	if !result.IsError {
		t.Error("expected error for invalid JSON args")
	}
	if !strings.Contains(result.Content, "invalid arguments") {
		t.Errorf("content = %q, expected 'invalid arguments'", result.Content)
	}
}

func TestExecEditFile_Traversal(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execEditFile(makeToolCall("edit_file", map[string]string{
		"path": "../../etc/passwd", "old_text": "root", "new_text": "evil",
	}), t.TempDir())
	if !result.IsError {
		t.Error("expected error for path traversal")
	}
}

// ── execRunCommand — invalid args path ───────────────────────────────

func TestExecRunCommand_InvalidJSON(t *testing.T) {
	rt := newTestRuntime(t)
	result := rt.execRunCommand(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`not valid json`)}, t.TempDir())
	if !result.IsError {
		t.Error("expected error for invalid JSON args")
	}
}

// ── execWriteScratchboard — invalid args path ─────────────────────────

func TestExecWriteScratchboard_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	sbPath := filepath.Join(tmpDir, "scratch.jsonl")
	sb, _ := scratchboard.New(sbPath)

	rt := newTestRuntime(t)
	rt.Scratchboard = sb

	result := rt.execWriteScratchboard(llm.ToolCall{ID: "c1", Arguments: json.RawMessage(`not valid json`)})
	if !result.IsError {
		t.Error("expected error for invalid JSON args in execWriteScratchboard")
	}
}

// ── execReadScratchboard — with entries ──────────────────────────────

func TestExecReadScratchboard_WithEntries(t *testing.T) {
	sbPath := filepath.Join(t.TempDir(), "scratch.jsonl")
	sb, _ := scratchboard.New(sbPath)

	rt := newTestRuntime(t)
	rt.Scratchboard = sb
	rt.AgentID = "agent-002"
	rt.StoryID = "s-002"

	// First write an entry.
	writeResult := rt.execWriteScratchboard(makeToolCall("write_scratchboard", map[string]string{
		"category": "gotcha", "content": "always check nil",
	}))
	if writeResult.IsError {
		t.Fatalf("write failed: %s", writeResult.Content)
	}

	// Now read it back.
	readResult := rt.execReadScratchboard(makeToolCall("read_scratchboard", map[string]string{
		"category": "gotcha",
	}))
	if readResult.IsError {
		t.Fatalf("read failed: %s", readResult.Content)
	}
	if !strings.Contains(readResult.Content, "always check nil") {
		t.Errorf("read content = %q, expected 'always check nil'", readResult.Content)
	}
}

// ── AgentStatus unknown ───────────────────────────────────────────────

func TestAgentStatus_String_Unknown(t *testing.T) {
	unknown := AgentStatus(999)
	if unknown.String() != "unknown" {
		t.Errorf("String() = %q, want 'unknown'", unknown.String())
	}
}
