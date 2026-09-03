package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// recordingClient captures the last request and replays a canned response.
type recordingClient struct {
	resp  llm.CompletionResponse
	err   error
	calls int
	last  llm.CompletionRequest
}

func (r *recordingClient) Complete(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
	r.calls++
	r.last = req
	return r.resp, r.err
}

func wrapSentinel(body string) string {
	return resolvedFileSentinelStart + "\n" + body + "\n" + resolvedFileSentinelEnd
}

func newFileStore(t *testing.T) *state.FileStore {
	t.Helper()
	es, err := state.NewFileStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	return es
}

func TestResolveTextConflict_OversizedFileIsNeverSentOrWritten(t *testing.T) {
	dir := t.TempDir()
	original := "<<<<<<< HEAD\n" + strings.Repeat("a\n", maxConflictContentBytes/2) +
		"=======\n" + strings.Repeat("b\n", maxConflictContentBytes/2) + ">>>>>>> branch\n"
	if len(original) <= maxConflictContentBytes {
		t.Fatalf("fixture must exceed the limit")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("package x")}}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	err := cr.resolveTextConflict(context.Background(), "s-1", dir, "big.go", false)
	if err == nil {
		t.Fatal("expected error for oversized conflict")
	}
	var tooLarge *ConflictTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ConflictTooLargeError, got %T: %v", err, err)
	}
	if tooLarge.File != "big.go" || tooLarge.Size != len(original) || tooLarge.Limit != maxConflictContentBytes {
		t.Errorf("unexpected error fields: %+v", tooLarge)
	}
	if client.calls != 0 {
		t.Errorf("LLM must not be called for oversized files, got %d call(s)", client.calls)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "big.go"))
	if string(got) != original {
		t.Error("oversized file was modified on disk")
	}

	events, _ := es.List(state.EventFilter{StoryID: "s-1"})
	if len(events) != 1 || events[0].Type != state.EventStoryConflictEscalated {
		t.Fatalf("expected one STORY_CONFLICT_ESCALATED event, got %+v", events)
	}
	payload := state.DecodePayload(events[0].Payload)
	if payload["reason"] != conflictReasonFileTooLarge {
		t.Errorf("reason = %v, want %q", payload["reason"], conflictReasonFileTooLarge)
	}
	if payload["outcome"] != "file_too_large" || payload["file"] != "big.go" {
		t.Errorf("unexpected payload: %v", payload)
	}
}

func TestResolveTextConflict_MarkdownWithFencesRoundTrips(t *testing.T) {
	dir := t.TempDir()
	conflicted := "# Title\n\n<<<<<<< HEAD\nIntro A\n=======\nIntro B\n>>>>>>> branch\n\n```go\nfunc a() {}\n```\n\nMore prose.\n\n```sh\nmake test\n```\n"
	resolved := "# Title\n\nIntro A and Intro B\n\n```go\nfunc a() {}\n```\n\nMore prose.\n\n```sh\nmake test\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &recordingClient{resp: llm.CompletionResponse{Content: "Here you go:\n" + wrapSentinel(resolved) + "\nDone."}}
	cr := NewConflictResolver(client, "m", 100, nil)

	if err := cr.resolveTextConflict(context.Background(), "s-1", dir, "README.md", false); err != nil {
		t.Fatalf("resolveTextConflict: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	if string(got) != resolved {
		t.Errorf("markdown did not round-trip intact:\n%q", string(got))
	}
	if !strings.Contains(client.last.Messages[0].Content, resolvedFileSentinelStart) {
		t.Error("prompt must instruct the model to use the sentinel")
	}
	if !strings.Contains(client.last.Messages[0].Content, conflicted) {
		t.Error("prompt must carry the full (untruncated) conflicted content")
	}
}

func TestResolveTextConflict_LeftoverMarkersAreRejectedNotWritten(t *testing.T) {
	dir := t.TempDir()
	conflicted := "package x\n<<<<<<< HEAD\nvar a = 1\n=======\nvar a = 2\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel(conflicted)}}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	err := cr.resolveTextConflict(context.Background(), "s-1", dir, "x.go", false)
	var esc *ConflictEscalatedError
	if !errors.As(err, &esc) {
		t.Fatalf("expected *ConflictEscalatedError, got %T: %v", err, err)
	}
	if !strings.Contains(esc.Reason, "conflict markers") {
		t.Errorf("reason = %q", esc.Reason)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.go"))
	if string(got) != conflicted {
		t.Error("file with leftover markers must not be written")
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 1 {
		t.Fatalf("expected escalation event, got %d", len(events))
	}
}

func TestResolveTextConflict_ShrunkenOutputIsRejected(t *testing.T) {
	dir := t.TempDir()
	conflicted := "<<<<<<< HEAD\n" + strings.Repeat("line a\n", 50) + "=======\n" + strings.Repeat("line b\n", 50) + ">>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("line a\n")}}
	cr := NewConflictResolver(client, "m", 100, nil)

	err := cr.resolveTextConflict(context.Background(), "s-1", dir, "x.txt", false)
	var esc *ConflictEscalatedError
	if !errors.As(err, &esc) || !strings.Contains(esc.Reason, "shrank") {
		t.Fatalf("expected shrink rejection, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.txt"))
	if string(got) != conflicted {
		t.Error("shrunken output must not be written")
	}
}

func TestResolveTextConflict_TechLeadRescuesSeniorFailure(t *testing.T) {
	dir := t.TempDir()
	conflicted := "<<<<<<< HEAD\nA\n=======\nB\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	senior := &recordingClient{err: errors.New("boom")}
	techLead := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("A\nB\n")}}
	es := newFileStore(t)
	cr := NewConflictResolverWithTechLead(senior, "s", techLead, "tl", 100, nil, es)

	if err := cr.resolveTextConflict(context.Background(), "s-1", dir, "x.txt", false); err != nil {
		t.Fatalf("expected tech lead rescue, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.txt"))
	if string(got) != "A\nB\n" {
		t.Errorf("unexpected content %q", got)
	}
	if !strings.Contains(techLead.last.Messages[0].Content, resolvedFileSentinelStart) {
		t.Error("tech lead prompt must use the sentinel")
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 1 || state.DecodePayload(events[0].Payload)["outcome"] != "tech_lead_resolved" {
		t.Errorf("expected tech_lead_resolved escalation event, got %+v", events)
	}
}

func TestResolveTextConflict_TechLeadFailureEscalates(t *testing.T) {
	dir := t.TempDir()
	conflicted := "<<<<<<< HEAD\nA\n=======\nB\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	senior := &recordingClient{resp: llm.CompletionResponse{Content: "Conflict resolved. Kept both sides."}}
	techLead := &recordingClient{err: &llm.APIError{StatusCode: 401, Message: "bad key"}}
	es := newFileStore(t)
	cr := NewConflictResolverWithTechLead(senior, "s", techLead, "tl", 100, nil, es)

	err := cr.resolveTextConflict(context.Background(), "s-1", dir, "x.txt", true)
	var esc *ConflictEscalatedError
	if !errors.As(err, &esc) {
		t.Fatalf("expected *ConflictEscalatedError, got %T: %v", err, err)
	}
	if !llm.IsFatalAPIError(err) {
		t.Error("fatal API error must remain detectable through the escalation error")
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 1 || state.DecodePayload(events[0].Payload)["outcome"] != "tech_lead_failed" {
		t.Errorf("expected tech_lead_failed event, got %+v", events)
	}
}

func TestResolveTextConflict_NeedsTechLeadWithoutOneUsesSenior(t *testing.T) {
	dir := t.TempDir()
	conflicted := "<<<<<<< HEAD\nA\n=======\nB\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	senior := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("A\nB\n")}}
	cr := NewConflictResolver(senior, "s", 100, nil)
	if err := cr.resolveTextConflict(context.Background(), "s-1", dir, "x.txt", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.txt"))
	if string(got) != "A\nB\n" {
		t.Errorf("unexpected content %q", got)
	}
}

func TestResolveTextConflict_MissingFile(t *testing.T) {
	cr := NewConflictResolver(&recordingClient{}, "s", 100, nil)
	err := cr.resolveTextConflict(context.Background(), "s-1", t.TempDir(), "nope.txt", false)
	if err == nil || !strings.Contains(err.Error(), "read conflicted file") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestExtractResolvedFileContent_SentinelAndLargestFence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "sentinel wins over fences inside it",
			in:   "preamble\n" + wrapSentinel("# Doc\n```go\nx\n```\ntail") + "\nignored ```json\n{}\n```",
			want: "# Doc\n```go\nx\n```\ntail",
		},
		{
			name: "largest fenced block, not the first",
			in:   "Kept both.\n```json\n{}\n```\nFull file:\n```go\npackage main\n\nfunc main() {}\n```\n",
			want: "package main\n\nfunc main() {}",
		},
		{
			name: "unterminated fence falls back to strip",
			in:   "```go\npackage main\n",
			want: "package main",
		},
		{
			name: "plain content passes through",
			in:   "package main\n",
			want: "package main",
		},
		{
			name: "sentinel start without end falls back to fences",
			in:   resolvedFileSentinelStart + "\n```go\npackage y\n```",
			want: "package y",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractResolvedFileContent(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateResolvedContent(t *testing.T) {
	original := "<<<<<<< HEAD\n" + strings.Repeat("x\n", 50) + "=======\n" + strings.Repeat("x\n", 50) + ">>>>>>> b\n"
	tests := []struct {
		name     string
		resolved string
		wantErr  string
	}{
		{"ok", strings.Repeat("x\n", 60), ""},
		{"leftover start marker", "<<<<<<< HEAD\n" + strings.Repeat("x\n", 60), "conflict markers"},
		{"leftover end marker", strings.Repeat("x\n", 60) + ">>>>>>> b\n", "conflict markers"},
		{"too small", strings.Repeat("x\n", 10), "shrank"},
		{"empty", "", "empty"},
		{"chatter", "Conflict resolved. Kept both sides.\n" + strings.Repeat("x\n", 60), "commentary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResolvedContent(original, tt.resolved)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConflictErrors_Messages(t *testing.T) {
	tl := &ConflictTooLargeError{File: "a.go", Size: 30000, Limit: 24576}
	if !strings.Contains(tl.Error(), "a.go") || !strings.Contains(tl.Error(), "30000") {
		t.Errorf("ConflictTooLargeError.Error() = %q", tl.Error())
	}
	esc := &ConflictEscalatedError{File: "a.go", Reason: "why", Err: errors.New("inner")}
	if esc.Error() != "conflict in a.go escalated: why" || esc.Unwrap().Error() != "inner" {
		t.Errorf("ConflictEscalatedError = %q", esc.Error())
	}
	if (&ConflictEscalatedError{File: "a.go", Reason: "r"}).Unwrap() != nil {
		t.Error("nil inner must unwrap to nil")
	}
}

// tlProjStore is a minimal state.ProjectionStore for Tech Lead context tests.
type tlProjStore struct {
	story    state.Story
	storyErr error
	req      state.Requirement
	reqErr   error
	stories  []state.Story
	listErr  error
}

func (p *tlProjStore) Project(state.Event) error                        { return nil }
func (p *tlProjStore) GetRequirement(string) (state.Requirement, error) { return p.req, p.reqErr }
func (p *tlProjStore) GetStory(string) (state.Story, error)             { return p.story, p.storyErr }
func (p *tlProjStore) ListStories(state.StoryFilter) ([]state.Story, error) {
	return p.stories, p.listErr
}
func (p *tlProjStore) Close() error { return nil }

func TestBuildTechLeadContext_FullStore(t *testing.T) {
	ps := &tlProjStore{
		story: state.Story{ID: "s-1", ReqID: "r-1", Title: "Story One", AcceptanceCriteria: "AC"},
		req:   state.Requirement{ID: "r-1", Title: "Req", Description: "Desc"},
		stories: []state.Story{
			{ID: "s-1", Title: "Story One"},
			{ID: "s-2", Title: "Sibling Two"},
		},
	}
	cr := NewConflictResolverWithTechLead(nil, "", &recordingClient{}, "tl", 10, ps, nil)
	got := cr.buildTechLeadContext(context.Background(), "s-1", t.TempDir(), "x.go")
	if got.storyTitle != "Story One" || got.storyAcceptance != "AC" {
		t.Errorf("story fields: %+v", got)
	}
	if got.requirementTitle != "Req" || got.requirementText != "Desc" {
		t.Errorf("requirement fields: %+v", got)
	}
	if len(got.siblingStoryTitles) != 1 || got.siblingStoryTitles[0] != "Sibling Two" {
		t.Errorf("siblings = %v", got.siblingStoryTitles)
	}
	if got.fileHistory != nil {
		t.Errorf("non-repo dir must yield no history, got %v", got.fileHistory)
	}
}

func TestBuildTechLeadContext_StoreErrors(t *testing.T) {
	cr := &ConflictResolver{projStore: &tlProjStore{storyErr: errors.New("nope")}}
	if got := cr.buildTechLeadContext(context.Background(), "s-1", "", "f"); got.storyTitle != "" || got.siblingStoryTitles != nil {
		t.Errorf("story error must yield empty context, got %+v", got)
	}

	cr = &ConflictResolver{projStore: &tlProjStore{
		story:   state.Story{ID: "s-1", ReqID: "r-1", Title: "T"},
		reqErr:  errors.New("no req"),
		listErr: errors.New("no list"),
	}}
	got := cr.buildTechLeadContext(context.Background(), "s-1", "", "f")
	if got.storyTitle != "T" || got.requirementTitle != "" || got.siblingStoryTitles != nil {
		t.Errorf("partial context wrong: %+v", got)
	}
}

func TestResolveFile_NilClients(t *testing.T) {
	cr := &ConflictResolver{}
	if _, err := cr.resolveFile(context.Background(), "f", "c"); err == nil {
		t.Error("expected error without senior client")
	}
	if _, err := cr.resolveFileTechLead(context.Background(), "f", "c", techLeadContext{}); err == nil {
		t.Error("expected error without tech lead client")
	}
}

func TestResolveFileTechLead_PromptCarriesContext(t *testing.T) {
	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("merged\n")}}
	cr := &ConflictResolver{techLeadClient: client, techLeadModel: "tl", maxTokens: 5}
	tlCtx := techLeadContext{
		requirementTitle:   "Req Title",
		siblingStoryTitles: []string{"Sib A", "Sib B"},
		fileHistory:        []string{"fix: one", "feat: two"},
	}
	got, err := cr.resolveFileTechLead(context.Background(), "f.go", "<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x\n", tlCtx)
	if err != nil || got != "merged\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	p := client.last.Messages[0].Content
	for _, want := range []string{"Req Title", "Sib A, Sib B", "fix: one\n  feat: two", resolvedFileSentinelEnd} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if client.last.Model != "tl" || client.last.MaxTokens != 5 {
		t.Errorf("request not built from tech lead config: %+v", client.last)
	}
}

func TestCompleteResolution_NonFatalErrorPassesThrough(t *testing.T) {
	client := &recordingClient{err: &llm.APIError{StatusCode: 500, Message: "boom", Retryable: true}}
	cr := &ConflictResolver{llmClient: client}
	_, err := cr.resolveFile(context.Background(), "f", "c")
	if !llm.IsRetryable(err) || strings.Contains(err.Error(), "fatal") {
		t.Errorf("non-fatal error must pass through unchanged, got %v", err)
	}
}

func TestHandleBinaryConflict_GitErrors(t *testing.T) {
	dir := t.TempDir() // not a git repo → both git commands fail
	cr := &ConflictResolver{}
	if err := cr.handleBinaryConflict("s", dir, filepath.Join(dir, "server"), "server"); err == nil || !strings.Contains(err.Error(), "git rm") {
		t.Errorf("expected git rm error, got %v", err)
	}
	if err := cr.handleBinaryConflict("s", dir, filepath.Join(dir, "logo.png"), "logo.png"); err == nil || !strings.Contains(err.Error(), "checkout --ours") {
		t.Errorf("expected checkout error, got %v", err)
	}
}
