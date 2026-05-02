package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// fakeEventStore captures appends so the test can assert against them
// without touching the real filestore.
type fakeEventStore struct {
	events []state.Event
	err    error
}

func (f *fakeEventStore) Append(e state.Event) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, e)
	return nil
}

type fakeProjStore struct {
	projected []state.Event
	err       error
}

func (f *fakeProjStore) Project(e state.Event) error {
	if f.err != nil {
		return f.err
	}
	f.projected = append(f.projected, e)
	return nil
}

func TestEmitStageCompleted_RecordsEventWithDuration(t *testing.T) {
	es := &fakeEventStore{}
	ps := &fakeProjStore{}

	start := time.Now().Add(-150 * time.Millisecond)
	EmitStageCompleted(es, ps, "monitor", "STORY-1", "review", "success", start)

	if len(es.events) != 1 {
		t.Fatalf("expected 1 appended event, got %d", len(es.events))
	}
	if len(ps.projected) != 1 {
		t.Fatalf("expected 1 projected event, got %d", len(ps.projected))
	}
	evt := es.events[0]
	if evt.Type != state.EventStageCompleted {
		t.Errorf("event type = %s, want %s", evt.Type, state.EventStageCompleted)
	}
	if evt.StoryID != "STORY-1" {
		t.Errorf("story_id = %s, want STORY-1", evt.StoryID)
	}
	payload := state.DecodePayload(evt.Payload)
	if payload["stage"] != "review" {
		t.Errorf("stage = %v, want review", payload["stage"])
	}
	if payload["outcome"] != "success" {
		t.Errorf("outcome = %v, want success", payload["outcome"])
	}
	// duration_ms decodes as float64 from JSON.
	dur, ok := payload["duration_ms"].(float64)
	if !ok {
		t.Fatalf("duration_ms not a number: %T %v", payload["duration_ms"], payload["duration_ms"])
	}
	if dur < 100 {
		t.Errorf("duration_ms = %v, want >=100ms", dur)
	}
}

func TestEmitStageCompleted_NilStoresNoOp(t *testing.T) {
	// Should not panic when stores are nil — observability paths must be
	// safe on best-effort calls.
	EmitStageCompleted(nil, nil, "agent", "S-1", "qa", "failure", time.Now())
}

func TestEmitStageCompleted_AppendErrorSkipsProject(t *testing.T) {
	es := &fakeEventStore{err: errors.New("disk full")}
	ps := &fakeProjStore{}
	EmitStageCompleted(es, ps, "monitor", "", "plan", "failure", time.Now().Add(-10*time.Millisecond))
	if len(ps.projected) != 0 {
		t.Errorf("project should be skipped when append fails, got %d", len(ps.projected))
	}
}
