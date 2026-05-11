//go:build live_tmux

// Package tmux live-integration tests. Build-tagged so the default
// `go test ./...` lane (which doesn't ship tmux on CI containers)
// skips them. The dedicated `tmux-integration` CI job runs:
//
//	go test -tags live_tmux ./internal/tmux/...
//
// after installing tmux. These tests exercise the real realRun /
// realOutput paths that the mocked unit tests in mocked_test.go
// can't reach.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; skip live-tmux test")
	}
}

// uniqueSessionName generates a tmux session name unique to the
// test name and current time so parallel test runs don't collide
// on a shared tmux server.
func uniqueSessionName(t *testing.T) string {
	t.Helper()
	return "nxd-livetest-" + t.Name() + "-" + time.Now().Format("20060102150405") + "-" + fmt.Sprint(time.Now().UnixNano() % 1000000)
}

// TestLive_AvailableTrue covers the production code path through
// the real exec.LookPath — runFn/outputFn don't intercept this.
func TestLive_AvailableTrue(t *testing.T) {
	requireTmux(t)
	if !Available() {
		t.Fatal("Available() returned false even though tmux is on PATH")
	}
}

// TestLive_CreateAndKillSession drives realRun end-to-end: spawn
// a session, verify it exists, kill it, verify it's gone.
func TestLive_CreateAndKillSession(t *testing.T) {
	requireTmux(t)
	name := uniqueSessionName(t)
	t.Cleanup(func() { _ = KillSession(name) })

	if err := CreateSession(name, "/tmp", "sleep 60"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !SessionExists(name) {
		t.Errorf("session %s should exist after CreateSession", name)
	}
	if err := KillSession(name); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if SessionExists(name) {
		t.Errorf("session %s should not exist after KillSession", name)
	}
}

// TestLive_ListSessionsContainsNew covers realOutput by listing
// sessions and asserting the new one shows up.
func TestLive_ListSessionsContainsNew(t *testing.T) {
	requireTmux(t)
	name := uniqueSessionName(t)
	if err := CreateSession(name, "/tmp", "sleep 60"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(name) })

	got, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	hit := false
	for _, s := range got {
		if s == name {
			hit = true
		}
	}
	if !hit {
		t.Errorf("session %s not in list; got %v", name, got)
	}
}

// TestLive_SendKeysAndCapture exercises both runFn (send-keys) and
// outputFn (capture-pane) via the real tmux daemon. Spawns a
// long-running shell, sends a marker echo, captures the pane and
// asserts the marker shows up.
func TestLive_SendKeysAndCapture(t *testing.T) {
	requireTmux(t)
	name := uniqueSessionName(t)
	// Use bash (long-lived) instead of cat — cat in a tmux pane
	// behaves unreliably on Linux CI containers; bash gives us a
	// stable prompt to send-keys against.
	if err := CreateSession(name, "/tmp", "bash --noprofile --norc"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(name) })

	// Wait briefly for the shell to be ready to receive input.
	time.Sleep(300 * time.Millisecond)

	const marker = "NXD_LIVE_TMUX_MARKER_OUTPUT_XYZ123"
	if err := SendKeys(name, "echo "+marker); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // let bash render the echo

	out, err := CapturePaneOutput(name, 50)
	if err != nil {
		t.Fatalf("CapturePaneOutput: %v", err)
	}
	if !strings.Contains(out, marker) {
		t.Errorf("captured output missing marker; got %q", out)
	}
}

// TestLive_KillSessionUnknownErrors confirms the real tmux returns
// an error when killing a non-existent session (we want the wrap
// path realRun goes through to surface a useful message).
func TestLive_KillSessionUnknownErrors(t *testing.T) {
	requireTmux(t)
	err := KillSession("nxd-definitely-does-not-exist-" + time.Now().Format("1504050000"))
	if err == nil {
		t.Error("expected error from killing nonexistent session")
	}
}
