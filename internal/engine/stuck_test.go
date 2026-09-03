package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// stuckRuntime is a runtime.Runtime double whose output the test mutates
// between polls.
type stuckRuntime struct {
	status runtime.AgentStatus
	output string
}

func (r *stuckRuntime) Spawn(runtime.SessionConfig) error                { return nil }
func (r *stuckRuntime) Terminate(string) error                           { return nil }
func (r *stuckRuntime) SendInput(string, string) error                   { return nil }
func (r *stuckRuntime) ReadOutput(string, int) (string, error)           { return r.output, nil }
func (r *stuckRuntime) DetectStatus(string) (runtime.AgentStatus, error) { return r.status, nil }
func (r *stuckRuntime) Name() string                                     { return "stuck-rt" }
func (r *stuckRuntime) SupportedModels() []string                        { return nil }

// fakeClock lets the watchdog tests advance time deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClockedWatchdog(t *testing.T, thresholdS int) (*Watchdog, *fakeClock, *state.FileStore) {
	t.Helper()
	es, _ := newAttemptStores(t)
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: thresholdS}, es)
	clock := &fakeClock{t: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	wd.now = clock.now
	return wd, clock, es
}

// TestWatchdog_KeepsFingerprintTimestampWhileUnchanged reproduces the bug:
// the fingerprint timestamp was overwritten on every poll even when the hash
// was unchanged, so elapsed never exceeded one poll interval and a 60s
// threshold could never trip with a 10s poll.
func TestWatchdog_KeepsFingerprintTimestampWhileUnchanged(t *testing.T) {
	wd, clock, _ := newClockedWatchdog(t, 60)
	rt := &stuckRuntime{status: runtime.StatusWorking, output: "compiling..."}

	first := wd.CheckAgent("sess", rt, AgentIdentity{})
	if first.Action != "none" || first.OutputChanged {
		t.Fatalf("first poll should just establish the baseline, got %+v", first)
	}

	clock.advance(30 * time.Second)
	second := wd.CheckAgent("sess", rt, AgentIdentity{})
	if second.Action != "none" {
		t.Fatalf("30s < 60s threshold, expected none, got %s", second.Action)
	}
	if second.StuckFor != 30*time.Second {
		t.Fatalf("StuckFor = %s, want 30s (measured from the ORIGINAL fingerprint)", second.StuckFor)
	}

	clock.advance(31 * time.Second) // 61s since the output last changed
	third := wd.CheckAgent("sess", rt, AgentIdentity{})
	if third.Action != "stuck_detected" || third.Status != runtime.StatusStuck {
		t.Fatalf("expected stuck after 61s of unchanged output, got %+v", third)
	}
	if !third.StuckEpisodeStarted {
		t.Fatal("first detection of an episode must set StuckEpisodeStarted")
	}
}

func TestWatchdog_EmitsAgentStuckOncePerEpisodeWithIdentity(t *testing.T) {
	wd, clock, es := newClockedWatchdog(t, 60)
	rt := &stuckRuntime{status: runtime.StatusWorking, output: "same"}
	id := AgentIdentity{AgentID: "junior-1", StoryID: "s-1", AttemptID: "s-1-a1"}

	wd.CheckAgent("sess", rt, id)
	clock.advance(61 * time.Second)
	r1 := wd.CheckAgent("sess", rt, id)
	clock.advance(30 * time.Second)
	r2 := wd.CheckAgent("sess", rt, id)

	if !r1.StuckEpisodeStarted || r2.StuckEpisodeStarted {
		t.Fatalf("episode start must be reported exactly once: r1=%v r2=%v", r1.StuckEpisodeStarted, r2.StuckEpisodeStarted)
	}
	if r2.Action != "stuck_detected" {
		t.Fatalf("still stuck on later polls, got %s", r2.Action)
	}

	stuck, _ := es.List(state.EventFilter{Type: state.EventAgentStuck})
	if len(stuck) != 1 {
		t.Fatalf("expected exactly 1 AGENT_STUCK for the episode, got %d", len(stuck))
	}
	evt := stuck[0]
	if evt.AgentID != "junior-1" || evt.StoryID != "s-1" || evt.AttemptID != "s-1-a1" {
		t.Fatalf("AGENT_STUCK must carry agent/story/attempt identity, got %+v", evt)
	}
	payload := state.DecodePayload(evt.Payload)
	if payload["session_name"] != "sess" || payload["stuck_for_s"] != float64(61) {
		t.Fatalf("unexpected payload %v", payload)
	}

	// Output changes → episode over → progress is reported and a new stall
	// later is a NEW episode with its own event.
	rt.output = "new output"
	clock.advance(time.Second)
	r3 := wd.CheckAgent("sess", rt, id)
	if !r3.OutputChanged || r3.Action != "none" {
		t.Fatalf("changed output should clear the stall, got %+v", r3)
	}
	clock.advance(61 * time.Second)
	r4 := wd.CheckAgent("sess", rt, id)
	if !r4.StuckEpisodeStarted {
		t.Fatal("a stall after fresh output is a new episode")
	}
	stuck, _ = es.List(state.EventFilter{Type: state.EventAgentStuck})
	if len(stuck) != 2 {
		t.Fatalf("expected 2 AGENT_STUCK events across 2 episodes, got %d", len(stuck))
	}
}

func TestWatchdog_ClearFingerprintEndsEpisode(t *testing.T) {
	wd, clock, es := newClockedWatchdog(t, 10)
	rt := &stuckRuntime{status: runtime.StatusWorking, output: "same"}
	wd.CheckAgent("sess", rt, AgentIdentity{})
	clock.advance(11 * time.Second)
	wd.CheckAgent("sess", rt, AgentIdentity{})
	wd.ClearFingerprint("sess")
	// A re-tracked session starts a fresh baseline: no stuck, no event.
	r := wd.CheckAgent("sess", rt, AgentIdentity{})
	if r.Action != "none" {
		t.Fatalf("expected baseline after clear, got %s", r.Action)
	}
	stuck, _ := es.List(state.EventFilter{Type: state.EventAgentStuck})
	if len(stuck) != 1 {
		t.Fatalf("expected 1 AGENT_STUCK, got %d", len(stuck))
	}
}

// TestPollOnce_WatchdogOutputChangeEmitsCheckpoint: tmux agents never emit
// STORY_PROGRESS, so the controller's stuck detector saw them as idle from
// STORY_STARTED onwards. The monitor now turns a watchdog-observed output
// change into one AGENT_CHECKPOINT per poll per agent.
func TestPollOnce_WatchdogOutputChangeEmitsCheckpoint(t *testing.T) {
	es, ps := newAttemptStores(t)
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{
		"cli": {Command: "true", Models: []string{"m"},
			Detection: config.RuntimeDetection{IdlePattern: `DONE$`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pane := "line 1"
	var mu sync.Mutex
	stop := tmux.SetTestExec(
		func(args ...string) error { return nil },
		func(args ...string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return pane, nil
		},
	)
	defer stop()

	cfg := config.DefaultConfig()
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: 3600}, es)
	mon := NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	active := map[string]ActiveAgent{
		"nxd-s1": {
			Assignment:  Assignment{StoryID: "s-1", AgentID: "junior-1", SessionName: "nxd-s1", AttemptID: "s-1-a1"},
			RuntimeName: "cli",
		},
	}
	var wg sync.WaitGroup
	ctx := context.Background()

	mon.pollOnce(ctx, &wg, active, t.TempDir()) // baseline
	mon.pollOnce(ctx, &wg, active, t.TempDir()) // unchanged → no checkpoint
	cps, _ := es.List(state.EventFilter{Type: state.EventAgentCheckpoint, StoryID: "s-1"})
	if len(cps) != 0 {
		t.Fatalf("unchanged output must not emit checkpoints, got %d", len(cps))
	}

	mu.Lock()
	pane = "line 1\nline 2"
	mu.Unlock()
	mon.pollOnce(ctx, &wg, active, t.TempDir())
	cps, _ = es.List(state.EventFilter{Type: state.EventAgentCheckpoint, StoryID: "s-1"})
	if len(cps) != 1 {
		t.Fatalf("expected 1 AGENT_CHECKPOINT after output change, got %d", len(cps))
	}
	if cps[0].AgentID != "junior-1" || cps[0].AttemptID != "s-1-a1" {
		t.Fatalf("checkpoint must carry identity, got %+v", cps[0])
	}
	if src := state.DecodePayload(cps[0].Payload)["source"]; src != "watchdog" {
		t.Fatalf("payload source = %v, want watchdog", src)
	}
	if len(active) != 1 {
		t.Fatal("working agent must stay active")
	}
}

func TestPollOnce_StuckAgentEmitsAgentStuckWithStory(t *testing.T) {
	es, ps := newAttemptStores(t)
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{
		"cli": {Command: "true", Models: []string{"m"},
			Detection: config.RuntimeDetection{IdlePattern: `DONE$`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := tmux.SetTestExec(
		func(args ...string) error { return nil },
		func(args ...string) (string, error) { return "frozen", nil },
	)
	defer stop()

	cfg := config.DefaultConfig()
	wd := NewWatchdog(WatchdogConfig{StuckThresholdS: 60}, es)
	clock := &fakeClock{t: time.Now()}
	wd.now = clock.now
	mon := NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	active := map[string]ActiveAgent{
		"nxd-s1": {
			Assignment:  Assignment{StoryID: "s-1", AgentID: "junior-1", SessionName: "nxd-s1", AttemptID: "s-1-a1"},
			RuntimeName: "cli",
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		mon.pollOnce(context.Background(), &wg, active, t.TempDir())
		clock.advance(45 * time.Second)
	}
	stuck, _ := es.List(state.EventFilter{Type: state.EventAgentStuck, StoryID: "s-1"})
	if len(stuck) != 1 {
		t.Fatalf("expected exactly 1 AGENT_STUCK for the story, got %d", len(stuck))
	}
	if stuck[0].AttemptID != "s-1-a1" {
		t.Fatalf("AGENT_STUCK AttemptID = %q", stuck[0].AttemptID)
	}
	if !strings.HasPrefix(stuck[0].AgentID, "junior") {
		t.Fatalf("AGENT_STUCK AgentID = %q", stuck[0].AgentID)
	}
}

// TestController_LastProgressTime_UsesCheckpoints makes the controller treat
// the watchdog's AGENT_CHECKPOINT as progress for tmux agents.
func TestController_LastProgressTime_UsesCheckpoints(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)
	base := time.Now().Add(-10 * time.Minute)

	started := state.NewEvent(state.EventStoryStarted, "agent-1", "s-cp", nil)
	started.Timestamp = base
	progress := state.NewEvent(state.EventStoryProgress, "agent-1", "s-cp", nil)
	progress.Timestamp = base.Add(time.Minute)
	checkpoint := state.NewEvent(state.EventAgentCheckpoint, "agent-1", "s-cp", map[string]any{"source": "watchdog"})
	checkpoint.Timestamp = base.Add(5 * time.Minute)
	for _, evt := range []state.Event{started, progress, checkpoint} {
		if err := es.Append(evt); err != nil {
			t.Fatal(err)
		}
	}

	got := ctrl.lastProgressTime("s-cp")
	if !got.Equal(checkpoint.Timestamp) {
		t.Fatalf("lastProgressTime = %s, want checkpoint time %s", got, checkpoint.Timestamp)
	}

	// A later STORY_PROGRESS still wins over an older checkpoint.
	later := state.NewEvent(state.EventStoryProgress, "agent-1", "s-cp", nil)
	later.Timestamp = base.Add(7 * time.Minute)
	es.Append(later)
	if got := ctrl.lastProgressTime("s-cp"); !got.Equal(later.Timestamp) {
		t.Fatalf("lastProgressTime = %s, want later progress %s", got, later.Timestamp)
	}
}
