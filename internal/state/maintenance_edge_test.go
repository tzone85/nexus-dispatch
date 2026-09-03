package state_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestSQLiteStore_AckDirectWriteAdvancesWatermark(t *testing.T) {
	_, ps := openTxTestStores(t)
	if err := ps.AckDirectWrite(3); err != nil {
		t.Fatalf("AckDirectWrite: %v", err)
	}
	n, err := ps.AppliedEventCount()
	if err != nil || n != 3 {
		t.Errorf("AppliedEventCount = %d, %v; want 3", n, err)
	}
}

func TestSQLiteStore_StoryRewritten_AllFields(t *testing.T) {
	_, ps := openTxTestStores(t)
	created := state.NewEvent(state.EventStoryCreated, "tl", "s1", map[string]any{
		"id": "s1", "req_id": "r1", "title": "old", "description": "old", "acceptance_criteria": "old", "complexity": 1,
	})
	if err := ps.Project(created); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		changes map[string]any
		check   func(state.Story) bool
	}{
		{"float complexity", map[string]any{"complexity": float64(5)}, func(s state.Story) bool { return s.Complexity == 5 }},
		{"int complexity", map[string]any{"complexity": 8}, func(s state.Story) bool { return s.Complexity == 8 }},
		{"bad complexity ignored", map[string]any{"complexity": "x"}, func(s state.Story) bool { return s.Complexity == 8 }},
		{"title", map[string]any{"title": "new"}, func(s state.Story) bool { return s.Title == "new" }},
		{"description", map[string]any{"description": "nd"}, func(s state.Story) bool { return s.Description == "nd" }},
		{"criteria", map[string]any{"acceptance_criteria": "ac"}, func(s state.Story) bool { return s.AcceptanceCriteria == "ac" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Payload goes through JSON, so ints become float64 — pass the
			// raw map via a pre-encoded event to keep the int case honest.
			evt := state.Event{ID: "x", Type: state.EventStoryRewritten, StoryID: "s1", Timestamp: time.Now()}
			evt.Payload = mustJSON(t, map[string]any{"changes": tc.changes})
			if err := ps.Project(evt); err != nil {
				t.Fatalf("Project: %v", err)
			}
			story, err := ps.GetStory("s1")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(story) {
				t.Errorf("story after %s: %+v", tc.name, story)
			}
			if story.Status != "draft" {
				t.Errorf("status = %s, want draft", story.Status)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonMarshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRepair_ReadOnlyDirFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	appendRaw(t, path, "{bad\n")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := state.Repair(path); err == nil {
		t.Fatal("expected error writing backup into a read-only directory")
	}
}

func TestCompact_ReadOnlyDirFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	buildCompactLog(t, path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := state.Compact(path, time.Now()); err == nil {
		t.Fatal("expected error creating archive in a read-only directory")
	}
}

// Lenient List on a log that also has a torn tail quarantines both the
// malformed lines and the fragment.
func TestFileStore_Lenient_TornAndMalformedTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1)+"{bad\n"+`{"torn":`)

	t.Setenv("NXD_EVENTS_LENIENT", "1")
	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	events, err := fs.List(state.EventFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("List = %d, %v; want 1", len(events), err)
	}
	q, _ := os.ReadFile(state.QuarantineName(path))
	if !strings.Contains(string(q), "{bad") || !strings.Contains(string(q), `{"torn":`) {
		t.Errorf("quarantine = %q", q)
	}
	rep, _ := state.Check(path)
	if !rep.Healthy() {
		t.Errorf("log should be healthy after lenient quarantine: %+v", rep)
	}
}

// Torn tail is logged only once per store even across repeated reads.
func TestFileStore_TornTailReadTwice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	appendRaw(t, path, `{"torn":`)
	fs, _ := state.NewFileStore(path)
	defer fs.Close()
	for i := 0; i < 2; i++ {
		n, err := fs.Count(state.EventFilter{})
		if err != nil || n != 0 {
			t.Fatalf("Count #%d = %d, %v", i, n, err)
		}
	}
}

// A torn fragment larger than one backward-scan chunk is still found.
func TestFileStore_Append_LargeTornFragment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1)+`{"big":"`+strings.Repeat("q", 200*1024))
	fs, _ := state.NewFileStore(path)
	defer fs.Close()
	if err := fs.Append(state.NewEvent(state.EventStoryCreated, "tl", "s1", nil)); err != nil {
		t.Fatal(err)
	}
	events, err := fs.List(state.EventFilter{})
	if err != nil || len(events) != 2 {
		t.Fatalf("List = %d, %v; want 2", len(events), err)
	}
}

// Two successive cuts on the same string report a cumulative byte count.
func TestTruncatePayload_CumulativeMarker(t *testing.T) {
	p := map[string]any{"s": strings.Repeat("a", 1000)}
	p, ok := state.TruncatePayload(p, 600)
	if !ok {
		t.Fatal("first cut should truncate")
	}
	p, ok = state.TruncatePayload(p, 300)
	if !ok {
		t.Fatal("second cut should truncate")
	}
	s := p["s"].(string)
	if strings.Count(s, "…[truncated") != 1 {
		t.Errorf("markers should not nest: %q", s[len(s)-60:])
	}
	var n int
	if _, err := sscanf(s[strings.LastIndex(s, "…"):], "…[truncated %d bytes]", &n); err != nil || n < 700 {
		t.Errorf("cumulative removed = %d (%v), want >= 700", n, err)
	}
}

// Strings that are already minimal cannot be cut further; the payload is
// still returned (and written by Append) rather than dropped.
func TestTruncatePayload_TinyStringsCannotShrink(t *testing.T) {
	p := map[string]any{"a": "0123456789ab", "b": "0123456789ab"}
	_, ok := state.TruncatePayload(p, 10)
	if ok {
		t.Error("nothing should be truncated when every string is already <= 16 bytes")
	}
}

// Multi-byte runes are not split by a cut.
func TestTruncatePayload_RespectsUTF8(t *testing.T) {
	p := map[string]any{"s": strings.Repeat("é", 400)}
	p, ok := state.TruncatePayload(p, 300)
	if !ok {
		t.Fatal("should truncate")
	}
	s := p["s"].(string)
	if !isValidUTF8(s) {
		t.Errorf("cut produced invalid UTF-8")
	}
}

var (
	jsonMarshal = json.Marshal
	sscanf      = fmt.Sscanf
	isValidUTF8 = utf8.ValidString
)
