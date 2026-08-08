package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a concurrency-safe bytes.Buffer: followLogPoll writes from its
// own goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestFollowLog_PicksUpAppendedLines guards the tail-follow regression: the
// previous implementation reused a single bufio.Scanner, which latches EOF and
// so never emitted lines appended after follow began. A working follow must:
// (1) skip pre-existing content (it starts at end), and (2) surface every line
// appended afterwards.
func TestFollowLog_PicksUpAppendedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace_events.jsonl")
	// Pre-existing content — must NOT be emitted (follow seeks to end first).
	if err := os.WriteFile(path, []byte(`{"type":"OLD_PREEXISTING"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out syncBuffer
	stop := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- followLogPoll(path, true, &out, 10*time.Millisecond, stop, ready) }()
	<-ready // follower has seeked to end; appends below are guaranteed to be "after"

	// Append new lines after follow has started.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"NEW_ONE"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"NEW_TWO"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Wait (bounded) for the follower to surface both appended lines.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if strings.Contains(s, "NEW_ONE") && strings.Contains(s, "NEW_TWO") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("followLogPoll returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "NEW_ONE") || !strings.Contains(got, "NEW_TWO") {
		t.Errorf("follow did not surface appended lines; got %q", got)
	}
	if strings.Contains(got, "OLD_PREEXISTING") {
		t.Errorf("follow must start at end, but emitted pre-existing content: %q", got)
	}
}

// TestFollowLog_ReassemblesPartialLine ensures a record written in two steps
// (bytes, then the terminating newline) is emitted once, whole, and only after
// the newline arrives — never as a truncated fragment.
func TestFollowLog_ReassemblesPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace_events.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	var out syncBuffer
	stop := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- followLogPoll(path, true, &out, 10*time.Millisecond, stop, ready) }()
	<-ready // follower has seeked to end

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// Write a partial record with no newline yet.
	if _, err := f.WriteString(`{"type":"PART`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the follower observe the partial
	if strings.Contains(out.String(), "PART") {
		t.Errorf("partial line emitted before newline arrived: %q", out.String())
	}
	// Complete the record.
	if _, err := f.WriteString(`IAL"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), `{"type":"PARTIAL"}`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("followLogPoll returned error: %v", err)
	}
	if !strings.Contains(out.String(), `{"type":"PARTIAL"}`) {
		t.Errorf("completed record not emitted whole; got %q", out.String())
	}
}
