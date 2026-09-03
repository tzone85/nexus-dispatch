package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// conflictRepo is a throwaway git repo with a "main" branch and a "story"
// branch whose commits conflict with main.
type conflictRepo struct {
	t   *testing.T
	dir string
}

func (r *conflictRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *conflictRepo) write(name string, data []byte) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *conflictRepo) commitAll(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
}

func (r *conflictRepo) read(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *conflictRepo) rebaseInProgress() bool {
	_, err := os.Stat(filepath.Join(r.dir, ".git", "rebase-merge"))
	return err == nil
}

// newConflictRepo builds: main: base -> mainEdit ; story (from base): storyEdit.
// files maps filename -> [base, main, story] content.
func newConflictRepo(t *testing.T, files map[string][3][]byte) *conflictRepo {
	t.Helper()
	r := &conflictRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@t")
	r.git("config", "user.name", "t")
	for name, v := range files {
		r.write(name, v[0])
	}
	r.commitAll("base")
	r.git("checkout", "-q", "-b", "story")
	for name, v := range files {
		r.write(name, v[2])
	}
	r.commitAll("story edit")
	r.git("checkout", "-q", "main")
	for name, v := range files {
		r.write(name, v[1])
	}
	r.commitAll("main edit")
	r.git("checkout", "-q", "story")
	return r
}

func textConflict(name string) map[string][3][]byte {
	return map[string][3][]byte{name: {
		[]byte("package x\n\nvar v = 0\n"),
		[]byte("package x\n\nvar v = 1 // main\n"),
		[]byte("package x\n\nvar v = 2 // story\n"),
	}}
}

func TestRebaseWithResolution_CleanRebase(t *testing.T) {
	r := newConflictRepo(t, map[string][3][]byte{"a.txt": {[]byte("a\n"), []byte("a\n"), []byte("a\nstory\n")}})
	client := &recordingClient{}
	cr := NewConflictResolver(client, "m", 100, nil)
	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("clean rebase failed: %v", err)
	}
	if client.calls != 0 {
		t.Error("LLM must not be consulted for a clean rebase")
	}
}

func TestRebaseWithResolution_NonConflictError(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	cr := NewConflictResolver(&recordingClient{}, "m", 100, nil)
	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "no-such-branch")
	if err == nil || !strings.Contains(err.Error(), "git rebase") {
		t.Fatalf("expected rebase error, got %v", err)
	}
}

func TestRebaseWithResolution_ResolvesTextConflict(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	resolved := "package x\n\nvar v = 3 // main+story\n"
	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel(resolved)}}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("RebaseWithResolution: %v", err)
	}
	if got := r.read("x.go"); got != resolved {
		t.Errorf("file = %q, want resolved content", got)
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be finished")
	}
	if !strings.Contains(client.last.Messages[0].Content, "<<<<<<<") {
		t.Error("prompt must contain the conflicted content")
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryProgress})
	if len(events) != 1 || state.DecodePayload(events[0].Payload)["action"] != "conflicts_resolved" {
		t.Errorf("expected conflicts_resolved progress event, got %+v", events)
	}
}

func TestRebaseWithResolution_OversizedFileAbortsAndEscalates(t *testing.T) {
	big := func(tag string) []byte {
		return []byte("header\n" + strings.Repeat(tag+" line\n", maxConflictContentBytes/8))
	}
	files := map[string][3][]byte{"big.txt": {[]byte("header\n"), big("main"), big("story")}}
	r := newConflictRepo(t, files)
	storyContent := r.read("big.txt")

	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("tiny")}}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main")
	var tooLarge *ConflictTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ConflictTooLargeError, got %T: %v", err, err)
	}
	if client.calls != 0 {
		t.Error("oversized file must not be sent to the LLM")
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be aborted for a human")
	}
	if got := r.read("big.txt"); got != storyContent {
		t.Error("story branch content must be restored intact after abort")
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 1 || state.DecodePayload(events[0].Payload)["reason"] != conflictReasonFileTooLarge {
		t.Errorf("expected file-too-large escalation, got %+v", events)
	}
}

func TestRebaseWithResolution_LLMFailureAborts(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	storyContent := r.read("x.go")
	client := &recordingClient{err: &llm.APIError{StatusCode: 401, Message: "invalid x-api-key"}}
	cr := NewConflictResolver(client, "m", 100, newFileStore(t))

	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main")
	if !llm.IsFatalAPIError(err) {
		t.Fatalf("fatal API error must surface, got %v", err)
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be aborted")
	}
	if r.read("x.go") != storyContent {
		t.Error("file must be untouched after abort")
	}
}

func TestRebaseWithResolution_LockFileTakesOurs(t *testing.T) {
	files := map[string][3][]byte{"go.sum": {[]byte("base\n"), []byte("main\n"), []byte("story\n")}}
	r := newConflictRepo(t, files)
	client := &recordingClient{}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("RebaseWithResolution: %v", err)
	}
	if client.calls != 0 {
		t.Error("lock files must never reach the LLM")
	}
	// During a rebase --ours is the branch being rebased ONTO (main).
	if got := r.read("go.sum"); got != "main\n" {
		t.Errorf("go.sum = %q, want --ours side", got)
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 1 || state.DecodePayload(events[0].Payload)["outcome"] != "lock_file_deterministic" {
		t.Errorf("expected lock_file_deterministic event, got %+v", events)
	}
}

func TestRebaseWithResolution_BinaryConflicts(t *testing.T) {
	bin := func(b byte) []byte { return append([]byte{0, 1, 2, b}, []byte("\x00binary")...) }
	files := map[string][3][]byte{
		"logo.png": {bin(0), bin(1), bin(2)}, // small binary → --ours
		"server":   {bin(0), bin(3), bin(4)}, // compiled name → git rm
	}
	r := newConflictRepo(t, files)
	client := &recordingClient{}
	es := newFileStore(t)
	cr := NewConflictResolver(client, "m", 100, es)

	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("RebaseWithResolution: %v", err)
	}
	if client.calls != 0 {
		t.Error("binary files must never reach the LLM")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "server")); !os.IsNotExist(err) {
		t.Error("compiled binary 'server' must be removed")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "logo.png")); err != nil {
		t.Error("logo.png must be kept")
	}
	kept, _ := es.List(state.EventFilter{Type: state.EventStoryConflictBinary})
	removed, _ := es.List(state.EventFilter{Type: state.EventStoryConflictBinaryRemoved})
	if len(kept) != 1 || len(removed) != 1 {
		t.Errorf("expected 1 kept + 1 removed binary event, got %d/%d", len(kept), len(removed))
	}
}

func TestRebaseWithResolution_MultipleConflictingCommits(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	// Second story commit that also conflicts with main.
	r.write("x.go", []byte("package x\n\nvar v = 2 // story\nvar w = 1\n"))
	r.commitAll("story edit 2")

	responses := []string{
		wrapSentinel("package x\n\nvar v = 3 // merged\n"),
		wrapSentinel("package x\n\nvar v = 3 // merged\nvar w = 1\n"),
	}
	client := &sequenceClient{responses: responses}
	cr := NewConflictResolver(client, "m", 100, newFileStore(t))

	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("RebaseWithResolution: %v", err)
	}
	if client.calls != 2 {
		t.Errorf("expected 2 resolution rounds, got %d", client.calls)
	}
	if got := r.read("x.go"); got != "package x\n\nvar v = 3 // merged\nvar w = 1\n" {
		t.Errorf("final content = %q", got)
	}
}

func TestRebaseWithResolution_ExhaustsRounds(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	r.write("x.go", []byte("package x\n\nvar v = 2 // story\nvar w = 1\n"))
	r.commitAll("story edit 2")
	client := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("package x\n\nvar v = 3 // merged\n")}}
	cr := NewConflictResolver(client, "m", 100, nil)
	cr.maxRounds = 1

	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main")
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("expected exhaustion error, got %v", err)
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be aborted after exhaustion")
	}
}

func TestRebaseWithResolution_TechLeadForWideConflict(t *testing.T) {
	files := map[string][3][]byte{}
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		files[n] = [3][]byte{[]byte("base\n"), []byte("main " + n + "\n"), []byte("story " + n + "\n")}
	}
	r := newConflictRepo(t, files)
	senior := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("main+story\n")}}
	techLead := &recordingClient{resp: llm.CompletionResponse{Content: wrapSentinel("techlead merged\n")}}
	es := newFileStore(t)
	cr := NewConflictResolverWithTechLead(senior, "s", techLead, "tl", 100, nil, es)

	if err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main"); err != nil {
		t.Fatalf("RebaseWithResolution: %v", err)
	}
	if techLead.calls != 4 {
		t.Errorf("expected tech lead on all 4 files (>3 conflicts), got %d", techLead.calls)
	}
	if got := r.read("a.txt"); got != "techlead merged\n" {
		t.Errorf("a.txt = %q", got)
	}
	events, _ := es.List(state.EventFilter{Type: state.EventStoryConflictEscalated})
	if len(events) != 4 {
		t.Errorf("expected 4 tech_lead_resolved events, got %d", len(events))
	}
}

func TestGitFileHistory(t *testing.T) {
	r := newConflictRepo(t, textConflict("x.go"))
	got := gitFileHistory(r.dir, "x.go", 3)
	if len(got) != 2 || got[0] != "story edit" || got[1] != "base" {
		t.Errorf("history = %v", got)
	}
	if gitFileHistory(t.TempDir(), "x.go", 3) != nil {
		t.Error("non-repo must yield nil history")
	}
}

// sequenceClient replays responses in order.
type sequenceClient struct {
	responses []string
	calls     int
}

func (s *sequenceClient) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	i := s.calls
	s.calls++
	if i >= len(s.responses) {
		i = len(s.responses) - 1
	}
	return llm.CompletionResponse{Content: s.responses[i]}, nil
}

// newDeleteModifyRepo builds a repo where main DELETED name while story
// modified it — `git checkout --ours` has no "ours" side and fails.
func newDeleteModifyRepo(t *testing.T, name string, base, story []byte) *conflictRepo {
	t.Helper()
	r := &conflictRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "t@t")
	r.git("config", "user.name", "t")
	r.write(name, base)
	r.write("keep.txt", []byte("keep\n"))
	r.commitAll("base")
	r.git("checkout", "-q", "-b", "story")
	r.write(name, story)
	r.commitAll("story edit")
	r.git("checkout", "-q", "main")
	r.git("rm", "-q", name)
	r.commitAll("main delete")
	r.git("checkout", "-q", "story")
	return r
}

func TestRebaseWithResolution_LockFileOursMissingAborts(t *testing.T) {
	r := newDeleteModifyRepo(t, "go.sum", []byte("base\n"), []byte("story\n"))
	cr := NewConflictResolver(&recordingClient{}, "m", 100, nil)
	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main")
	if err == nil || !strings.Contains(err.Error(), "checkout --ours") {
		t.Fatalf("expected checkout --ours failure, got %v", err)
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be aborted")
	}
}

func TestRebaseWithResolution_BinaryOursMissingAborts(t *testing.T) {
	bin := []byte{0, 1, 2, 3, 0, 'b', 'i', 'n'}
	r := newDeleteModifyRepo(t, "logo.png", bin, append(bin, 9))
	cr := NewConflictResolver(&recordingClient{}, "m", 100, nil)
	err := cr.RebaseWithResolution(context.Background(), "s-1", r.dir, "main")
	if err == nil || !strings.Contains(err.Error(), "checkout --ours") {
		t.Fatalf("expected binary checkout failure, got %v", err)
	}
	if r.rebaseInProgress() {
		t.Error("rebase must be aborted")
	}
}
