package runtime

import (
	"strings"
	"testing"
)

func TestExtractInlineToolCalls_SingleCall(t *testing.T) {
	in := `{"name": "write_file", "arguments": {"path": "main.go", "content": "package main"}}`
	got := extractInlineToolCalls(in)
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1", len(got))
	}
	if got[0].Name != "write_file" {
		t.Errorf("name = %q", got[0].Name)
	}
	if !strings.Contains(string(got[0].Arguments), "main.go") {
		t.Errorf("arguments missing path: %s", string(got[0].Arguments))
	}
}

func TestExtractInlineToolCalls_MultipleCalls(t *testing.T) {
	in := `{"name": "run_command", "arguments": {"command": "go mod init test"}}
{"name": "write_file", "arguments": {"path": "main.go", "content": "package main"}}
{"name": "task_complete", "arguments": {"summary": "done"}}`
	got := extractInlineToolCalls(in)
	if len(got) != 3 {
		t.Fatalf("got %d calls, want 3", len(got))
	}
	if got[0].Name != "run_command" || got[1].Name != "write_file" || got[2].Name != "task_complete" {
		t.Errorf("names = %q %q %q", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestExtractInlineToolCalls_HandlesCodeFences(t *testing.T) {
	in := "```json\n" +
		`{"name": "write_file", "arguments": {"path": "go.mod", "content": "module test"}}` +
		"\n```"
	got := extractInlineToolCalls(in)
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1", len(got))
	}
	if got[0].Name != "write_file" {
		t.Errorf("name = %q", got[0].Name)
	}
}

func TestExtractInlineToolCalls_NoToolCalls(t *testing.T) {
	for _, in := range []string{
		"",
		"plain text without JSON",
		`{"foo": "bar"}`,
		`{"name": ""}`,
	} {
		got := extractInlineToolCalls(in)
		if len(got) != 0 {
			t.Errorf("input %q returned %d calls, want 0", in, len(got))
		}
	}
}

func TestExtractInlineToolCalls_NestedArguments(t *testing.T) {
	in := `{"name": "edit_file", "arguments": {"path": "x.go", "old_text": "func {", "new_text": "func {\n\treturn nil\n}"}}`
	got := extractInlineToolCalls(in)
	if len(got) != 1 {
		t.Fatalf("got %d calls", len(got))
	}
	if !strings.Contains(string(got[0].Arguments), "old_text") {
		t.Errorf("arguments lost: %s", string(got[0].Arguments))
	}
}

func TestExtractInlineToolCalls_MalformedSkippedGracefully(t *testing.T) {
	in := `{"name": "run_command", "arguments": {"command": "go build ./..."}}
{this is not valid json}
{"name": "write_file", "arguments": {"path": "x.go", "content": ""}}`
	got := extractInlineToolCalls(in)
	if len(got) < 2 {
		t.Errorf("expected at least 2 valid calls, got %d", len(got))
	}
}

func TestMatchBalancedBrace(t *testing.T) {
	for _, tc := range []struct {
		in    string
		start int
		want  int
	}{
		{`{}`, 0, 1},
		{`{a}`, 0, 2},
		{`{{}}`, 0, 3},
		{`{"a":"}"}`, 0, 8},
		{`prefix{x}suffix`, 6, 8},
		{`{unbalanced`, 0, -1},
		{`x{a}y`, 1, 3},
	} {
		if got := matchBalancedBrace(tc.in, tc.start); got != tc.want {
			t.Errorf("matchBalancedBrace(%q, %d) = %d, want %d", tc.in, tc.start, got, tc.want)
		}
	}
}
