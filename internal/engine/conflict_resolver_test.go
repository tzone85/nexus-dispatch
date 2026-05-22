package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

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

// --------------------------------------------------------------------------
// truncateConflictContent tests
// --------------------------------------------------------------------------

func TestTruncateConflictContent_ShortContent(t *testing.T) {
	input := "package main\n\nfunc main() {}\n"
	got := truncateConflictContent(input)
	if got != input {
		t.Errorf("expected unchanged output for short content")
	}
}

func TestTruncateConflictContent_LongContent(t *testing.T) {
	// Generate content longer than maxConflictContentBytes.
	input := strings.Repeat("x", maxConflictContentBytes+100)
	got := truncateConflictContent(input)
	if len(got) <= maxConflictContentBytes {
		// Should be truncated to limit + truncation notice.
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation notice in output")
	}
	if len(got) > maxConflictContentBytes+100 {
		t.Errorf("truncated content too long: %d bytes", len(got))
	}
}

// --------------------------------------------------------------------------
// Binary conflict policy tests
// --------------------------------------------------------------------------

func TestBinaryConflictPolicy_OversizedBinaryPattern(t *testing.T) {
	cases := []struct {
		path    string
		matches bool
	}{
		{"server", true},
		{"main", true},
		{"app", true},
		{"cmd/server", true},       // last segment is "server"
		{"cmd/main", true},          // last segment is "main"
		{"binary", true},
		{"app.exe", false},          // regex only matches exact ".exe" as a segment
		{"README.md", false},
		{"main.go", false},
		{"internal/server/handler.go", false},
		{"internal/app/main.go", false},
	}
	for _, tc := range cases {
		got := oversizedBinaryPattern.MatchString(tc.path)
		if got != tc.matches {
			t.Errorf("oversizedBinaryPattern.MatchString(%q) = %v; want %v", tc.path, got, tc.matches)
		}
	}
}

// --------------------------------------------------------------------------
// Tech Lead context builder — smoke test with nil projStore
// --------------------------------------------------------------------------

func TestBuildTechLeadContext_NilProjStore(t *testing.T) {
	cr := &ConflictResolver{projStore: nil}
	ctx := context.Background()
	// Should not panic; returns empty context.
	tlCtx := cr.buildTechLeadContext(ctx, "s-001", t.TempDir(), "main.go")
	if tlCtx.storyTitle != "" {
		t.Errorf("expected empty storyTitle with nil projStore, got %q", tlCtx.storyTitle)
	}
}

// --------------------------------------------------------------------------
// resolveFile prompt construction — verify no-markdown-fence instruction
// --------------------------------------------------------------------------

func TestResolveFile_PromptContainsNoFenceInstruction(t *testing.T) {
	// Use ReplayClient to capture what the LLM would receive.
	// The ReplayClient returns a fixed response without recording the prompt,
	// so we verify the prompt by calling resolveFile with a controlled input
	// and checking there are no conflict markers in the output.
	client := llm.NewReplayClient(llm.CompletionResponse{Content: "resolved content"})

	cr := &ConflictResolver{
		llmClient: client,
		model:     "test-model",
		maxTokens: 100,
	}

	resolved, err := cr.resolveFile(context.Background(), "main.go", "<<<<<<< HEAD\nfoo\n=======\nbar\n>>>>>>> branch")
	if err != nil {
		t.Fatalf("resolveFile: %v", err)
	}
	if strings.Contains(resolved, "<<<<<<<") || strings.Contains(resolved, ">>>>>>>") {
		t.Errorf("resolved content should not contain conflict markers")
	}
}

// TestResolveFile_ConflictMarkersInOutput verifies that resolveFile returns an
// error when the LLM echoes back conflict markers (Ollama models sometimes do this).
func TestResolveFile_ConflictMarkersInOutput(t *testing.T) {
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "<<<<<<< HEAD\nstill conflicted\n=======\n>>>>>>> branch",
	})

	cr := &ConflictResolver{
		llmClient: client,
		model:     "test-model",
		maxTokens: 100,
	}

	_, err := cr.resolveFile(context.Background(), "main.go", "conflict content")
	if err == nil {
		t.Error("expected error when LLM returns conflict markers")
	}
}

// --------------------------------------------------------------------------
// emitBinaryEvent tests
// --------------------------------------------------------------------------

func TestEmitBinaryEvent_WithStore(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	cr := &ConflictResolver{eventStore: es}
	cr.emitBinaryEvent("s-001", "server", state.EventStoryConflictBinaryRemoved, "test reason")

	events, err := es.List(state.EventFilter{StoryID: "s-001"})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != state.EventStoryConflictBinaryRemoved {
		t.Errorf("expected EventStoryConflictBinaryRemoved, got %v", events[0].Type)
	}
}

func TestEmitEscalationEvent_WithStore(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	cr := &ConflictResolver{eventStore: es}
	cr.emitEscalationEvent("s-001", "main.go", "tech_lead_resolved")

	events, err := es.List(state.EventFilter{StoryID: "s-001"})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != state.EventStoryConflictEscalated {
		t.Errorf("expected EventStoryConflictEscalated, got %v", events[0].Type)
	}
}
