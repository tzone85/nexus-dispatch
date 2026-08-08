package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func tlEvent(t time.Time, typ state.EventType, storyID string, data map[string]any) state.Event {
	evt := state.NewEvent(typ, "tester", storyID, data)
	evt.Timestamp = t
	return evt
}

func TestBuildTimeline(t *testing.T) {
	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	req := state.Requirement{ID: "req-1", Title: "Build the thing", Status: "completed"}
	stories := []state.Story{
		{ID: "s1", ReqID: "req-1", Title: "Story one", Status: "merged", Wave: 1},
		{ID: "s2", ReqID: "req-1", Title: "Story two", Status: "in_progress", Wave: 2},
	}
	events := []state.Event{
		tlEvent(base, state.EventReqSubmitted, "", map[string]any{"id": "req-1"}),
		tlEvent(base.Add(30*time.Second), state.EventReqPlanned, "", map[string]any{"id": "req-1", "story_count": float64(2)}),
		tlEvent(base.Add(time.Minute), state.EventStoryStarted, "s1", nil),
		tlEvent(base.Add(2*time.Minute), state.EventStoryProgress, "s1", nil), // noise — must be skipped
		tlEvent(base.Add(4*time.Minute), state.EventStoryMerged, "s1", nil),
		tlEvent(base.Add(5*time.Minute), state.EventReqCompleted, "", map[string]any{"id": "req-1"}),
		// Unrelated requirement/story — must be excluded entirely.
		tlEvent(base.Add(6*time.Minute), state.EventReqCompleted, "", map[string]any{"id": "req-other"}),
		tlEvent(base.Add(6*time.Minute), state.EventStoryMerged, "sX", nil),
	}

	tl := BuildTimeline(req, stories, events)

	if tl.ReqID != "req-1" || tl.Total != 2 || tl.Merged != 1 {
		t.Fatalf("header wrong: %+v", tl)
	}
	if len(tl.Entries) != 5 {
		for _, e := range tl.Entries {
			t.Logf("entry: %s %s", e.Time, e.Label)
		}
		t.Fatalf("want 5 entries (submitted, planned, started, merged, completed — noise and foreign events excluded), got %d", len(tl.Entries))
	}
	if !tl.Start.Equal(base) || !tl.End.Equal(base.Add(5*time.Minute)) {
		t.Errorf("span wrong: start=%v end=%v", tl.Start, tl.End)
	}

	var s1 TimelineStory
	for _, s := range tl.Stories {
		if s.ID == "s1" {
			s1 = s
		}
	}
	if s1.Duration != 3*time.Minute {
		t.Errorf("s1 duration: want 3m (started +1m, merged +4m), got %v", s1.Duration)
	}
}

func TestTimelineLabels(t *testing.T) {
	stories := map[string]state.Story{"s1": {ID: "s1", Title: "Tokens"}}

	cases := []struct {
		evt  state.Event
		want string
	}{
		{tlEvent(time.Now(), state.EventStoryReviewFailed, "s1", nil), "review FAILED"},
		{tlEvent(time.Now(), state.EventReqPaused, "", map[string]any{"id": "r", "reason": "overload"}), "overload"},
		{tlEvent(time.Now(), state.EventReqBudgetExceeded, "", map[string]any{"id": "r", "spent_usd": 5.0, "budget_usd": 4.0}), "$5.00"},
		{tlEvent(time.Now(), state.EventType("FUTURE_EVENT"), "", nil), "FUTURE_EVENT"},
	}
	for _, tc := range cases {
		if got := timelineLabel(tc.evt, stories); !strings.Contains(got, tc.want) {
			t.Errorf("label %q should contain %q", got, tc.want)
		}
	}
	// Story titles make labels self-explanatory.
	if got := timelineLabel(tlEvent(time.Now(), state.EventStoryMerged, "s1", nil), stories); !strings.Contains(got, "Tokens") {
		t.Errorf("label should carry the story title, got %q", got)
	}
}
