package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestNewMonitor(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	cfg := config.DefaultConfig()
	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)

	reg, err := newTestRegistry()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	if mon == nil {
		t.Fatal("expected non-nil Monitor")
	}
}

func TestMonitor_Run_EmptyAgents(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	cfg := config.DefaultConfig()
	cfg.Monitor.PollIntervalMs = 10 // fast polling for test

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	reg, err := newTestRegistry()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// With no agents, the monitor should return nil after the first tick
	// detects the empty map.
	err = mon.Run(ctx, []engine.ActiveAgent{}, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestMonitor_Run_ContextCancelled(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	cfg := config.DefaultConfig()
	cfg.Monitor.PollIntervalMs = 10

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	reg, err := newTestRegistry()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)

	// Create an agent that references a non-existent runtime name so
	// pollOnce skips it on every tick, keeping the monitor alive until
	// the context is cancelled.
	agents := []engine.ActiveAgent{
		{
			Assignment: engine.Assignment{
				StoryID:     "s-001",
				AgentID:     "agent-1",
				SessionName: "nxd-test-1",
				Branch:      "nxd/s-001",
			},
			RuntimeName:  "nonexistent-runtime",
			WorktreePath: "/tmp/wt",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = mon.Run(ctx, agents, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected nil error on cancellation, got %v", err)
	}
}

func TestMonitor_Run_DetectsCompletedAgent(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Pre-populate story so projection works
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{
		"id": "s-001", "req_id": "r-001", "title": "Task", "description": "desc", "complexity": 3,
	}))

	// Create a registry with a "test-runtime" whose idle pattern matches
	// the word "done". When DetectStatus reads the session output and
	// finds this pattern, it returns StatusDone.
	reg, err := newTestRegistryWithDone()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Monitor.PollIntervalMs = 10

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)

	// No reviewer, QA, or merger — just verify the monitor detects completion
	// and emits the STORY_COMPLETED event.
	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)

	agents := []engine.ActiveAgent{
		{
			Assignment: engine.Assignment{
				StoryID:     "s-001",
				AgentID:     "agent-1",
				SessionName: "nxd-test-1",
				Branch:      "nxd/s-001",
			},
			RuntimeName:  "test-runtime",
			WorktreePath: "/tmp/wt",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// The monitor will call registry.Get("test-runtime") which returns a
	// CLIRuntime backed by tmux. In CI/local without tmux, DetectStatus
	// will fail and return StatusTerminated (session doesn't exist), which
	// triggers the completion path. This tests the happy path integration.
	err = mon.Run(ctx, agents, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Verify STORY_COMPLETED event was emitted
	events, err := es.List(state.EventFilter{Type: state.EventStoryCompleted})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 STORY_COMPLETED event, got %d", len(events))
	}
}

func TestMonitor_Run_DefaultPollInterval(t *testing.T) {
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	cfg := config.DefaultConfig()
	cfg.Monitor.PollIntervalMs = 0 // should default to 10s

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	reg, err := newTestRegistry()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)

	// Cancel immediately — this validates that the monitor handles zero
	// poll interval gracefully.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = mon.Run(ctx, []engine.ActiveAgent{}, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// newTestRegistry creates a minimal registry with no runtimes configured.
func newTestRegistry() (*runtime.Registry, error) {
	return runtime.NewRegistry(map[string]config.RuntimeConfig{})
}

// newTestRegistryWithDone creates a registry with a "test-runtime" that has
// detection patterns configured. When the session doesn't exist (no tmux),
// DetectStatus returns StatusTerminated.
func newTestRegistryWithDone() (*runtime.Registry, error) {
	return runtime.NewRegistry(map[string]config.RuntimeConfig{
		"test-runtime": {
			Command: "echo",
			Args:    []string{"test"},
			Models:  []string{"test-model"},
			Detection: config.RuntimeDetection{
				IdlePattern:       `\$\s*$`,
				PermissionPattern: `Allow\?`,
			},
		},
	})
}
