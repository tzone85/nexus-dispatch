package engine

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// promptRuntime is a runtime double that records inputs sent to it.
type promptRuntime struct {
	status runtime.AgentStatus
	inputs []string
}

func (r *promptRuntime) Spawn(runtime.SessionConfig) error { return nil }
func (r *promptRuntime) Terminate(string) error            { return nil }
func (r *promptRuntime) SendInput(_ string, in string) error {
	r.inputs = append(r.inputs, in)
	return nil
}
func (r *promptRuntime) ReadOutput(string, int) (string, error)           { return "waiting", nil }
func (r *promptRuntime) DetectStatus(string) (runtime.AgentStatus, error) { return r.status, nil }
func (r *promptRuntime) Name() string                                     { return "prompt-rt" }
func (r *promptRuntime) SupportedModels() []string                        { return nil }

func TestWatchdogConfig_AutoApprovePerRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Runtimes["boxed"] = config.RuntimeConfig{Runner: "docker"}
	cfg.Runtimes["host"] = config.RuntimeConfig{}
	wc := WatchdogConfig{AutoApprovePrompts: cfg.AutoApprovePrompts}

	cases := map[string]bool{"boxed": true, "host": false, "unknown": false}
	for rtName, want := range cases {
		if got := wc.autoApprove(rtName); got != want {
			t.Errorf("autoApprove(%q) = %v, want %v", rtName, got, want)
		}
	}
	if (WatchdogConfig{}).autoApprove("boxed") {
		t.Error("a nil decision function must never auto-approve")
	}
}

func TestWatchdog_PermissionPrompt(t *testing.T) {
	t.Run("auto-approved runtime answers Y", func(t *testing.T) {
		es, _ := newAttemptStores(t)
		wd := NewWatchdog(WatchdogConfig{AutoApprovePrompts: func(n string) bool { return n == "boxed" }}, es)
		rt := &promptRuntime{status: runtime.StatusPermissionPrompt}
		res := wd.CheckAgent("sess", rt, AgentIdentity{RuntimeName: "boxed"})
		if res.Action != "permission_bypass" || res.PromptEpisodeStarted {
			t.Fatalf("result = %+v, want permission_bypass without a prompt episode", res)
		}
		if len(rt.inputs) != 1 || rt.inputs[0] != "Y" {
			t.Fatalf("inputs = %v, want [Y]", rt.inputs)
		}
	})

	t.Run("host runtime surfaces the prompt once per episode", func(t *testing.T) {
		es, _ := newAttemptStores(t)
		wd := NewWatchdog(WatchdogConfig{AutoApprovePrompts: func(n string) bool { return n == "boxed" }}, es)
		rt := &promptRuntime{status: runtime.StatusPermissionPrompt}
		id := AgentIdentity{RuntimeName: "host"}

		first := wd.CheckAgent("sess", rt, id)
		if first.Action != "permission_prompt" || !first.PromptEpisodeStarted {
			t.Fatalf("first poll = %+v, want permission_prompt episode start", first)
		}
		second := wd.CheckAgent("sess", rt, id)
		if second.Action != "permission_prompt" || second.PromptEpisodeStarted {
			t.Fatalf("second poll = %+v, want permission_prompt without a new episode", second)
		}
		if len(rt.inputs) != 0 {
			t.Fatalf("the watchdog must not answer for the human, sent %v", rt.inputs)
		}

		// The human answers; the agent works again, then prompts again → new episode.
		rt.status = runtime.StatusWorking
		if res := wd.CheckAgent("sess", rt, id); res.Action != "none" || res.PromptEpisodeStarted {
			t.Fatalf("working poll = %+v", res)
		}
		rt.status = runtime.StatusPermissionPrompt
		if res := wd.CheckAgent("sess", rt, id); !res.PromptEpisodeStarted {
			t.Fatalf("a new prompt after progress must start a new episode, got %+v", res)
		}
	})
}

// TestObserveAgent_PermissionPromptPausesOnce: the monitor turns the first
// poll of a prompt episode into HUMAN_REVIEW_NEEDED + REQ_PAUSED, exactly
// once, without terminating the agent.
func TestObserveAgent_PermissionPromptPausesOnce(t *testing.T) {
	es, ps := newControllerTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-PP", "s-pp")
	cfg := config.DefaultConfig()
	cfg.Runtimes["host"] = config.RuntimeConfig{}
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: 3600, AutoApprovePrompts: cfg.AutoApprovePrompts}, es)
	m := NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	rt := &promptRuntime{status: runtime.StatusPermissionPrompt}
	ag := ActiveAgent{
		Assignment:  Assignment{StoryID: "s-pp", AgentID: "junior-1", SessionName: "nxd-s-pp", AttemptID: "s-pp-a1"},
		RuntimeName: "host",
	}

	for i := 0; i < 3; i++ {
		if res := m.observeAgent("nxd-s-pp", rt, ag); res.Action != "permission_prompt" {
			t.Fatalf("poll %d action = %q", i, res.Action)
		}
	}

	reviews, _ := es.List(state.EventFilter{Type: state.EventHumanReviewNeeded, StoryID: "s-pp"})
	if len(reviews) != 1 {
		t.Fatalf("expected exactly 1 HUMAN_REVIEW_NEEDED per prompt episode, got %d", len(reviews))
	}
	reason, _ := state.DecodePayload(reviews[0].Payload)["reason"].(string)
	for _, want := range []string{"permission prompt", "tmux attach -t nxd-s-pp", "sandbox.auto_approve_prompts", "host"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q must mention %q", reason, want)
		}
	}
	if pauses, _ := es.List(state.EventFilter{Type: state.EventReqPaused}); len(pauses) != 1 {
		t.Fatalf("expected 1 REQ_PAUSED, got %d", len(pauses))
	}
	if req, _ := ps.GetRequirement("REQ-PP"); req.Status != "paused" {
		t.Fatalf("requirement status = %q, want paused", req.Status)
	}
	if len(rt.inputs) != 0 {
		t.Fatalf("monitor must not answer the prompt, sent %v", rt.inputs)
	}
}

// TestObserveAgent_SandboxedPromptIsAnswered: the same prompt on a sandboxed
// runtime is answered by the watchdog and never pauses anything.
func TestObserveAgent_SandboxedPromptIsAnswered(t *testing.T) {
	es, ps := newControllerTestStores(t)
	seedCapacityStory(t, es, ps, "REQ-PP", "s-pp")
	cfg := config.DefaultConfig()
	cfg.Runtimes["boxed"] = config.RuntimeConfig{Runner: "docker"}
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: 3600, AutoApprovePrompts: cfg.AutoApprovePrompts}, es)
	m := NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	rt := &promptRuntime{status: runtime.StatusPermissionPrompt}
	ag := ActiveAgent{Assignment: Assignment{StoryID: "s-pp", SessionName: "nxd-s-pp"}, RuntimeName: "boxed"}

	if res := m.observeAgent("nxd-s-pp", rt, ag); res.Action != "permission_bypass" {
		t.Fatalf("action = %q, want permission_bypass", res.Action)
	}
	if len(rt.inputs) != 1 || rt.inputs[0] != "Y" {
		t.Fatalf("inputs = %v", rt.inputs)
	}
	if pauses, _ := es.List(state.EventFilter{Type: state.EventReqPaused}); len(pauses) != 0 {
		t.Fatal("auto-approved prompt must not pause")
	}
}
