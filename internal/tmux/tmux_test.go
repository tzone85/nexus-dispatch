package tmux_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
}

func TestAvailable(t *testing.T) {
	// Just verify it doesn't panic.
	_ = tmux.Available()
}

func TestCreateAndKillSession(t *testing.T) {
	skipIfNoTmux(t)

	name := "nxd-test-session"
	// Cleanup in case of a previous failed test.
	tmux.KillSession(name)

	err := tmux.CreateSession(name, "/tmp", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer tmux.KillSession(name)

	if !tmux.SessionExists(name) {
		t.Fatal("session should exist")
	}

	err = tmux.KillSession(name)
	if err != nil {
		t.Fatalf("kill session: %v", err)
	}

	if tmux.SessionExists(name) {
		t.Fatal("session should not exist after kill")
	}
}

func TestListSessions(t *testing.T) {
	skipIfNoTmux(t)

	name := "nxd-test-list"
	tmux.KillSession(name)

	err := tmux.CreateSession(name, "/tmp", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer tmux.KillSession(name)

	sessions, err := tmux.ListSessions()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	found := false
	for _, s := range sessions {
		if s == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected session %s in list %v", name, sessions)
	}
}

func TestSendKeysAndCapture(t *testing.T) {
	skipIfNoTmux(t)

	name := "nxd-test-capture"
	tmux.KillSession(name)

	err := tmux.CreateSession(name, "/tmp", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer tmux.KillSession(name)

	// Give session time to start.
	time.Sleep(500 * time.Millisecond)

	err = tmux.SendKeys(name, "echo hello-vxd")
	if err != nil {
		t.Fatalf("send keys: %v", err)
	}

	// Give command time to execute.
	time.Sleep(500 * time.Millisecond)

	out, err := tmux.CapturePaneOutput(name, 10)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	if !strings.Contains(out, "hello-vxd") {
		t.Fatalf("expected 'hello-vxd' in output, got: %s", out)
	}
}

func TestSessionExists_NonExistent(t *testing.T) {
	skipIfNoTmux(t)

	if tmux.SessionExists("nxd-nonexistent-session-xyz") {
		t.Fatal("should not exist")
	}
}
