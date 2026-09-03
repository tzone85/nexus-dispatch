package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newReviewStores(t *testing.T) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	es, ps, cleanup := newTestStores(t)
	t.Cleanup(cleanup)
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))
	return es, ps
}

func TestReviewer_DiffIsCappedWithExplicitMarker(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{Content: `{"passed": true, "comments": [], "summary": "ok"}`})

	diff := "diff --git a/x b/x\n" + strings.Repeat("+line of code\n", 1000)
	reviewer := engine.NewReviewer(client, "ollama", "m", 4000, es, ps).WithMaxDiffBytes(1024)
	if _, err := reviewer.Review(context.Background(), "s-001", "T", "AC", diff); err != nil {
		t.Fatalf("review: %v", err)
	}

	prompt := client.CallAt(0).Messages[0].Content
	wantMarker := engine.DiffTruncationMarker(len(diff) - 1024)
	if !strings.Contains(prompt, wantMarker) {
		t.Fatalf("prompt missing truncation marker %q", wantMarker)
	}
	if strings.Count(prompt, "+line of code") > 1024/len("+line of code\n")+1 {
		t.Fatal("prompt carries more diff than the byte budget allows")
	}
	if !strings.Contains(prompt, "diff was truncated") {
		t.Fatal("prompt must tell the reviewer the diff may be truncated")
	}
}

func TestReviewer_SmallDiffNotTruncated(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{Content: `{"passed": true, "comments": [], "summary": "ok"}`})
	diff := "diff --git a/x b/x\n+ok\n"
	reviewer := engine.NewReviewer(client, "ollama", "m", 4000, es, ps)
	if _, err := reviewer.Review(context.Background(), "s-001", "T", "AC", diff); err != nil {
		t.Fatalf("review: %v", err)
	}
	prompt := client.CallAt(0).Messages[0].Content
	if strings.Contains(prompt, "[diff truncated") {
		t.Fatal("small diff must not be truncated")
	}
	if !strings.Contains(prompt, diff) {
		t.Fatal("prompt must carry the whole diff")
	}
}

func TestReviewer_DefaultDiffBudget(t *testing.T) {
	if engine.DefaultMaxDiffBytes != 200*1024 {
		t.Fatalf("default budget = %d, want 200 KB", engine.DefaultMaxDiffBytes)
	}
	// Non-positive budgets fall back to the default rather than dropping the diff.
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{Content: `{"passed": true, "comments": [], "summary": "ok"}`})
	reviewer := engine.NewReviewer(client, "ollama", "m", 4000, es, ps).WithMaxDiffBytes(0)
	if _, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n"); err != nil {
		t.Fatalf("review: %v", err)
	}
	if !strings.Contains(client.CallAt(0).Messages[0].Content, "+ok") {
		t.Fatal("diff dropped with zero budget")
	}
}

func TestTruncateDiffForReview(t *testing.T) {
	tests := []struct {
		name      string
		diff      string
		max       int
		wantTrunc bool
	}{
		{"fits", "abc", 3, false},
		{"over", "abcdef", 3, true},
		{"zero budget uses default", strings.Repeat("x", 10), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := engine.TruncateDiffForReview(tt.diff, tt.max)
			if truncated != tt.wantTrunc {
				t.Fatalf("truncated = %v, want %v", truncated, tt.wantTrunc)
			}
			if !truncated && got != tt.diff {
				t.Fatalf("untruncated diff altered: %q", got)
			}
			if truncated {
				want := tt.diff[:tt.max] + "\n" + engine.DiffTruncationMarker(len(tt.diff)-tt.max)
				if got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			}
		})
	}
}

// Fail-closed: with native tool support, a prose-only reply with no tool call
// and no JSON must be a rejection, not a "degraded pass".
func TestReviewer_ToolsPath_NoStructuredVerdictFailsClosed(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "The change looks reasonable overall and I have no blocking concerns.",
	})
	reviewer := engine.NewReviewer(client, "anthropic", "claude", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed {
		t.Fatal("prose without a structured verdict must NOT pass")
	}
	if result.Summary != engine.NoStructuredVerdictFeedback {
		t.Fatalf("summary = %q, want %q", result.Summary, engine.NoStructuredVerdictFeedback)
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(events) != 1 {
		t.Fatalf("expected STORY_REVIEW_FAILED, got %d", len(events))
	}
}

func TestReviewer_ToolsPath_ProseRejectionAlsoFailsClosed(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{Content: "I reject this: it does not build."})
	reviewer := engine.NewReviewer(client, "anthropic", "claude", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed || result.Summary != engine.NoStructuredVerdictFeedback {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestReviewer_TextPath_NoJSONFailsClosed(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{Content: "Looks fine to me."})
	reviewer := engine.NewReviewer(client, "ollama", "llama2", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review must not error on unparseable prose, got %v", err)
	}
	if result.Passed || result.Summary != engine.NoStructuredVerdictFeedback {
		t.Fatalf("unexpected result: %+v", result)
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed})
	if len(events) != 1 {
		t.Fatalf("expected STORY_REVIEW_FAILED, got %d", len(events))
	}
}

func TestReviewer_ToolsPath_JSONTextFallbackStillWorks(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: "```json\n{\"passed\": true, \"comments\": [], \"summary\": \"fine\"}\n```",
	})
	reviewer := engine.NewReviewer(client, "anthropic", "claude", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !result.Passed || result.Summary != "fine" {
		t.Fatalf("JSON text fallback broken: %+v", result)
	}
}

func TestReviewer_ToolsPath_SubmitReviewToolCall(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{Name: "submit_review", Arguments: []byte(`{"verdict":"request_changes","summary":"needs tests","file_comments":[{"file":"a.go","line":3,"severity":"major","message":"no test"}]}`)}},
	})
	reviewer := engine.NewReviewer(client, "openai", "gpt", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed || result.Summary != "needs tests" || len(result.Comments) != 1 || result.Comments[0].Comment != "no test" {
		t.Fatalf("tool verdict not mapped: %+v", result)
	}
}

func TestReviewer_ToolsPath_ContextRequestIsNotAPass(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{Name: "request_more_context", Arguments: []byte(`{"files":["b.go"],"reason":"need caller"}`)}},
	})
	reviewer := engine.NewReviewer(client, "openai", "gpt", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed || !strings.Contains(result.Summary, "need caller") {
		t.Fatalf("context request must not pass: %+v", result)
	}
}

func TestReviewer_ToolsPath_BadToolCallFallsBackToTextThenFailsClosed(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{Name: "submit_review", Arguments: []byte(`{"verdict":"maybe"}`)}},
		Content:   "unsure",
	})
	reviewer := engine.NewReviewer(client, "openai", "gpt", 4000, es, ps)
	result, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.Passed || result.Summary != engine.NoStructuredVerdictFeedback {
		t.Fatalf("invalid tool verdict must fail closed: %+v", result)
	}
}

func TestReviewer_ToolsPath_EmptyResponseIsError(t *testing.T) {
	es, ps := newReviewStores(t)
	client := llm.NewReplayClient(llm.CompletionResponse{})
	reviewer := engine.NewReviewer(client, "openai", "gpt", 4000, es, ps)
	if _, err := reviewer.Review(context.Background(), "s-001", "T", "AC", "diff --git a/x b/x\n+ok\n"); err == nil {
		t.Fatal("empty response must be an error, not a verdict")
	}
}
