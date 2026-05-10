package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// fakeLLMClient is a no-op llm.Client used by buildNativeClient tests.
// We don't drive Complete() — only the construction path matters.
type fakeLLMClient struct{}

func (fakeLLMClient) Complete(ctx interface{ Done() <-chan struct{} }, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	return llm.CompletionResponse{}, nil
}

// minimalExecutor builds an Executor with just enough state to drive
// the helpers. The executor's required dependencies (registry,
// stores) are injected so the helpers can be exercised in isolation.
func minimalExecutor(t *testing.T, llmClient llm.Client, runtimeCfg map[string]config.RuntimeConfig) *Executor {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	ps, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	if runtimeCfg == nil {
		runtimeCfg = map[string]config.RuntimeConfig{}
	}
	cfg := config.DefaultConfig()
	cfg.Runtimes = runtimeCfg

	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	e := NewExecutor(reg, cfg, es, ps, nil)
	if llmClient != nil {
		e.SetLLMClient(llmClient)
	}
	return e
}

// TestBuildNativeClient_NilLLMReturnsNil covers the early-return path:
// when no LLM client is wired (e.g. CLI mode), buildNativeClient
// must return nil so spawnNative knows to refuse.
func TestBuildNativeClient_NilLLMReturnsNil(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	if got := e.buildNativeClient(); got != nil {
		t.Errorf("expected nil when no LLM wired, got %T", got)
	}
}

// TestBuildNativeClient_DefaultConcurrency confirms the fallback
// concurrency=1 when no native runtime config sets a positive
// Concurrency. Single-GPU Ollama users rely on this default.
func TestBuildNativeClient_DefaultConcurrency(t *testing.T) {
	stub := llm.NewReplayClient(llm.CompletionResponse{})
	e := minimalExecutor(t, stub, nil)
	got := e.buildNativeClient()
	if got == nil {
		t.Fatal("expected non-nil client")
	}
	if _, ok := got.(*llm.SemaphoreClient); !ok {
		t.Errorf("expected *SemaphoreClient wrapping; got %T", got)
	}
}

// TestBuildNativeClient_HonoursNativeConcurrency covers the branch
// that picks up the first native runtime's Concurrency. When a
// runtime declares Native:true and Concurrency:N, the wrapper must
// honour N (rather than the conservative default of 1).
func TestBuildNativeClient_HonoursNativeConcurrency(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"gemma": {Native: true, Concurrency: 4, MaxIterations: 10, CommandAllowlist: []string{"go test"}, Models: []string{"gemma4"}},
	}
	stub := llm.NewReplayClient(llm.CompletionResponse{})
	e := minimalExecutor(t, stub, cfg)

	got := e.buildNativeClient()
	if got == nil {
		t.Fatal("expected non-nil client")
	}
	if _, ok := got.(*llm.SemaphoreClient); !ok {
		t.Errorf("expected *SemaphoreClient wrapping; got %T", got)
	}
	// We can't peek at the channel buffer size from outside the
	// llm package — the visible contract is that a SemaphoreClient
	// was returned. That's what spawnNative checks.
}

// TestRuntimeForRole_PrefersNativeMatchingModel: when the model
// configured for a role matches a native runtime's model list,
// runtimeForRole picks the native runtime over CLI alternatives.
func TestRuntimeForRole_PrefersNativeMatchingModel(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"gemma":  {Native: true, MaxIterations: 10, CommandAllowlist: []string{"go test"}, Models: []string{"gemma4"}},
		"claude": {Command: "claude", Args: []string{"--no-auto-update"}, Models: []string{"sonnet"}},
	}
	e := minimalExecutor(t, nil, cfg)
	e.config.Models.Junior.Provider = "ollama"
	e.config.Models.Junior.Model = "gemma4:e4b"

	got := e.runtimeForRole(agent.RoleJunior)
	if got != "gemma" {
		t.Errorf("expected native gemma runtime for gemma4 model; got %q", got)
	}
}

// TestRuntimeForRole_FallsBackToProvider covers the well-known
// provider → runtime mapping for non-native models.
func TestRuntimeForRole_FallsBackToProvider(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"claude-code": {Command: "claude", Args: []string{}, Models: []string{"claude-3-5-sonnet"}},
	}
	e := minimalExecutor(t, nil, cfg)
	e.config.Models.Senior.Provider = "anthropic"
	e.config.Models.Senior.Model = "claude-3-5-sonnet"

	got := e.runtimeForRole(agent.RoleSenior)
	if got != "claude-code" {
		t.Errorf("expected claude-code for anthropic provider; got %q", got)
	}
}

// TestRuntimeForRole_UnknownProviderUsesFirstAvailable falls back to
// the first available runtime when neither native nor provider
// mapping matches.
func TestRuntimeForRole_UnknownProviderUsesFirstAvailable(t *testing.T) {
	cfg := map[string]config.RuntimeConfig{
		"someruntime": {Command: "echo", Args: []string{}, Models: []string{"any"}},
	}
	e := minimalExecutor(t, nil, cfg)
	e.config.Models.Junior.Provider = "unknown-provider"
	e.config.Models.Junior.Model = "no-match"

	got := e.runtimeForRole(agent.RoleJunior)
	if got != "someruntime" {
		t.Errorf("expected fallback to first available runtime; got %q", got)
	}
}

// TestRuntimeForRole_NoRuntimesDefaultsToAider is the absolute
// fallback when no runtimes are configured at all. Defends against
// runtime-config drift breaking the CLI when an operator has an
// almost-empty nxd.yaml.
func TestRuntimeForRole_NoRuntimesDefaultsToAider(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	e.config.Models.Junior.Provider = "ollama"
	e.config.Models.Junior.Model = "any-model"

	got := e.runtimeForRole(agent.RoleJunior)
	if got != "aider" {
		t.Errorf("expected aider fallback when no runtimes; got %q", got)
	}
}

// TestLatestReviewFeedback_NoEventsReturnsEmpty covers the
// no-feedback path. The agent prompt template handles "" cleanly,
// so this contract matters.
func TestLatestReviewFeedback_NoEventsReturnsEmpty(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	got := e.latestReviewFeedback("STORY-NEVER")
	if got != "" {
		t.Errorf("expected empty feedback for unknown story; got %q", got)
	}
}

// TestLatestReviewFeedback_ReturnsLatestFeedback covers the happy
// path: when multiple STORY_REVIEW_FAILED events exist for a story,
// the helper returns the feedback from the most recent one.
func TestLatestReviewFeedback_ReturnsLatestFeedback(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	for i, msg := range []string{"first feedback", "second feedback", "latest feedback"} {
		evt := state.NewEvent(state.EventStoryReviewFailed, "monitor", "STORY-RF", map[string]any{
			"feedback": msg,
			"index":    i,
		})
		if err := e.eventStore.Append(evt); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got := e.latestReviewFeedback("STORY-RF")
	if got != "latest feedback" {
		t.Errorf("expected most recent feedback; got %q", got)
	}
}

// TestLatestReviewFeedback_OtherAgentsIgnored confirms only events
// authored by 'monitor' count. Without this filter, a STORY_REVIEW_FAILED
// from a plugin or human-source would leak into the agent prompt.
func TestLatestReviewFeedback_OtherAgentsIgnored(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	// Plugin-emitted, non-monitor agent.
	evt := state.NewEvent(state.EventStoryReviewFailed, "operator", "STORY-FILTER", map[string]any{
		"feedback": "operator-supplied feedback",
	})
	if err := e.eventStore.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}

	got := e.latestReviewFeedback("STORY-FILTER")
	if got != "" {
		t.Errorf("non-monitor feedback should be filtered; got %q", got)
	}
}

// TestLatestReviewFeedback_MalformedPayloadReturnsEmpty covers the
// json.Unmarshal failure path — corrupted payload (e.g. plugin
// emitted bytes that aren't valid JSON) must not crash the executor.
func TestLatestReviewFeedback_MalformedPayloadReturnsEmpty(t *testing.T) {
	e := minimalExecutor(t, nil, nil)
	// Hand-craft an event with bad payload bytes.
	evt := state.Event{
		ID:      "EV-MALFORMED",
		Type:    state.EventStoryReviewFailed,
		AgentID: "monitor",
		StoryID: "STORY-BAD",
		Payload: []byte("not json"),
	}
	if err := e.eventStore.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}

	got := e.latestReviewFeedback("STORY-BAD")
	if got != "" {
		t.Errorf("malformed payload should yield empty feedback; got %q", got)
	}
}

// TestExecExpandHome_LeadingTilde covers the ~ → home replacement.
func TestExecExpandHome_LeadingTilde(t *testing.T) {
	got := execExpandHome("~/foo")
	if !strings.Contains(got, "foo") {
		t.Errorf("expected expansion to include foo; got %q", got)
	}
	if strings.HasPrefix(got, "~") {
		t.Errorf("expected ~ to be expanded; got %q", got)
	}
}

// TestExecExpandHome_NoTildePassThrough covers the no-op path.
func TestExecExpandHome_NoTildePassThrough(t *testing.T) {
	for _, in := range []string{"/abs/path", "relative/path", ""} {
		if got := execExpandHome(in); got != in {
			t.Errorf("execExpandHome(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestTierForRole_AllRoles locks down the role → tier mapping. Adding
// or renaming a role without updating tierForRole would silently
// route stories to the wrong escalation tier — caught here.
func TestTierForRole_AllRoles(t *testing.T) {
	cases := map[agent.Role]int{
		agent.RoleJunior:       0,
		agent.RoleIntermediate: 0,
		agent.RoleSenior:       1,
	}
	for role, want := range cases {
		t.Run(string(role), func(t *testing.T) {
			if got := tierForRole(role); got != want {
				t.Errorf("tierForRole(%s) = %d, want %d", role, got, want)
			}
		})
	}
}
