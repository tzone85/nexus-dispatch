package state_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newAttemptTestStore(t *testing.T) *state.FileStore {
	t.Helper()
	fs, err := state.NewFileStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

func TestNewEventForAttempt_StampsFieldAndPayload(t *testing.T) {
	evt := state.NewEventForAttempt(state.EventStoryStarted, "agent-1", "s-1", "s-1-a01", map[string]any{"tier": 0})

	if evt.AttemptID != "s-1-a01" {
		t.Fatalf("AttemptID = %q, want s-1-a01", evt.AttemptID)
	}
	payload := state.DecodePayload(evt.Payload)
	if payload["attempt_id"] != "s-1-a01" {
		t.Fatalf("payload attempt_id = %v, want s-1-a01", payload["attempt_id"])
	}
	if payload["tier"] != float64(0) {
		t.Fatalf("original payload fields must be preserved, got %v", payload)
	}
}

func TestNewEventForAttempt_NilPayloadStillCarriesAttempt(t *testing.T) {
	evt := state.NewEventForAttempt(state.EventStoryCompleted, "agent-1", "s-1", "att-1", nil)
	payload := state.DecodePayload(evt.Payload)
	if payload["attempt_id"] != "att-1" {
		t.Fatalf("payload attempt_id = %v, want att-1", payload["attempt_id"])
	}
}

func TestNewEventForAttempt_EmptyAttemptBehavesLikeNewEvent(t *testing.T) {
	evt := state.NewEventForAttempt(state.EventStoryCompleted, "agent-1", "s-1", "", nil)
	if evt.AttemptID != "" {
		t.Fatalf("expected empty AttemptID, got %q", evt.AttemptID)
	}
	if evt.Payload != nil {
		t.Fatalf("expected nil payload for legacy call, got %s", evt.Payload)
	}
}

func TestEvent_AttemptIDOmittedFromJSONWhenEmpty(t *testing.T) {
	legacy := state.NewEvent(state.EventStoryCompleted, "agent-1", "s-1", nil)
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, present := m["attempt_id"]; present {
		t.Fatalf("attempt_id must be omitted for legacy events, got %s", raw)
	}

	stamped := state.NewEventForAttempt(state.EventStoryCompleted, "agent-1", "s-1", "att-9", nil)
	raw, _ = json.Marshal(stamped)
	var back state.Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.AttemptID != "att-9" {
		t.Fatalf("round-trip AttemptID = %q, want att-9", back.AttemptID)
	}
}

func TestFileStore_FilterByAttemptID(t *testing.T) {
	fs := newAttemptTestStore(t)

	must := func(evt state.Event) {
		t.Helper()
		if err := fs.Append(evt); err != nil {
			t.Fatal(err)
		}
	}
	must(state.NewEventForAttempt(state.EventStoryCompleted, "a1", "s-1", "att-1", nil))
	must(state.NewEventForAttempt(state.EventStoryCompleted, "a2", "s-1", "att-2", nil))
	must(state.NewEvent(state.EventStoryCompleted, "a0", "s-1", nil)) // legacy, no attempt

	tests := []struct {
		name   string
		filter state.EventFilter
		want   int
	}{
		{"no attempt filter returns all", state.EventFilter{StoryID: "s-1"}, 3},
		{"attempt 1 only", state.EventFilter{StoryID: "s-1", AttemptID: "att-1"}, 1},
		{"attempt 2 only", state.EventFilter{Type: state.EventStoryCompleted, AttemptID: "att-2"}, 1},
		{"unknown attempt matches nothing", state.EventFilter{AttemptID: "att-x"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := fs.List(tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != tc.want {
				t.Fatalf("List: got %d events, want %d", len(events), tc.want)
			}
			n, err := fs.Count(tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if n != tc.want {
				t.Fatalf("Count: got %d, want %d", n, tc.want)
			}
			for _, e := range events {
				if tc.filter.AttemptID != "" && e.AttemptID != tc.filter.AttemptID {
					t.Fatalf("event %s has AttemptID %q, filter wanted %q", e.ID, e.AttemptID, tc.filter.AttemptID)
				}
			}
		})
	}
}
