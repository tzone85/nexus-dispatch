package runtime

import (
	"encoding/json"
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
