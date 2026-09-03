package state_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// appendRaw writes raw bytes to the end of path without any validation,
// simulating a foreign writer or a crash mid-write.
func appendRaw(t *testing.T, path string, raw string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(raw); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Defect 1: bufio.Scanner's default 64 KB token limit made a single large
// QA payload brick every command with "token too long". Lines up to 16 MB
// must read back.
func TestFileStore_ReadsPreexisting200KBLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	big := strings.Repeat("x", 200*1024)
	evt := state.NewEvent(state.EventStoryQAFailed, "qa", "s-1", map[string]any{"output": big})
	line, _ := json.Marshal(evt)
	appendRaw(t, path, string(line)+"\n")

	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer fs.Close()

	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if got := state.DecodePayload(events[0].Payload)["output"]; got != big {
		t.Errorf("200KB payload did not round-trip (len %d)", len(got.(string)))
	}
}

// Defect 1 (write side): a payload larger than max_event_bytes is truncated
// longest-string-first — never dropped — and flagged truncated:true.
func TestFileStore_Append_TruncatesOversizedPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	fs, err := state.NewFileStore(path) // default 1 MB cap
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer fs.Close()

	huge := strings.Repeat("y", 3<<20) // 3 MB
	small := "keep-me"
	evt := state.NewEvent(state.EventStoryQAFailed, "qa", "s-1", map[string]any{
		"output": huge,
		"short":  small,
		"code":   float64(2),
	})
	if err := fs.Append(evt); err != nil {
		t.Fatalf("Append: %v", err)
	}

	raw, _ := os.ReadFile(path)
	if n := len(raw); n > state.DefaultMaxEventBytes {
		t.Errorf("encoded line is %d bytes, exceeds cap %d", n, state.DefaultMaxEventBytes)
	}
	if bytes.Count(raw, []byte{'\n'}) != 1 {
		t.Errorf("expected exactly one line, got %d newlines", bytes.Count(raw, []byte{'\n'}))
	}

	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	p := state.DecodePayload(events[0].Payload)
	if p["truncated"] != true {
		t.Errorf("payload should carry truncated:true, got %v", p["truncated"])
	}
	if p["short"] != small {
		t.Errorf("small string must be untouched, got %v", p["short"])
	}
	if p["code"] != float64(2) {
		t.Errorf("non-string values must be untouched, got %v", p["code"])
	}
	out, _ := p["output"].(string)
	if !strings.HasSuffix(out, " bytes]") || !strings.Contains(out, "…[truncated ") {
		t.Errorf("long string should end with truncation marker, got tail %q", out[max(0, len(out)-40):])
	}
	if !strings.HasPrefix(out, "yyyy") {
		t.Errorf("truncated string should keep its prefix")
	}
}

func TestFileStore_Append_SmallPayloadUntouched(t *testing.T) {
	dir := t.TempDir()
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"), state.WithMaxEventBytes(4096))
	defer fs.Close()

	evt := state.NewEvent(state.EventStoryProgress, "a", "s-1", map[string]any{"msg": "hello"})
	if err := fs.Append(evt); err != nil {
		t.Fatalf("Append: %v", err)
	}
	events, _ := fs.List(state.EventFilter{})
	p := state.DecodePayload(events[0].Payload)
	if _, ok := p["truncated"]; ok {
		t.Errorf("small payload must not be flagged truncated")
	}
	if p["msg"] != "hello" {
		t.Errorf("msg = %v", p["msg"])
	}
}

// A payload with no strings to trim is written anyway — never dropped.
func TestFileStore_Append_OversizedWithoutStringsStillWritten(t *testing.T) {
	dir := t.TempDir()
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"), state.WithMaxEventBytes(200))
	defer fs.Close()

	nums := make([]any, 200)
	for i := range nums {
		nums[i] = float64(i)
	}
	evt := state.NewEvent(state.EventStoryProgress, "a", "s-1", map[string]any{"nums": nums})
	if err := fs.Append(evt); err != nil {
		t.Fatalf("Append: %v", err)
	}
	events, err := fs.List(state.EventFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("List = %d events, %v", len(events), err)
	}
}

// Defect 2: a torn last line (crash mid-write, no trailing newline) must
// not brick strict mode. It is skipped and logged once.
func TestFileStore_List_SkipsTornLastLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	good := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	line, _ := json.Marshal(good)
	appendRaw(t, path, string(line)+"\n")
	appendRaw(t, path, `{"id":"01ABC","type":"STORY_PRO`) // torn, no newline

	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer fs.Close()

	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("strict List must tolerate a torn tail: %v", err)
	}
	if len(events) != 1 || events[0].ID != good.ID {
		t.Fatalf("got %+v, want only the good event", events)
	}
	n, err := fs.Count(state.EventFilter{})
	if err != nil || n != 1 {
		t.Errorf("Count = %d, %v; want 1", n, err)
	}
}

// A malformed line that DOES end in a newline is corruption, not a torn
// write, and strict mode still refuses it even when it is last.
func TestFileStore_List_StrictStillRejectsTerminatedGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	appendRaw(t, path, `{"id":"01ABC","type":"STORY_PRO`+"\n")

	fs, _ := state.NewFileStore(path)
	defer fs.Close()
	if _, err := fs.List(state.EventFilter{}); err == nil {
		t.Fatal("expected strict-mode error for newline-terminated garbage")
	}
}

// Appending after a torn tail must not glue the new event onto the
// fragment. The fragment is quarantined and the log truncated to the last
// complete line before the new write.
func TestFileStore_Append_AfterTornTailQuarantinesFragment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	good := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	line, _ := json.Marshal(good)
	appendRaw(t, path, string(line)+"\n")
	appendRaw(t, path, `{"torn":tr`)

	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer fs.Close()

	next := state.NewEvent(state.EventStoryCreated, "tl", "s-1", map[string]any{"id": "s-1"})
	if err := fs.Append(next); err != nil {
		t.Fatalf("Append: %v", err)
	}
	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	q, err := os.ReadFile(filepath.Join(dir, "events.quarantine.jsonl"))
	if err != nil {
		t.Fatalf("quarantine file missing: %v", err)
	}
	if !strings.Contains(string(q), `{"torn":tr`) {
		t.Errorf("quarantine should hold the torn fragment, got %q", q)
	}
}

// Lenient mode moves malformed lines to events.quarantine.jsonl instead of
// silently dropping them; the log is rewritten without them and subsequent
// appends still land in the live file.
func TestFileStore_Lenient_QuarantinesMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	e2 := state.NewEvent(state.EventStoryCreated, "tl", "s-1", map[string]any{"id": "s-1"})
	l1, _ := json.Marshal(e1)
	l2, _ := json.Marshal(e2)
	appendRaw(t, path, string(l1)+"\n")
	appendRaw(t, path, "{garbage-one\n")
	appendRaw(t, path, string(l2)+"\n")
	appendRaw(t, path, "not json at all\n")

	t.Setenv("NXD_EVENTS_LENIENT", "1")
	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer fs.Close()

	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("lenient List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	q, err := os.ReadFile(filepath.Join(dir, "events.quarantine.jsonl"))
	if err != nil {
		t.Fatalf("quarantine file missing: %v", err)
	}
	if !strings.Contains(string(q), "{garbage-one") || !strings.Contains(string(q), "not json at all") {
		t.Errorf("quarantine should hold both bad lines, got %q", q)
	}

	// The live log no longer contains the bad lines, so a strict reader is happy.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "garbage") {
		t.Errorf("bad lines should have been moved out of the live log")
	}

	// Appends after the rewrite go to the live (renamed) file, not the old inode.
	e3 := state.NewEvent(state.EventStoryStarted, "a", "s-1", nil)
	if err := fs.Append(e3); err != nil {
		t.Fatalf("Append after quarantine: %v", err)
	}
	t.Setenv("NXD_EVENTS_LENIENT", "")
	strict, _ := state.NewFileStore(path)
	defer strict.Close()
	got, err := strict.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("strict List after quarantine: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d events after append, want 3", len(got))
	}
}

func TestFileStore_FsyncOptionRoundTrips(t *testing.T) {
	dir := t.TempDir()
	for _, sync := range []bool{true, false} {
		fs, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"), state.WithFsync(sync))
		if err != nil {
			t.Fatalf("NewFileStore(fsync=%v): %v", sync, err)
		}
		if err := fs.Append(state.NewEvent(state.EventStoryProgress, "a", "s", nil)); err != nil {
			t.Errorf("Append(fsync=%v): %v", sync, err)
		}
		fs.Close()
	}
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer fs.Close()
	n, _ := fs.Count(state.EventFilter{})
	if n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}
}

// TruncatePayload is exported for the maintenance tooling; cover its
// edge cases directly.
func TestTruncatePayload(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]any
		budget   int
		wantTrun bool
	}{
		{"fits", map[string]any{"a": "short"}, 1000, false},
		{"nested string trimmed", map[string]any{"outer": map[string]any{"inner": strings.Repeat("z", 500)}}, 200, true},
		{"array string trimmed", map[string]any{"arr": []any{strings.Repeat("z", 500), "s"}}, 200, true},
		{"nil payload", nil, 10, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := state.TruncatePayload(tc.payload, tc.budget)
			if truncated != tc.wantTrun {
				t.Fatalf("truncated = %v, want %v", truncated, tc.wantTrun)
			}
			if tc.wantTrun {
				enc, _ := json.Marshal(got)
				if len(enc) > tc.budget {
					t.Errorf("encoded %d bytes > budget %d", len(enc), tc.budget)
				}
				if got["truncated"] != true {
					t.Errorf("missing truncated flag")
				}
			}
		})
	}
}
