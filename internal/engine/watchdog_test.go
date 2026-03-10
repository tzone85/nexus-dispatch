package engine_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// mockRuntime is a test double for the runtime.Runtime interface.
type mockRuntime struct {
	status    runtime.AgentStatus
	output    string
	name      string
	lastInput string
}

func (m *mockRuntime) Spawn(_ runtime.SessionConfig) error { return nil }
func (m *mockRuntime) Terminate(_ string) error            { return nil }
func (m *mockRuntime) SendInput(_ string, input string) error {
	m.lastInput = input
	return nil
}
func (m *mockRuntime) ReadOutput(_ string, _ int) (string, error) { return m.output, nil }
func (m *mockRuntime) DetectStatus(_ string) (runtime.AgentStatus, error) {
	return m.status, nil
}
func (m *mockRuntime) Name() string            { return m.name }
func (m *mockRuntime) SupportedModels() []string { return nil }

func TestWatchdog_DetectsPermissionPrompt(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	rt := &mockRuntime{status: runtime.StatusPermissionPrompt, output: "Allow? [Y/n]"}

	result := wd.Check("test-session", rt)
	if result.Action != "permission_bypass" {
		t.Fatalf("expected permission_bypass, got %s", result.Action)
	}
	if rt.lastInput != "Y" {
		t.Fatalf("expected 'Y' sent to runtime, got %q", rt.lastInput)
	}
}

func TestWatchdog_DetectsPlanMode(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	rt := &mockRuntime{status: runtime.StatusPlanMode, output: "Plan mode active"}

	result := wd.Check("test-session", rt)
	if result.Action != "plan_escape" {
		t.Fatalf("expected plan_escape, got %s", result.Action)
	}
}

func TestWatchdog_DetectsStuck(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 0}, es)
	rt := &mockRuntime{status: runtime.StatusWorking, output: "same output forever"}

	// First check: establishes fingerprint
	wd.Check("test-session", rt)

	// Small delay to ensure time passes
	time.Sleep(10 * time.Millisecond)

	// Second check: same output = stuck
	result := wd.Check("test-session", rt)
	if result.Action != "stuck_detected" {
		t.Fatalf("expected stuck_detected, got %s", result.Action)
	}
	if result.Status != runtime.StatusStuck {
		t.Fatalf("expected status stuck, got %s", result.Status)
	}

	// Verify stuck event emitted
	events, err := es.List(state.EventFilter{Type: state.EventAgentStuck})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 AGENT_STUCK event, got %d", len(events))
	}
}

func TestWatchdog_NotStuckWhenOutputChanges(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 0}, es)
	rt := &mockRuntime{status: runtime.StatusWorking, output: "output 1"}
	wd.Check("test-session", rt)

	rt.output = "output 2" // Changed
	result := wd.Check("test-session", rt)
	if result.Action != "none" {
		t.Fatalf("expected none (output changed), got %s", result.Action)
	}
}

func TestWatchdog_ClearFingerprint(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 0}, es)
	rt := &mockRuntime{status: runtime.StatusWorking, output: "same"}
	wd.Check("test-session", rt)
	wd.ClearFingerprint("test-session")

	// After clear, first check establishes new baseline (not stuck)
	result := wd.Check("test-session", rt)
	if result.Action == "stuck_detected" {
		t.Fatal("should not be stuck after fingerprint clear")
	}
}

func TestWatchdog_DoneStatus(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)
	rt := &mockRuntime{status: runtime.StatusDone, output: "done"}

	result := wd.Check("test-session", rt)
	if result.Action != "none" {
		t.Fatalf("expected none for done status, got %s", result.Action)
	}
	if result.Status != runtime.StatusDone {
		t.Fatalf("expected done status, got %s", result.Status)
	}
}
