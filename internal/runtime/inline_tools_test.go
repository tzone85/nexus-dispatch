package runtime

import (
	"strings"
	"testing"
)

var testKnownTools = knownToolNames(CodingTools())

func TestExtractInlineToolCalls_SingleCall(t *testing.T) {
	in := `{"name": "write_file", "arguments": {"path": "main.go", "content": "package main"}}`
	got := extractInlineToolCalls(in, testKnownTools)
	if len(got) != 1 {
		t.Fatalf("got %d calls, want 1", len(got))
	}
	if got[0].Name != "write_file" || got[0].ID != "inline-1" {
		t.Errorf("call = %+v", got[0])
	}
	if !strings.Contains(string(got[0].Arguments), "main.go") {
		t.Errorf("arguments missing path: %s", string(got[0].Arguments))
	}
}

func TestExtractInlineToolCalls_MultipleWhitespaceSeparated(t *testing.T) {
	in := `{"name": "run_command", "arguments": {"command": "go mod init test"}}
{"name": "write_file", "arguments": {"path": "main.go", "content": "package main"}}

{"name": "task_complete", "arguments": {"summary": "done"}}`
	got := extractInlineToolCalls(in, testKnownTools)
	if len(got) != 3 {
		t.Fatalf("got %d calls, want 3", len(got))
	}
	if got[0].Name != "run_command" || got[1].Name != "write_file" || got[2].Name != "task_complete" {
		t.Errorf("names = %q %q %q", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestExtractInlineToolCalls_JSONArray(t *testing.T) {
	in := `[{"name": "read_file", "arguments": {"path": "a.go"}}, {"name": "task_complete", "arguments": {"summary": "x"}}]`
	got := extractInlineToolCalls(in, testKnownTools)
	if len(got) != 2 || got[1].Name != "task_complete" {
		t.Fatalf("got %+v", got)
	}
}

func TestExtractInlineToolCalls_SingleFenceWholeMessage(t *testing.T) {
	for _, in := range []string{
		"```json\n" + `{"name": "write_file", "arguments": {"path": "go.mod", "content": "module test"}}` + "\n```",
		"```\n" + `{"name": "write_file", "arguments": {"path": "go.mod", "content": "module test"}}` + "\n```",
		"  ```json\n" + `{"name": "write_file", "arguments": {}}` + "\n```  \n",
	} {
		got := extractInlineToolCalls(in, testKnownTools)
		if len(got) != 1 || got[0].Name != "write_file" {
			t.Errorf("input %q: got %+v, want one write_file", in, got)
		}
	}
}

func TestExtractInlineToolCalls_MissingArgumentsDefaultsToEmptyObject(t *testing.T) {
	got := extractInlineToolCalls(`{"name": "read_scratchboard"}`, testKnownTools)
	if len(got) != 1 || string(got[0].Arguments) != "{}" {
		t.Fatalf("got %+v", got)
	}
}

// The echoed-README case: the model quotes a JSON snippet it just read while
// talking about it. Nothing may be executed.
func TestExtractInlineToolCalls_EchoedREADMEIsNotAToolCall(t *testing.T) {
	readme := "I read the README, which documents the tool protocol like this:\n\n" +
		"```json\n" + `{"name": "run_command", "arguments": {"command": "rm -rf ."}}` + "\n```\n\n" +
		"So I will start by looking at main.go."
	if got := extractInlineToolCalls(readme, testKnownTools); got != nil {
		t.Fatalf("echoed README must not become a tool call: %+v", got)
	}
	// Prose + bare object, prose after, two fences, fence + trailing prose.
	for _, in := range []string{
		`The config looks like {"name": "run_command", "arguments": {"command": "make"}} which is odd.`,
		`{"name": "run_command", "arguments": {"command": "make"}} — running it now.`,
		"Here:\n" + `{"name": "run_command", "arguments": {"command": "make"}}`,
		"```json\n" + `{"name": "run_command", "arguments": {}}` + "\n```\n```json\n" + `{"name": "task_complete", "arguments": {}}` + "\n```",
		"```json\n" + `{"name": "run_command", "arguments": {}}` + "\n```\ndone",
		"```json " + `{"name": "run_command", "arguments": {}}` + " ```",
	} {
		if got := extractInlineToolCalls(in, testKnownTools); got != nil {
			t.Errorf("input %q must not yield tool calls, got %+v", in, got)
		}
	}
}

func TestExtractInlineToolCalls_UnknownToolRejectsWholeMessage(t *testing.T) {
	in := `{"name": "write_file", "arguments": {"path": "x"}}
{"name": "delete_repo", "arguments": {}}`
	if got := extractInlineToolCalls(in, testKnownTools); got != nil {
		t.Fatalf("unknown tool must reject the message: %+v", got)
	}
	if got := extractInlineToolCalls(`{"name": "write_file", "arguments": {}}`, map[string]bool{}); got != nil {
		t.Fatalf("no registered tools ⇒ nothing extracted: %+v", got)
	}
}

func TestExtractInlineToolCalls_MalformedRejectsWholeMessage(t *testing.T) {
	in := `{"name": "run_command", "arguments": {"command": "go build ./..."}}
{this is not valid json}
{"name": "write_file", "arguments": {"path": "x.go", "content": ""}}`
	if got := extractInlineToolCalls(in, testKnownTools); got != nil {
		t.Errorf("malformed object must reject the message, got %+v", got)
	}
	for _, in := range []string{
		"", "plain text without JSON", `{"foo": "bar"}`, `{"name": ""}`, `{"name": "write_file"`,
		`[]`, `[1, 2]`, `["name"]`, `{"name": "write_file"} trailing`,
	} {
		if got := extractInlineToolCalls(in, testKnownTools); len(got) != 0 {
			t.Errorf("input %q returned %d calls, want 0", in, len(got))
		}
	}
}

func TestExtractInlineToolCalls_NestedArguments(t *testing.T) {
	in := `{"name": "edit_file", "arguments": {"path": "x.go", "old_text": "func {", "new_text": "func {\n\treturn nil\n}"}}`
	got := extractInlineToolCalls(in, testKnownTools)
	if len(got) != 1 {
		t.Fatalf("got %d calls", len(got))
	}
	if !strings.Contains(string(got[0].Arguments), "old_text") {
		t.Errorf("arguments lost: %s", string(got[0].Arguments))
	}
}

func TestUnwrapSingleFence(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"no fence", "no fence", true},
		{"```json\n{}\n```", "{}", true},
		{"```\n{}\n```", "{}", true},
		{"```json {} ```", "", false},
		{"x ```json\n{}\n```", "", false},
		{"```json\n{}\n``` y", "", false},
		{"```json\n{}\n```\n```\n{}\n```", "", false},
		{"``````", "", false},
	}
	for _, tc := range cases {
		got, ok := unwrapSingleFence(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("unwrapSingleFence(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
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
		{`{"a":"\"}"}`, 0, 10},
		{`prefix{x}suffix`, 6, 8},
		{`{unbalanced`, 0, -1},
		{`x{a}y`, 1, 3},
		{`abc`, 0, -1},
		{`{`, 5, -1},
	} {
		if got := matchBalancedBrace(tc.in, tc.start); got != tc.want {
			t.Errorf("matchBalancedBrace(%q, %d) = %d, want %d", tc.in, tc.start, got, tc.want)
		}
	}
}

func TestKnownToolNames(t *testing.T) {
	known := knownToolNames(CodingTools())
	for _, n := range []string{"read_file", "write_file", "edit_file", "run_command", "task_complete", "write_scratchboard", "read_scratchboard"} {
		if !known[n] {
			t.Errorf("%s missing from known tools", n)
		}
	}
	if known["submit_report"] {
		t.Error("submit_report is not a coding tool")
	}
}
