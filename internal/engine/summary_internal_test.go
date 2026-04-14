package engine

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestFormatWaveTime_ZeroStart(t *testing.T) {
	got := formatWaveTime(time.Time{}, time.Now())
	if got != "—" {
		t.Errorf("expected em dash for zero start, got %q", got)
	}
}

func TestFormatWaveTime_ZeroEnd(t *testing.T) {
	start := time.Date(2026, 4, 10, 14, 30, 0, 0, time.UTC)
	got := formatWaveTime(start, time.Time{})
	if got == "" || got == "—" {
		t.Errorf("expected time string, got %q", got)
	}
}

func TestFormatWaveTime_Range(t *testing.T) {
	start := time.Date(2026, 4, 10, 14, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 10, 15, 30, 0, 0, time.UTC)
	got := formatWaveTime(start, end)
	if got == "" {
		t.Error("expected non-empty range")
	}
	// Should contain a dash/en-dash separator
	if len(got) < 5 {
		t.Errorf("expected range format, got %q", got)
	}
}

func TestFormatWaveTime_ShortDuration(t *testing.T) {
	start := time.Date(2026, 4, 10, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Second) // less than a minute
	got := formatWaveTime(start, end)
	// Should show just the start time (not a range)
	if got == "" {
		t.Error("expected time string")
	}
}

func TestExtractPRNumberFromURL_Valid(t *testing.T) {
	got := extractPRNumberFromURL("https://github.com/user/repo/pull/42")
	if got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestExtractPRNumberFromURL_TrailingSlash(t *testing.T) {
	got := extractPRNumberFromURL("https://github.com/user/repo/pull/99/")
	if got != 99 {
		t.Errorf("expected 99, got %d", got)
	}
}

func TestExtractPRNumberFromURL_Empty(t *testing.T) {
	got := extractPRNumberFromURL("")
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestExtractPRNumberFromURL_NonNumeric(t *testing.T) {
	got := extractPRNumberFromURL("https://github.com/user/repo/pull/abc")
	if got != 0 {
		t.Errorf("expected 0 for non-numeric, got %d", got)
	}
}

func TestBuildStoryTimings_Empty(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	timings := buildStoryTimings(es, nil)
	if len(timings) != 0 {
		t.Errorf("expected empty timings, got %d", len(timings))
	}
}

func TestBuildStoryTimings_WithEvents(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", "s-001", map[string]any{
		"tier": 0, "role": "junior",
	}))
	es.Append(state.NewEvent(state.EventStoryMerged, "merger", "s-001", map[string]any{
		"pr_url": "https://github.com/pr/1",
	}))

	stories := []state.Story{{ID: "s-001"}}
	timings := buildStoryTimings(es, stories)

	if len(timings) != 1 {
		t.Fatalf("expected 1 timing, got %d", len(timings))
	}
	st := timings["s-001"]
	if st.startedAt.IsZero() {
		t.Error("expected non-zero started time")
	}
	if st.mergedAt.IsZero() {
		t.Error("expected non-zero merged time")
	}
}

func TestBuildStoryTimings_FallbackToPRCreated(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	es.Append(state.NewEvent(state.EventStoryStarted, "agent-1", "s-001", map[string]any{
		"tier": 0, "role": "junior",
	}))
	// No merge event, but PR was created
	es.Append(state.NewEvent(state.EventStoryPRCreated, "merger", "s-001", map[string]any{
		"pr_number": 5, "pr_url": "https://github.com/pr/5",
	}))

	stories := []state.Story{{ID: "s-001"}}
	timings := buildStoryTimings(es, stories)

	st := timings["s-001"]
	if st.mergedAt.IsZero() {
		t.Error("expected mergedAt to use PR creation time as fallback")
	}
}
