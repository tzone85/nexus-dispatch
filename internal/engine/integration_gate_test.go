package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newIntegrationMonitor(t *testing.T, es state.EventStore, ps state.ProjectionStore, pause bool, buildErr error, client llm.Client) *Monitor {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.QA.PauseOnIntegrationFailure = pause
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	m.Configure(WithMonTechLeadFixer(NewTechLeadFixer(client, "model", 256, es, ps)))
	m.integrationBuild = func(string) error { return buildErr }
	return m
}

func TestDefaultConfig_PausesOnIntegrationFailure(t *testing.T) {
	if !config.DefaultConfig().QA.PauseOnIntegrationFailure {
		t.Fatal("qa.pause_on_integration_failure must default to true")
	}
}

// TestCheckIntegration_PausesRequirementOnRedMainline: a failed post-merge
// build used to be logged and then dispatchNextWave branched the next wave
// from a red mainline. With the default config the requirement is paused.
func TestCheckIntegration_PausesRequirementOnRedMainline(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	client := &recordingFixClient{called: make(chan struct{})}
	m := newIntegrationMonitor(t, es, ps, true, errors.New("main.go:3: undefined: Foo"), client)

	paused := m.checkIntegration(context.Background(), "s-int", "s-int-a1", t.TempDir())
	if !paused {
		t.Fatal("checkIntegration must report the pause")
	}
	<-client.called // the fixer still runs its diagnosis

	req, err := ps.GetRequirement("REQ-INT")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != "paused" {
		t.Fatalf("requirement status = %q, want paused", req.Status)
	}
	failed, _ := es.List(state.EventFilter{Type: state.EventStoryIntegrationFailed, StoryID: "s-int", AttemptID: "s-int-a1"})
	if len(failed) != 1 {
		t.Fatalf("expected 1 attempt-stamped STORY_INTEGRATION_FAILED from the monitor, got %d", len(failed))
	}
	payload := state.DecodePayload(failed[0].Payload)
	if payload["error"] != "main.go:3: undefined: Foo" || payload["paused"] != true {
		t.Fatalf("unexpected payload %v", payload)
	}
	pauses, _ := es.List(state.EventFilter{Type: state.EventReqPaused})
	if len(pauses) != 1 {
		t.Fatalf("expected 1 REQ_PAUSED, got %d", len(pauses))
	}
	if !m.isRequirementPaused("s-int") {
		t.Fatal("next-wave dispatch must see the requirement as paused")
	}
}

func TestCheckIntegration_ConfigOffKeepsGoing(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	client := &recordingFixClient{called: make(chan struct{})}
	m := newIntegrationMonitor(t, es, ps, false, errors.New("boom"), client)

	if m.checkIntegration(context.Background(), "s-int", "", t.TempDir()) {
		t.Fatal("pause disabled → must not pause")
	}
	<-client.called
	req, _ := ps.GetRequirement("REQ-INT")
	if req.Status == "paused" {
		t.Fatal("requirement must not be paused when pause_on_integration_failure is false")
	}
	failed, _ := es.List(state.EventFilter{Type: state.EventStoryIntegrationFailed, StoryID: "s-int"})
	if len(failed) == 0 {
		t.Fatal("the failure must still be recorded")
	}
	if got := state.DecodePayload(failed[0].Payload)["paused"]; got != false {
		t.Fatalf("payload paused = %v, want false", got)
	}
}

func TestCheckIntegration_GreenBuildIsNoop(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	m := newIntegrationMonitor(t, es, ps, true, nil, llm.NewReplayClient())
	if m.checkIntegration(context.Background(), "s-int", "", t.TempDir()) {
		t.Fatal("green build must not pause")
	}
	if evts, _ := es.List(state.EventFilter{Type: state.EventStoryIntegrationFailed}); len(evts) != 0 {
		t.Fatal("green build must not emit STORY_INTEGRATION_FAILED")
	}
}

func TestCheckIntegration_NoFixerSkipsBuild(t *testing.T) {
	es, ps := capacityTestStores(t)
	cfg := config.DefaultConfig()
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	ran := false
	m.integrationBuild = func(string) error { ran = true; return errors.New("x") }
	if m.checkIntegration(context.Background(), "s", "", t.TempDir()) || ran {
		t.Fatal("without a fixer the post-merge build is not run (legacy gating)")
	}
}

// TestRunIntegrationFix_LLMFailureIsRecorded: the fixer's LLM path used to
// only log when the Tech Lead call failed — the operator saw nothing in the
// event log. It now records the failure with the build error.
func TestRunIntegrationFix_LLMFailureIsRecorded(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	fixer := NewTechLeadFixer(llm.NewErrorClient(errors.New("ollama down")), "model", 256, es, ps)

	hint, err := fixer.runIntegrationFix(context.Background(), "s-int", "build broke")
	if err == nil || hint != "" {
		t.Fatalf("expected error and empty hint, got hint=%q err=%v", hint, err)
	}
	evts, _ := es.List(state.EventFilter{Type: state.EventStoryIntegrationFailed, StoryID: "s-int"})
	if len(evts) != 1 {
		t.Fatalf("expected 1 STORY_INTEGRATION_FAILED, got %d", len(evts))
	}
	payload := state.DecodePayload(evts[0].Payload)
	if payload["build_error"] != "build broke" || payload["fix_error"] != "ollama down" {
		t.Fatalf("unexpected payload %v", payload)
	}
}

func TestRunIntegrationFix_SuggestionInPayload(t *testing.T) {
	es, ps := capacityTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-INT", "s-int")
	fixer := NewTechLeadFixer(llm.NewReplayClient(llm.CompletionResponse{Content: "  align the Handler interface  "}), "model", 256, es, ps)

	hint, err := fixer.runIntegrationFix(context.Background(), "s-int", "build broke")
	if err != nil || hint != "align the Handler interface" {
		t.Fatalf("hint=%q err=%v", hint, err)
	}
	evts, _ := es.List(state.EventFilter{Type: state.EventStoryIntegrationFailed, StoryID: "s-int"})
	if len(evts) != 1 || state.DecodePayload(evts[0].Payload)["fix_hint"] != "align the Handler interface" {
		t.Fatalf("suggestion must be in the payload, got %+v", evts)
	}
}

func TestRunIntegrationFix_UnknownStory(t *testing.T) {
	es, ps := capacityTestStores(t)
	fixer := NewTechLeadFixer(llm.NewReplayClient(), "model", 256, es, ps)
	if _, err := fixer.runIntegrationFix(context.Background(), "nope", "build broke"); err == nil {
		t.Fatal("expected error for unknown story")
	}
}
