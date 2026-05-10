package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// mkTestRegistry returns a Registry with a single CLI runtime
// configured for testing. The command/args don't matter at runtime
// (tmux is mocked) but the regex compilation is real.
func mkTestRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := NewRegistry(map[string]config.RuntimeConfig{
		"aider": {
			Command: "true",
			Args:    []string{"--no-op"},
			Models:  []string{"any"},
			Detection: config.RuntimeDetection{
				IdlePattern:       `>$`,
				PermissionPattern: `permission required`,
				PlanModePattern:   `plan mode`,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// TestCLIRuntime_Spawn_WritesClaudeMDAndCallsTmux locks down the
// Spawn contract:
//   - CLAUDE.md is written into the worktree (suppresses
//     brainstorming/planning plugin overrides).
//   - tmux.CreateSession is invoked with the right args.
// Was 0% before this PR.
func TestCLIRuntime_Spawn_WritesClaudeMDAndCallsTmux(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, err := reg.Get("aider")
	if err != nil {
		t.Fatalf("Get aider: %v", err)
	}

	wd := t.TempDir()
	var lastArgs []string
	stop := tmux.SetTestExec(
		func(args ...string) error {
			lastArgs = append([]string{}, args...)
			return nil
		}, nil)
	defer stop()

	cfg := SessionConfig{
		WorkDir:     wd,
		Model:       "any",
		Goal:        "do thing",
		SessionName: "nxd-spawn-1",
	}
	if err := rt.Spawn(cfg); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// CLAUDE.md must exist.
	if _, err := os.Stat(filepath.Join(wd, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md not written: %v", err)
	}

	// The last tmux call should be new-session for our session name.
	hit := false
	for _, a := range lastArgs {
		if a == "nxd-spawn-1" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("tmux not invoked with session name; got args %v", lastArgs)
	}
}

// TestCLIRuntime_Spawn_TmuxErrorPropagates ensures a tmux failure
// (the mock returns an error) bubbles up so the executor knows the
// agent couldn't start.
func TestCLIRuntime_Spawn_TmuxErrorPropagates(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	stop := tmux.SetTestExec(
		func(args ...string) error { return errors.New("tmux fork failed") }, nil)
	defer stop()

	err := rt.Spawn(SessionConfig{
		WorkDir:     t.TempDir(),
		Model:       "any",
		SessionName: "nxd-spawn-fail",
	})
	if err == nil {
		t.Fatal("expected Spawn to surface tmux error")
	}
}

// TestCLIRuntime_Terminate_CallsKillSession covers the termination
// path — operator cancels via the dashboard's ✕ button, which leads
// here.
func TestCLIRuntime_Terminate_CallsKillSession(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	called := false
	stop := tmux.SetTestExec(
		func(args ...string) error {
			if len(args) > 0 && args[0] == "kill-session" {
				called = true
			}
			return nil
		}, nil)
	defer stop()

	if err := rt.Terminate("session-x"); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if !called {
		t.Error("expected kill-session call to tmux")
	}
}

// TestCLIRuntime_SendInput_CallsSendKeys covers the input path that
// the runtime uses to deliver agent commands.
func TestCLIRuntime_SendInput_CallsSendKeys(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	var captured []string
	stop := tmux.SetTestExec(
		func(args ...string) error {
			captured = append([]string{}, args...)
			return nil
		}, nil)
	defer stop()

	if err := rt.SendInput("session-x", "ls -la"); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	if len(captured) == 0 || captured[0] != "send-keys" {
		t.Errorf("expected send-keys command; got %v", captured)
	}
}

// TestCLIRuntime_ReadOutput_CallsCapturePane covers the output read
// path used by the monitor's polling loop.
func TestCLIRuntime_ReadOutput_CallsCapturePane(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	stop := tmux.SetTestExec(nil, func(args ...string) (string, error) {
		return "  recent output line 1\nrecent output line 2  \n", nil
	})
	defer stop()

	out, err := rt.ReadOutput("session-x", 50)
	if err != nil {
		t.Fatalf("ReadOutput: %v", err)
	}
	if !strings.Contains(out, "recent output line 1") {
		t.Errorf("output missing line 1; got %q", out)
	}
}

// TestCLIRuntime_DetectStatus_PermissionPrompt covers the regex match
// branch for the permission-prompt detection.
func TestCLIRuntime_DetectStatus_PermissionPrompt(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	stop := tmux.SetTestExec(
		func(args ...string) error { return nil }, // has-session in fallback
		func(args ...string) (string, error) {
			return "running...\npermission required to write file\n", nil
		})
	defer stop()

	st, err := rt.DetectStatus("session-x")
	if err != nil {
		t.Fatalf("DetectStatus: %v", err)
	}
	if st != StatusPermissionPrompt {
		t.Errorf("status = %s, want permission_prompt", st)
	}
}

// TestCLIRuntime_DetectStatus_PlanMode covers the plan-mode regex
// branch.
func TestCLIRuntime_DetectStatus_PlanMode(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	stop := tmux.SetTestExec(
		func(args ...string) error { return nil },
		func(args ...string) (string, error) {
			return "now in plan mode\n", nil
		})
	defer stop()

	st, _ := rt.DetectStatus("session-x")
	if st != StatusPlanMode {
		t.Errorf("status = %s, want plan_mode", st)
	}
}

// TestCLIRuntime_DetectStatus_TerminatedWhenSessionGone covers the
// "session no longer exists" branch — the monitor relies on
// StatusTerminated to drop the agent from its tracking list.
func TestCLIRuntime_DetectStatus_TerminatedWhenSessionGone(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	// outputFn fails (capture-pane errors) and runFn fails too
	// (has-session returns error → SessionExists=false → Terminated).
	stop := tmux.SetTestExec(
		func(args ...string) error { return errors.New("no such session") },
		func(args ...string) (string, error) { return "", errors.New("capture failed") },
	)
	defer stop()

	st, err := rt.DetectStatus("session-gone")
	if err != nil {
		t.Errorf("expected nil error when session terminated, got %v", err)
	}
	if st != StatusTerminated {
		t.Errorf("status = %s, want terminated", st)
	}
}

// TestCLIRuntime_DetectionPatternsCompiled lock down the constructor's
// regex compilation. A typo in a config detection pattern shouldn't
// silently disable detection — NewRegistry should fail loudly.
func TestNewRegistry_RejectsBadDetectionRegex(t *testing.T) {
	_, err := NewRegistry(map[string]config.RuntimeConfig{
		"bad": {
			Command:   "true",
			Detection: config.RuntimeDetection{IdlePattern: `[unclosed`},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

// TestCLIRuntime_NameAndModels covers the trivial accessors so a
// future field rename can't silently break the runtime registry.
func TestCLIRuntime_NameAndModels(t *testing.T) {
	reg := mkTestRegistry(t)
	rt, _ := reg.Get("aider")

	if rt.Name() != "aider" {
		t.Errorf("Name() = %q, want aider", rt.Name())
	}
	models := rt.SupportedModels()
	if len(models) != 1 || models[0] != "any" {
		t.Errorf("SupportedModels = %v, want [any]", models)
	}
}

// TestRuntime_DetectionRegexFallbackPath confirms the matcher
// short-circuits when no patterns are configured: status must
// default to working (not panic on a nil regex).
func TestRuntime_DetectionRegexFallbackPath(t *testing.T) {
	// Build a runtime with no detection patterns at all.
	reg, err := NewRegistry(map[string]config.RuntimeConfig{
		"plain": {Command: "true", Models: []string{"x"}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	rt, _ := reg.Get("plain")

	stop := tmux.SetTestExec(nil, func(args ...string) (string, error) {
		return "any output", nil
	})
	defer stop()

	st, _ := rt.DetectStatus("s")
	if st != StatusWorking {
		t.Errorf("status with no patterns = %s, want working", st)
	}
}

// validRegex is just here so the import compiles cleanly when other
// tests are added that need regexp.MustCompile.
var validRegex = regexp.MustCompile(`.`)

// _ silences the unused-var warning if the import isn't needed.
var _ = validRegex
