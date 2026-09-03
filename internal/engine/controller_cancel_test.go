package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// terminatingRuntime records Terminate calls so the tests can prove the
// controller killed the right tmux session.
type terminatingRuntime struct {
	stuckRuntime
	terminated []string
	err        error
}

func (r *terminatingRuntime) Terminate(sessionID string) error {
	r.terminated = append(r.terminated, sessionID)
	return r.err
}

// TestController_CancelStory_TerminatesTmuxSession reproduces the bug where
// cancelStory only invoked native cancel funcs, so a stuck CLI agent kept
// running in tmux after the controller "cancelled" it.
func TestController_CancelStory_TerminatesTmuxSession(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	rt := &terminatingRuntime{}

	ctrl.RegisterSession("s-cli", "nxd-r1-junior-1", rt)
	ctrl.cancelStory("s-cli")

	if len(rt.terminated) != 1 || rt.terminated[0] != "nxd-r1-junior-1" {
		t.Fatalf("expected Terminate(nxd-r1-junior-1), got %v", rt.terminated)
	}
	// Entry is consumed: a second cancel must not kill anything again.
	ctrl.cancelStory("s-cli")
	if len(rt.terminated) != 1 {
		t.Fatalf("session entry should be removed after cancel, Terminate calls = %v", rt.terminated)
	}
	events, _ := es.List(state.EventFilter{Type: state.EventAgentTerminated, StoryID: "s-cli"})
	if len(events) != 2 {
		t.Fatalf("expected AGENT_TERMINATED per cancel, got %d", len(events))
	}
	if got := state.DecodePayload(events[0].Payload)["session_name"]; got != "nxd-r1-junior-1" {
		t.Fatalf("AGENT_TERMINATED should name the killed session, got %v", got)
	}
}

func TestController_CancelStory_TerminateErrorIsRecorded(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	rt := &terminatingRuntime{err: errors.New("no server running")}

	ctrl.RegisterSession("s-cli", "nxd-dead", rt)
	ctrl.cancelStory("s-cli")

	events, _ := es.List(state.EventFilter{Type: state.EventAgentTerminated, StoryID: "s-cli"})
	if len(events) != 1 {
		t.Fatalf("expected 1 AGENT_TERMINATED, got %d", len(events))
	}
	if got := state.DecodePayload(events[0].Payload)["terminate_error"]; got != "no server running" {
		t.Fatalf("terminate error must be surfaced in the payload, got %v", got)
	}
}

func TestController_CancelStory_BothNativeAndSession(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	rt := &terminatingRuntime{}
	cancelled := false
	ctrl.RegisterCancel("s-both", func() { cancelled = true })
	ctrl.RegisterSession("s-both", "sess", rt)

	ctrl.cancelStory("s-both")
	if !cancelled || len(rt.terminated) != 1 {
		t.Fatalf("expected both native cancel and tmux terminate, cancelled=%v terminated=%v", cancelled, rt.terminated)
	}
}

func TestController_RegisterSession_LatestDispatchWins(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	old := &terminatingRuntime{}
	current := &terminatingRuntime{}
	ctrl.RegisterSession("s-1", "attempt-1-session", old)
	ctrl.RegisterSession("s-1", "attempt-2-session", current)
	ctrl.DeregisterSession("s-other") // unknown is a no-op

	ctrl.cancelStory("s-1")
	if len(old.terminated) != 0 || len(current.terminated) != 1 {
		t.Fatalf("only the latest session must be killed: old=%v current=%v", old.terminated, current.terminated)
	}
}

func TestController_DeregisterSession(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	rt := &terminatingRuntime{}
	ctrl.RegisterSession("s-1", "sess", rt)
	ctrl.DeregisterSession("s-1")
	ctrl.cancelStory("s-1")
	if len(rt.terminated) != 0 {
		t.Fatalf("deregistered session must not be terminated, got %v", rt.terminated)
	}
}

// TestSpawn_CLIRuntime_RegistersSessionWithController is the wiring test:
// the executor must hand every CLI session to the controller so cancel can
// reach it.
func TestSpawn_CLIRuntime_RegistersSessionWithController(t *testing.T) {
	repo := t.TempDir()
	initSpawnTestRepo(t, repo)
	es, ps := newAttemptStores(t)

	var killed []string
	stop := tmux.SetTestExec(
		func(args ...string) error {
			if len(args) > 0 && args[0] == "kill-session" {
				killed = append(killed, args[len(args)-1])
			}
			return nil
		},
		func(args ...string) (string, error) { return "", nil },
	)
	defer stop()

	runtimeCfg := map[string]config.RuntimeConfig{"aider": {Command: "true", Models: []string{"any-model"}}}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = filepath.Join(t.TempDir(), "state")
	cfg.Runtimes = runtimeCfg
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "any-model"
	reg, err := runtime.NewRegistry(runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	e := NewExecutor(reg, cfg, es, ps, nil)
	e.SetController(ctrl)

	a := Assignment{
		StoryID: "STORY-KILL", ReqID: "REQ-1", Role: agent.RoleJunior,
		Branch: "story/STORY-KILL", AgentID: "junior-001", SessionName: "nxd-kill-me",
	}
	if res := e.spawn(context.Background(), repo, a, PlannedStory{ID: "STORY-KILL", Title: "t"}, nil, nil); res.Error != nil {
		t.Fatalf("spawn: %v", res.Error)
	}

	// tmux.CreateSession pre-emptively kills a same-named session; only
	// count kills issued by the controller.
	killed = nil
	ctrl.cancelStory("STORY-KILL")
	if len(killed) != 1 || killed[0] != "nxd-kill-me" {
		t.Fatalf("expected tmux kill-session nxd-kill-me, got %v", killed)
	}
}
