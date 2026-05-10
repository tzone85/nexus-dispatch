package tmux

import (
	"errors"
	"strings"
	"testing"
)

// withMockedExec swaps runFn / outputFn for the duration of a test.
// Returns a function that restores the originals; defer it.
//
// Without this hook, tmux tests had to be skipped on CI containers
// without a tmux binary — leaving session.go at 58% coverage. The
// indirection lets us exercise the orchestration logic (kill-then-
// create, no-server-running fallback, list parsing) deterministically.
func withMockedExec(t *testing.T, runResp func(args ...string) error, outResp func(args ...string) (string, error)) func() {
	t.Helper()
	prevRun := runFn
	prevOut := outputFn
	if runResp != nil {
		runFn = runResp
	}
	if outResp != nil {
		outputFn = outResp
	}
	return func() {
		runFn = prevRun
		outputFn = prevOut
	}
}

// TestCreateSession_KillsExistingFirst covers the "if a session with
// the same name already exists it is killed first" comment in
// CreateSession. We track the sequence of args sent to tmux and
// assert kill-session ran before new-session.
func TestCreateSession_KillsExistingFirst(t *testing.T) {
	calls := []string{}
	stop := withMockedExec(t,
		func(args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			return nil
		},
		func(args ...string) (string, error) {
			calls = append(calls, strings.Join(args, " "))
			return "", nil
		},
	)
	defer stop()

	if err := CreateSession("my-session", "/tmp", "echo hi"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	wantSeq := []string{
		"has-session -t my-session", // existence check (run via realRun → wrapped)
		"kill-session -t my-session",
		"new-session -d -s my-session -c /tmp echo hi",
	}
	if len(calls) != len(wantSeq) {
		t.Fatalf("expected %d tmux calls, got %d: %v", len(wantSeq), len(calls), calls)
	}
	for i, want := range wantSeq {
		if calls[i] != want {
			t.Errorf("call[%d] = %q, want %q", i, calls[i], want)
		}
	}
}

// TestCreateSession_NoExistingSkipsKill covers the alternate path: if
// the session doesn't exist (has-session returns error), kill-session
// must NOT run. Otherwise we'd waste a tmux call per spawn.
func TestCreateSession_NoExistingSkipsKill(t *testing.T) {
	calls := []string{}
	stop := withMockedExec(t,
		func(args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			if args[0] == "has-session" {
				return errors.New("no such session")
			}
			return nil
		}, nil)
	defer stop()

	if err := CreateSession("fresh", "/tmp", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for _, c := range calls {
		if strings.HasPrefix(c, "kill-session") {
			t.Errorf("unexpected kill-session call when no session existed: %v", calls)
		}
	}
	if len(calls) != 2 { // has-session + new-session
		t.Errorf("expected 2 calls (has + new), got %d: %v", len(calls), calls)
	}
}

// TestSessionExists_TrueWhenRunSucceeds and the negative variant lock
// down the boolean contract: SessionExists is `run("has-session")`
// returning nil → true.
func TestSessionExists_TrueWhenRunSucceeds(t *testing.T) {
	stop := withMockedExec(t,
		func(args ...string) error { return nil }, nil)
	defer stop()
	if !SessionExists("any") {
		t.Error("SessionExists must return true when has-session succeeds")
	}
}

func TestSessionExists_FalseWhenRunFails(t *testing.T) {
	stop := withMockedExec(t,
		func(args ...string) error { return errors.New("nope") }, nil)
	defer stop()
	if SessionExists("any") {
		t.Error("SessionExists must return false when has-session fails")
	}
}

// TestKillSession_PassesThroughError preserves the error from the
// underlying tmux call so callers know whether to retry.
func TestKillSession_PassesThroughError(t *testing.T) {
	want := errors.New("tmux gone")
	stop := withMockedExec(t,
		func(args ...string) error { return want }, nil)
	defer stop()

	got := KillSession("x")
	if got == nil || !errors.Is(got, want) {
		t.Errorf("KillSession error = %v, want wrapped %v", got, want)
	}
}

// TestListSessions_ParsesOutput covers the line-splitting + empty-line
// filter. Several sessions on multiple lines must come back as a slice.
func TestListSessions_ParsesOutput(t *testing.T) {
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		return "session-a\nsession-b\nsession-c\n\n", nil
	})
	defer stop()

	got, err := ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	want := []string{"session-a", "session-b", "session-c"}
	if len(got) != len(want) {
		t.Fatalf("expected %d sessions, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestListSessions_NoServerRunningReturnsNil exercises the
// "no server running" fallback — tmux running but no daemon → nil
// slice, nil error. Without this, NXD would log spurious "no
// server running" errors at startup before any session was spawned.
func TestListSessions_NoServerRunningReturnsNil(t *testing.T) {
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		return "", errors.New("error connecting: no server running on /tmp/tmux-1000/default")
	})
	defer stop()

	got, err := ListSessions()
	if err != nil {
		t.Errorf("expected nil error for 'no server running', got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
}

// TestListSessions_NoSessionsReturnsNil mirrors the above for the
// "no sessions" message tmux emits when the server is up but empty.
func TestListSessions_NoSessionsReturnsNil(t *testing.T) {
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		return "", errors.New("no sessions")
	})
	defer stop()

	got, err := ListSessions()
	if err != nil {
		t.Errorf("expected nil error for 'no sessions', got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
}

// TestListSessions_RealErrorBubblesUp confirms unrelated errors aren't
// silently swallowed. A permission error or unexpected exit must
// propagate to the caller.
func TestListSessions_RealErrorBubblesUp(t *testing.T) {
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		return "", errors.New("permission denied")
	})
	defer stop()

	if _, err := ListSessions(); err == nil {
		t.Error("expected error to bubble up for non-recoverable tmux failure")
	}
}

// TestSendKeys_AppendsEnter exercises the contract that SendKeys
// always sends an Enter keystroke (the runtime's I/O loop relies on
// this to actually submit the typed command).
func TestSendKeys_AppendsEnter(t *testing.T) {
	var lastArgs []string
	stop := withMockedExec(t,
		func(args ...string) error {
			lastArgs = append([]string{}, args...)
			return nil
		}, nil)
	defer stop()

	if err := SendKeys("session-x", "ls -la"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if len(lastArgs) == 0 || lastArgs[len(lastArgs)-1] != "Enter" {
		t.Errorf("last arg = %v (full %v), want trailing 'Enter'", lastArgs, lastArgs)
	}
}

// TestSendKeysRaw_NoEnter mirrors SendKeys but for the raw variant
// that lets callers send arbitrary key sequences. It must NOT append
// Enter, otherwise the runtime can't issue partial-line edits.
func TestSendKeysRaw_NoEnter(t *testing.T) {
	var lastArgs []string
	stop := withMockedExec(t,
		func(args ...string) error {
			lastArgs = append([]string{}, args...)
			return nil
		}, nil)
	defer stop()

	if err := SendKeysRaw("session-x", "C-c"); err != nil {
		t.Fatalf("SendKeysRaw: %v", err)
	}
	for _, a := range lastArgs {
		if a == "Enter" {
			t.Errorf("SendKeysRaw must not append Enter; got args %v", lastArgs)
		}
	}
}

// TestCapturePaneOutput_TrimsSurroundingWhitespace covers the trim
// step that protects callers from trailing newlines in tmux output.
func TestCapturePaneOutput_TrimsSurroundingWhitespace(t *testing.T) {
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		return "  line one\nline two\n\n", nil
	})
	defer stop()

	got, err := CapturePaneOutput("session-x", 25)
	if err != nil {
		t.Fatalf("CapturePaneOutput: %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("output should be trimmed of trailing newline; got %q", got)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Errorf("output missing content: %q", got)
	}
}

// TestCapturePaneOutput_DefaultLines confirms the lines<=0 branch
// substitutes 50 — without this default, callers passing 0 by
// accident would capture nothing.
func TestCapturePaneOutput_DefaultLines(t *testing.T) {
	var capturedArgs []string
	stop := withMockedExec(t, nil, func(args ...string) (string, error) {
		capturedArgs = append([]string{}, args...)
		return "", nil
	})
	defer stop()

	if _, err := CapturePaneOutput("s", 0); err != nil {
		t.Fatalf("CapturePaneOutput: %v", err)
	}
	hit := false
	for _, a := range capturedArgs {
		if a == "-50" {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected -50 default in args; got %v", capturedArgs)
	}
}
