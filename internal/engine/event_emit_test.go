package engine

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// captureLog redirects the default logger's output to a buffer for the duration
// of the test, restoring the previous output on cleanup.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

// Tests reuse fakeEventStore + fakeProjStore from stage_timing_test.go;
// emitEventOrLog accepts the same narrow interfaces.

func TestEmitEventOrLog_HappyPath(t *testing.T) {
	logBuf := captureLog(t)
	es := &fakeEventStore{}
	ps := &fakeProjStore{}
	evt := state.NewEvent(state.EventStoryRecovery, "test", "story-1", nil)

	emitEventOrLog(es, ps, evt)

	if len(es.events) != 1 {
		t.Fatalf("expected 1 append, got %d", len(es.events))
	}
	if len(ps.projected) != 1 {
		t.Fatalf("expected 1 projected, got %d", len(ps.projected))
	}
	if strings.Contains(logBuf.String(), "event-drop") || strings.Contains(logBuf.String(), "event-partial") {
		t.Errorf("happy path should not log drop/partial: %q", logBuf.String())
	}
}

func TestEmitEventOrLog_AppendFails_Logs(t *testing.T) {
	logBuf := captureLog(t)
	es := &fakeEventStore{err: errors.New("disk full")}
	ps := &fakeProjStore{}
	evt := state.NewEvent(state.EventStoryRecovery, "test", "story-2", nil)

	emitEventOrLog(es, ps, evt)

	if len(ps.projected) != 0 {
		t.Fatalf("project must NOT run when append fails (would produce drift)")
	}
	if !strings.Contains(logBuf.String(), "[event-drop]") {
		t.Errorf("want [event-drop] tag in log, got: %q", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "story-2") {
		t.Errorf("log should include story id for grep: %q", logBuf.String())
	}
}

func TestEmitEventOrLog_ProjectFails_LogsPartial(t *testing.T) {
	logBuf := captureLog(t)
	es := &fakeEventStore{}
	ps := &fakeProjStore{err: errors.New("sqlite locked")}
	evt := state.NewEvent(state.EventStoryRecovery, "test", "story-3", nil)

	emitEventOrLog(es, ps, evt)

	if len(es.events) != 1 {
		t.Fatalf("expected 1 append before projection failed")
	}
	if !strings.Contains(logBuf.String(), "[event-partial]") {
		t.Errorf("want [event-partial] tag on append-ok-but-project-fail, got: %q", logBuf.String())
	}
}

func TestEmitEventOrLog_NilStoresAreSafe(t *testing.T) {
	logBuf := captureLog(t)
	evt := state.NewEvent(state.EventStoryRecovery, "test", "story-4", nil)

	emitEventOrLog(nil, nil, evt)

	if !strings.Contains(logBuf.String(), "[event-drop]") {
		t.Errorf("want [event-drop] tag on nil stores, got: %q", logBuf.String())
	}
}
