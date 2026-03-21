package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestCompressRanges(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		expected [][2]int
	}{
		{"empty", nil, nil},
		{"single", []int{5}, [][2]int{{5, 5}}},
		{"consecutive", []int{1, 2, 3}, [][2]int{{1, 3}}},
		{"gap", []int{1, 2, 3, 5}, [][2]int{{1, 3}, {5, 5}}},
		{"mixed", []int{1, 2, 3, 5, 7, 8}, [][2]int{{1, 3}, {5, 5}, {7, 8}}},
		{"all separate", []int{1, 3, 5}, [][2]int{{1, 1}, {3, 3}, {5, 5}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressRanges(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d ranges, got %d: %v", len(tt.expected), len(got), got)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Fatalf("range %d: expected %v, got %v", i, tt.expected[i], got[i])
				}
			}
		})
	}
}

func TestFormatPRNumbers(t *testing.T) {
	tests := []struct {
		name     string
		nums     []int
		expected string
	}{
		{"single", []int{1}, "(PR #1)"},
		{"two consecutive", []int{1, 2}, "(PRs #1\u20132)"},
		{"three consecutive", []int{1, 2, 3}, "(PRs #1\u20133)"},
		{"gap", []int{1, 3}, "(PRs #1, #3)"},
		{"mixed", []int{3, 12, 13, 14}, "(PRs #3, #12\u201314)"},
		{"unsorted", []int{5, 2, 3, 1}, "(PRs #1\u20133, #5)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPRNumbers(tt.nums)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestFormatWaveTime(t *testing.T) {
	base := time.Date(2026, 3, 18, 10, 38, 0, 0, time.Local)

	tests := []struct {
		name     string
		start    time.Time
		end      time.Time
		expected string
	}{
		{"zero start", time.Time{}, base, "\u2014"},
		{"same time", base, base, "10:38"},
		{"zero end", base, time.Time{}, "10:38"},
		{"under a minute apart", base, base.Add(30 * time.Second), "10:38"},
		{"range", base, base.Add(3 * time.Minute), "10:38 \u2013 10:41"},
		{"long range", base, base.Add(34 * time.Minute), "10:38 \u2013 11:12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatWaveTime(tt.start, tt.end)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestFormatWaveStories(t *testing.T) {
	stories := []StoryInfo{
		{Title: "Budget", PRNumber: 2},
		{Title: "Research", PRNumber: 3},
		{Title: "Crew", PRNumber: 4},
	}
	got := formatWaveStories(stories)
	expected := "Budget, Research, Crew (PRs #2\u20134)"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractPRNumberFromURL(t *testing.T) {
	tests := []struct {
		url      string
		expected int
	}{
		{"https://github.com/user/repo/pull/42", 42},
		{"https://github.com/user/repo/pull/42/", 42},
		{"", 0},
		{"not-a-url", 0},
	}

	for _, tt := range tests {
		got := extractPRNumberFromURL(tt.url)
		if got != tt.expected {
			t.Fatalf("URL %q: expected %d, got %d", tt.url, tt.expected, got)
		}
	}
}

func TestGenerateSummary_FullPipeline(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	defer ps.Close()

	reqID := "r-summary-test"
	now := time.Now()

	reqEvt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": reqID, "title": "Build game", "description": "Build a complete game",
	})
	es.Append(reqEvt)
	ps.Project(reqEvt)

	stories := []struct {
		id    string
		title string
		wave  int
		prNum int
	}{
		{"s-sum-1", "Foundation", 0, 1},
		{"s-sum-2", "Budget system", 1, 2},
		{"s-sum-3", "Research module", 1, 3},
	}

	for _, s := range stories {
		createEvt := state.NewEvent(state.EventStoryCreated, "planner", s.id, map[string]any{
			"id": s.id, "req_id": reqID, "title": s.title,
			"description": "desc", "complexity": 3,
		})
		es.Append(createEvt)
		ps.Project(createEvt)

		assignEvt := state.NewEvent(state.EventStoryAssigned, "agent-1", s.id, map[string]any{
			"agent_id": "agent-1", "wave": s.wave,
		})
		es.Append(assignEvt)
		ps.Project(assignEvt)

		startEvt := state.NewEvent(state.EventStoryStarted, "agent-1", s.id, nil)
		startEvt.Timestamp = now.Add(time.Duration(s.wave*10) * time.Minute)
		es.Append(startEvt)
		ps.Project(startEvt)

		prEvt := state.NewEvent(state.EventStoryPRCreated, "merger", s.id, map[string]any{
			"pr_number": s.prNum,
			"pr_url":    "https://github.com/test/repo/pull/" + strings.Repeat("0", 0),
		})
		es.Append(prEvt)
		ps.Project(prEvt)

		mergeEvt := state.NewEvent(state.EventStoryMerged, "merger", s.id, nil)
		mergeEvt.Timestamp = now.Add(time.Duration(s.wave*10+3) * time.Minute)
		es.Append(mergeEvt)
		ps.Project(mergeEvt)
	}

	summary, err := GenerateSummary(es, ps, reqID)
	if err != nil {
		t.Fatalf("generate summary: %v", err)
	}

	if !strings.Contains(summary, "3 PRs created and merged") {
		t.Errorf("expected '3 PRs created and merged' in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Wave 0") {
		t.Errorf("expected 'Wave 0' in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Wave 1") {
		t.Errorf("expected 'Wave 1' in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Foundation") {
		t.Errorf("expected 'Foundation' in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "3/3 merged") {
		t.Errorf("expected '3/3 merged' in summary, got:\n%s", summary)
	}

	t.Logf("Generated summary:\n%s", summary)
}
