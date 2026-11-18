package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// F7: a half-written event line in events.jsonl previously short-
// circuited as `continue`, silently dropping the corrupt record. Because
// the projection store, retry counter, metrics aggregator, and resume
// logic all re-derive truth from this file, that silent skip let the
// rest of NXD run on a degraded view of state. Default behaviour is now
// to surface the corruption with a line number.
func TestFileStore_List_SurfacesCorruptLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Close()

	// Append a half-written line that json.Unmarshal will reject.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{not json\n")
	f.Close()

	store2, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()
	_, err = store2.List(state.EventFilter{})
	if err == nil {
		t.Fatal("expected error on corrupt line, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q should cite line 2", err)
	}
}

// Lenient mode preserves the legacy silent-skip behaviour for emergency
// recovery.
func TestFileStore_List_LenientSkipsCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	store, _ := state.NewFileStore(path)
	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Close()

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{garbage\n")
	f.Close()

	t.Setenv("NXD_EVENTS_LENIENT", "1")
	store2, _ := state.NewFileStore(path)
	defer store2.Close()
	events, err := store2.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("lenient mode should not error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 valid event, got %d", len(events))
	}
}

func TestFileStore_AppendAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	evt1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"title": "Add auth"})
	evt2 := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{"title": "OAuth middleware"})

	if err := store.Append(evt1); err != nil {
		t.Fatalf("append evt1: %v", err)
	}
	if err := store.Append(evt2); err != nil {
		t.Fatalf("append evt2: %v", err)
	}

	events, err := store.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != state.EventReqSubmitted {
		t.Fatalf("expected REQ_SUBMITTED, got %s", events[0].Type)
	}
	if events[1].Type != state.EventStoryCreated {
		t.Fatalf("expected STORY_CREATED, got %s", events[1].Type)
	}
}

// TestFileStore_List_HandlesEventOverDefaultScanBuffer proves a single event
// larger than bufio.Scanner's default 64 KB token limit does not make the
// entire event log unreadable. QA/build-failure events embed the full combined
// tool output in their feedback, which routinely exceeds 64 KB; before the
// buffer was raised, one such line made List/Count fail with bufio.ErrTooLong,
// stalling attempt-counting, escalation, reporting, and the dashboard.
func TestFileStore_List_HandlesEventOverDefaultScanBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	// A ~256 KB feedback blob — well over the 64 KB default scan-token cap and
	// realistic for a verbose `go test ./...` failure.
	bigFeedback := strings.Repeat("build failure line with lots of detail\n", 7000)
	if len(bigFeedback) < 64*1024 {
		t.Fatalf("test blob too small (%d bytes) to exercise the buffer limit", len(bigFeedback))
	}

	if err := store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"title": "before"})); err != nil {
		t.Fatalf("append small: %v", err)
	}
	if err := store.Append(state.NewEvent(state.EventStoryQAFailed, "monitor", "s-001", map[string]any{"feedback": bigFeedback})); err != nil {
		t.Fatalf("append large: %v", err)
	}
	if err := store.Append(state.NewEvent(state.EventStoryCreated, "tech-lead", "s-002", map[string]any{"title": "after"})); err != nil {
		t.Fatalf("append small 2: %v", err)
	}

	events, err := store.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list must not fail on an oversized event line: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events across the oversized line, got %d", len(events))
	}
	// The oversized event must round-trip intact, and events after it must still
	// be readable (a buffer overflow would have truncated the whole read).
	if events[2].Type != state.EventStoryCreated {
		t.Fatalf("event after the oversized line was lost: got %s", events[2].Type)
	}
}

func TestFileStore_FilterByType(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-2", nil))

	events, _ := store.List(state.EventFilter{Type: state.EventStoryCreated})
	if len(events) != 2 {
		t.Fatalf("expected 2 story events, got %d", len(events))
	}
}

func TestFileStore_FilterByStoryID(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventStoryStarted, "jr-1", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryStarted, "jr-2", "s-2", nil))
	store.Append(state.NewEvent(state.EventStoryCompleted, "jr-1", "s-1", nil))

	events, _ := store.List(state.EventFilter{StoryID: "s-1"})
	if len(events) != 2 {
		t.Fatalf("expected 2 events for s-1, got %d", len(events))
	}
}

func TestFileStore_FilterByAgentID(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventStoryStarted, "jr-1", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryStarted, "jr-2", "s-2", nil))

	events, _ := store.List(state.EventFilter{AgentID: "jr-1"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event for jr-1, got %d", len(events))
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	store1, _ := state.NewFileStore(path)
	store1.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store1.Close()

	store2, _ := state.NewFileStore(path)
	defer store2.Close()
	events, _ := store2.List(state.EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after reopen, got %d", len(events))
	}
}

func TestFileStore_Count(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-1", nil))

	count, _ := store.Count(state.EventFilter{})
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	count, _ = store.Count(state.EventFilter{Type: state.EventReqSubmitted})
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}

func TestFileStore_Limit(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(state.NewEvent(state.EventStoryProgress, "jr-1", "s-1", nil))
	}

	events, _ := store.List(state.EventFilter{Limit: 3})
	if len(events) != 3 {
		t.Fatalf("expected 3 events with limit, got %d", len(events))
	}
}

// TestFileStore_Limit_ReturnsMostRecent pins the semantics that every
// "recent activity" / live-tail caller (web BuildSnapshot "Last 50 events",
// the dashboard activity feed, the Hub delta push) relies on: Limit returns
// the NEWEST N events, in chronological order — not the oldest N. The bug was
// that readAndFilter broke out of the scan the moment it had collected Limit
// events, truncating from the FRONT of the log.
func TestFileStore_Limit_ReturnsMostRecent(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	// Append 10 distinguishable events, tagged 0..9 in payload order.
	for i := 0; i < 10; i++ {
		store.Append(state.NewEvent(state.EventStoryProgress, "jr-1", "s-1", map[string]any{
			"seq": i,
		}))
	}

	events, err := store.List(state.EventFilter{Limit: 3})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	var got []int
	for _, e := range events {
		payload := state.DecodePayload(e.Payload)
		seq, _ := payload["seq"].(float64)
		got = append(got, int(seq))
	}

	// Tail semantics: the last three appended (7, 8, 9), oldest-to-newest.
	want := []int{7, 8, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Limit returned the wrong slice: got seq %v, want the most recent %v", got, want)
		}
	}
}
