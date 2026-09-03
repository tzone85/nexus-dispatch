package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func encodeEvent(t *testing.T, evt state.Event) string {
	t.Helper()
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestCheck_HealthyLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	e2 := state.NewEvent(state.EventStoryCreated, "tl", "s1", map[string]any{"id": "s1", "req_id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1)+encodeEvent(t, e2))

	rep, err := state.Check(path)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rep.Lines != 2 || rep.Valid != 2 || rep.Torn || len(rep.Malformed) != 0 {
		t.Errorf("unexpected report: %+v", rep)
	}
	if !rep.Healthy() {
		t.Error("Healthy() should be true")
	}
	if rep.SizeBytes == 0 {
		t.Error("SizeBytes should be set")
	}
	if !rep.LastEventTime.Equal(e2.Timestamp) {
		t.Errorf("LastEventTime = %v, want %v", rep.LastEventTime, e2.Timestamp)
	}
}

func TestCheck_ReportsMalformedAndTorn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1))
	appendRaw(t, path, "{bad-one\n")
	appendRaw(t, path, "\n") // blank lines are ignored, not counted
	appendRaw(t, path, encodeEvent(t, e1))
	appendRaw(t, path, `{"torn":`)

	rep, err := state.Check(path)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rep.Torn {
		t.Error("Torn should be true")
	}
	if len(rep.Malformed) != 1 || rep.Malformed[0].Line != 2 {
		t.Fatalf("Malformed = %+v, want line 2 only", rep.Malformed)
	}
	if !strings.HasPrefix(rep.Malformed[0].Snippet, "{bad-one") {
		t.Errorf("snippet = %q", rep.Malformed[0].Snippet)
	}
	if rep.Lines != 4 || rep.Valid != 2 {
		t.Errorf("Lines=%d Valid=%d, want 4/2", rep.Lines, rep.Valid)
	}
	if rep.Healthy() {
		t.Error("Healthy() should be false")
	}
}

func TestCheck_MissingFile(t *testing.T) {
	if _, err := state.Check(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCheck_LongSnippetIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	appendRaw(t, path, "{"+strings.Repeat("x", 200)+"\n")
	rep, err := state.Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Malformed) != 1 || !strings.HasSuffix(rep.Malformed[0].Snippet, "…") {
		t.Errorf("snippet should be trimmed with an ellipsis: %+v", rep.Malformed)
	}
}

func TestRepair_MovesBadLinesAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	e2 := state.NewEvent(state.EventStoryCreated, "tl", "s1", map[string]any{"id": "s1", "req_id": "r1"})
	original := encodeEvent(t, e1) + "{bad-one\n" + encodeEvent(t, e2) + "garbage\n" + `{"torn":`
	appendRaw(t, path, original)

	moved, err := state.Repair(path)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if moved != 3 {
		t.Errorf("moved = %d, want 3 (2 malformed + torn tail)", moved)
	}

	rep, _ := state.Check(path)
	if !rep.Healthy() || rep.Valid != 2 {
		t.Errorf("log should be healthy with 2 events after repair: %+v", rep)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || string(bak) != original {
		t.Errorf("backup should hold the original log verbatim (err=%v)", err)
	}
	q, _ := os.ReadFile(state.QuarantineName(path))
	for _, want := range []string{"{bad-one", "garbage", `{"torn":`} {
		if !strings.Contains(string(q), want) {
			t.Errorf("quarantine missing %q", want)
		}
	}
	// A strict FileStore now reads it cleanly and appends land after e2.
	fs, err := state.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if err := fs.Append(state.NewEvent(state.EventStoryStarted, "a", "s1", nil)); err != nil {
		t.Fatal(err)
	}
	got, err := fs.List(state.EventFilter{})
	if err != nil || len(got) != 3 {
		t.Errorf("List after repair = %d, %v; want 3", len(got), err)
	}
}

func TestRepair_HealthyLogUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1))
	before, _ := os.Stat(path)

	moved, err := state.Repair(path)
	if err != nil || moved != 0 {
		t.Fatalf("Repair = %d, %v; want 0, nil", moved, err)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("no backup should be written for a healthy log")
	}
	after, _ := os.Stat(path)
	if before.ModTime() != after.ModTime() {
		t.Error("healthy log must not be rewritten")
	}
}

func TestRepair_TornOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1"})
	appendRaw(t, path, encodeEvent(t, e1)+`{"half`)
	moved, err := state.Repair(path)
	if err != nil || moved != 1 {
		t.Fatalf("Repair = %d, %v; want 1, nil", moved, err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != encodeEvent(t, e1) {
		t.Errorf("log should be truncated to the last complete line, got %q", raw)
	}
}

func TestRepair_MissingFile(t *testing.T) {
	if _, err := state.Repair(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("expected error")
	}
}

// buildCompactLog writes a log with two requirements: r1 completed, r2 still
// running. Each has a story with progress + checkpoint events.
func buildCompactLog(t *testing.T, path string) (progressR1, progressR2 state.Event) {
	t.Helper()
	var b strings.Builder
	add := func(e state.Event) { b.WriteString(encodeEvent(t, e)) }
	add(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r1", "title": "a", "description": "d"}))
	add(state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"id": "r2", "title": "b", "description": "d"}))
	add(state.NewEvent(state.EventStoryCreated, "tl", "s1", map[string]any{"id": "s1", "req_id": "r1", "title": "s"}))
	add(state.NewEvent(state.EventStoryCreated, "tl", "s2", map[string]any{"id": "s2", "req_id": "r2", "title": "s"}))
	progressR1 = state.NewEvent(state.EventStoryProgress, "a1", "s1", map[string]any{"msg": "working"})
	add(progressR1)
	add(state.NewEvent(state.EventAgentCheckpoint, "a1", "s1", map[string]any{"iteration": 1}))
	progressR2 = state.NewEvent(state.EventStoryProgress, "a2", "s2", map[string]any{"msg": "working"})
	add(progressR2)
	add(state.NewEvent(state.EventAgentCheckpoint, "a2", "s2", map[string]any{"iteration": 1}))
	add(state.NewEvent(state.EventStoryCompleted, "a1", "s1", nil))
	add(state.NewEvent(state.EventStoryMerged, "merger", "s1", nil))
	add(state.NewEvent(state.EventReqCompleted, "system", "", map[string]any{"id": "r1"}))
	appendRaw(t, path, b.String())
	return progressR1, progressR2
}

func TestCompact_DropsOnlyInformationalEventsOfCompletedReqs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	progressR1, progressR2 := buildCompactLog(t, path)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	res, err := state.Compact(path, now)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Removed != 2 || res.Kept != 9 {
		t.Errorf("Removed=%d Kept=%d, want 2/9", res.Removed, res.Kept)
	}
	wantArchive := filepath.Join(dir, "events.archive-20260903T120000Z.jsonl")
	if res.ArchivePath != wantArchive {
		t.Errorf("ArchivePath = %s, want %s", res.ArchivePath, wantArchive)
	}

	fs, _ := state.NewFileStore(path)
	defer fs.Close()
	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range events {
		ids[e.ID] = true
	}
	if ids[progressR1.ID] {
		t.Error("STORY_PROGRESS of completed r1 should be removed")
	}
	if !ids[progressR2.ID] {
		t.Error("STORY_PROGRESS of running r2 must be kept")
	}
	for _, e := range events {
		if e.Type == state.EventAgentCheckpoint && e.StoryID == "s1" {
			t.Error("AGENT_CHECKPOINT of completed r1 should be removed")
		}
	}

	arch, err := os.ReadFile(wantArchive)
	if err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	if !strings.Contains(string(arch), progressR1.ID) || strings.Count(string(arch), "\n") != 2 {
		t.Errorf("archive should hold exactly the 2 removed events, got %q", arch)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

func TestCompact_NothingToDo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	e := state.NewEvent(state.EventStoryProgress, "a", "s1", nil) // no completed req
	appendRaw(t, path, encodeEvent(t, e))
	res, err := state.Compact(path, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || res.Kept != 1 || res.ArchivePath != "" {
		t.Errorf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("no backup expected when nothing changes")
	}
}

func TestCompact_RefusesUnhealthyLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	appendRaw(t, path, "{bad\n")
	if _, err := state.Compact(path, time.Now()); err == nil || !strings.Contains(err.Error(), "repair") {
		t.Fatalf("expected refusal pointing at repair, got %v", err)
	}
}

func TestCompact_MissingFile(t *testing.T) {
	if _, err := state.Compact(filepath.Join(t.TempDir(), "nope.jsonl"), time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRebuild_TakesAndReleasesLock(t *testing.T) {
	es, ps := openTxTestStores(t)
	seedReqs(t, es, ps, 2)

	acquired, released := 0, 0
	lock := func() (func(), error) {
		acquired++
		return func() { released++ }, nil
	}
	if err := state.Rebuild(context.Background(), es, ps, lock); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if acquired != 1 || released != 1 {
		t.Errorf("acquired=%d released=%d, want 1/1", acquired, released)
	}
	reqs, _ := ps.ListRequirements()
	if len(reqs) != 2 {
		t.Errorf("rows = %d, want 2", len(reqs))
	}
}

func TestRebuild_LockFailureAborts(t *testing.T) {
	es, ps := openTxTestStores(t)
	seedReqs(t, es, ps, 1)
	lock := func() (func(), error) { return nil, errors.New("pipeline running") }
	err := state.Rebuild(context.Background(), es, ps, lock)
	if err == nil || !strings.Contains(err.Error(), "pipeline running") {
		t.Fatalf("expected lock error, got %v", err)
	}
}

func TestRebuild_NilLockRebuildsDirectly(t *testing.T) {
	es, ps := openTxTestStores(t)
	seedReqs(t, es, ps, 1)
	if err := state.Rebuild(context.Background(), es, ps, nil); err != nil {
		t.Fatal(err)
	}
}

func TestQuarantineName(t *testing.T) {
	got := state.QuarantineName("/x/.nxd/events.jsonl")
	if got != "/x/.nxd/events.quarantine.jsonl" {
		t.Errorf("QuarantineName = %s", got)
	}
}
