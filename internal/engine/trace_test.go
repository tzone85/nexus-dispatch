package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTraceString_ToolCalls(t *testing.T) {
	input := `Read(path="/Users/test/main.go")
Some random text
Edit(file="/Users/test/main.go", old_string="foo")
Bash(command="go build ./...")`

	events := ParseTraceString(input)
	toolCalls := 0
	for _, e := range events {
		if e.Kind == TraceToolCall {
			toolCalls++
		}
	}
	if toolCalls < 2 {
		t.Errorf("expected at least 2 tool calls, got %d", toolCalls)
	}
}

func TestParseTraceString_FileOperations(t *testing.T) {
	input := `Created internal/engine/lockfile.go
Edited internal/engine/qa.go
Modified internal/runtime/registry.go`

	events := ParseTraceString(input)
	creates := 0
	edits := 0
	for _, e := range events {
		switch e.Kind {
		case TraceFileCreate:
			creates++
			if e.File == "" {
				t.Error("file create should have File field set")
			}
		case TraceFileEdit:
			edits++
		}
	}
	if creates != 1 {
		t.Errorf("expected 1 file create, got %d", creates)
	}
	if edits < 1 {
		t.Errorf("expected at least 1 file edit, got %d", edits)
	}
}

func TestParseTraceString_Errors(t *testing.T) {
	input := `./main.go:10: undefined: foo
FAIL	github.com/example/pkg	0.5s
panic: runtime error: nil pointer`

	events := ParseTraceString(input)
	errors := 0
	for _, e := range events {
		if e.Kind == TraceError {
			errors++
		}
	}
	if errors < 1 {
		t.Errorf("expected at least 1 error, got %d", errors)
	}
}

func TestParseTraceString_Tests(t *testing.T) {
	input := `--- PASS: TestFoo (0.01s)
--- FAIL: TestBar (0.02s)
ok  	github.com/example/pkg	0.5s
PASS`

	events := ParseTraceString(input)
	tests := 0
	for _, e := range events {
		if e.Kind == TraceTest {
			tests++
		}
	}
	if tests < 2 {
		t.Errorf("expected at least 2 test events, got %d", tests)
	}
}

func TestParseTraceString_Commits(t *testing.T) {
	input := `[main abc1234] feat: add new feature
git commit -m "fix: resolve bug"`

	events := ParseTraceString(input)
	commits := 0
	for _, e := range events {
		if e.Kind == TraceCommit {
			commits++
		}
	}
	if commits < 1 {
		t.Errorf("expected at least 1 commit event, got %d", commits)
	}
}

func TestParseTraceString_ErrorFalsePositives(t *testing.T) {
	// These should NOT be classified as errors
	input := `internal/llm/errors.go
TestErrorHandling
errorhandler.go`

	events := ParseTraceString(input)
	for _, e := range events {
		if e.Kind == TraceError {
			t.Errorf("false positive error: %q", e.Content)
		}
	}
}

func TestParseTraceFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	content := `Read(path="main.go")
Created internal/engine/trace.go
$ go test ./...
--- PASS: TestFoo (0.01s)
ok  	github.com/example/pkg
[main abc1234] feat: add trace parser`

	os.WriteFile(logPath, []byte(content), 0644)

	events, err := ParseTraceFile(logPath)
	if err != nil {
		t.Fatalf("ParseTraceFile: %v", err)
	}
	if len(events) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(events))
	}
}

func TestSummarize(t *testing.T) {
	events := []TraceEvent{
		{Kind: TraceToolCall},
		{Kind: TraceToolCall},
		{Kind: TraceFileEdit},
		{Kind: TraceFileCreate},
		{Kind: TraceError},
		{Kind: TraceTest},
		{Kind: TraceTest},
		{Kind: TraceCommit},
	}
	s := Summarize(events)
	if s.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", s.ToolCalls)
	}
	if s.FileEdits != 1 {
		t.Errorf("FileEdits = %d, want 1", s.FileEdits)
	}
	if s.FileCreates != 1 {
		t.Errorf("FileCreates = %d, want 1", s.FileCreates)
	}
	if s.Errors != 1 {
		t.Errorf("Errors = %d, want 1", s.Errors)
	}
	if s.Tests != 2 {
		t.Errorf("Tests = %d, want 2", s.Tests)
	}
	if s.Commits != 1 {
		t.Errorf("Commits = %d, want 1", s.Commits)
	}
}

func TestParseTraceString_EmptyInput(t *testing.T) {
	events := ParseTraceString("")
	if len(events) != 0 {
		t.Errorf("empty input should produce 0 events, got %d", len(events))
	}
}

func TestParseTraceFile_Missing(t *testing.T) {
	_, err := ParseTraceFile("/nonexistent/log.txt")
	if err == nil {
		t.Fatal("ParseTraceFile should fail on missing file")
	}
}
