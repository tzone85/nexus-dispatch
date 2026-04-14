package engine

import "testing"

func TestStripCodeFences_NoFences(t *testing.T) {
	input := `{"id": "s-001", "title": "test"}`
	got := stripCodeFences(input)
	if got != input {
		t.Errorf("expected unchanged output, got %q", got)
	}
}

func TestStripCodeFences_JSONFence(t *testing.T) {
	input := "```json\n{\"id\": \"s-001\"}\n```"
	want := `{"id": "s-001"}`
	got := stripCodeFences(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestStripCodeFences_PlainFence(t *testing.T) {
	input := "```\nhello world\n```"
	want := "hello world"
	got := stripCodeFences(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestStripCodeFences_MultipleLinesInFence(t *testing.T) {
	input := "```go\npackage main\n\nfunc main() {}\n```"
	want := "package main\n\nfunc main() {}"
	got := stripCodeFences(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestStripCodeFences_WhitespaceAround(t *testing.T) {
	input := "  \n```json\n{\"key\": \"val\"}\n```\n  "
	want := `{"key": "val"}`
	got := stripCodeFences(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestStripCodeFences_EmptyString(t *testing.T) {
	got := stripCodeFences("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
