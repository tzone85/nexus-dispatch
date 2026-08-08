package notify

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestRender_KnownEvents(t *testing.T) {
	cases := []struct {
		name      string
		evt       state.Event
		wantTitle string
		wantBody  []string
	}{
		{
			name:      "completed carries req id",
			evt:       state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": "req-42"}),
			wantTitle: "completed",
			wantBody:  []string{"req-42", "merged"},
		},
		{
			name:      "blocked points at fix gaps",
			evt:       state.NewEvent(state.EventReqBlocked, "monitor", "", map[string]any{"id": "req-42"}),
			wantTitle: "BLOCKED",
			wantBody:  []string{".nxd-fix-gaps.md", "req-42"},
		},
		{
			name:      "paused carries reason",
			evt:       state.NewEvent(state.EventReqPaused, "monitor", "", map[string]any{"id": "r1", "reason": "Ollama overload"}),
			wantTitle: "paused",
			wantBody:  []string{"Ollama overload", "r1"},
		},
		{
			name:      "security failure names the story",
			evt:       state.NewEvent(state.EventStorySecurityFailed, "gate", "story-7", map[string]any{"summary": "1 critical finding"}),
			wantTitle: "security",
			wantBody:  []string{"story-7", "1 critical finding"},
		},
		{
			name: "budget exceeded shows spend vs budget",
			evt: state.NewEvent(state.EventReqBudgetExceeded, "monitor", "", map[string]any{
				"id": "r2", "spent_usd": 12.5, "budget_usd": 10.0,
			}),
			wantTitle: "EXCEEDED",
			wantBody:  []string{"$12.50", "$10.00", "paused"},
		},
		{
			name: "budget warning shows spend vs budget",
			evt: state.NewEvent(state.EventReqBudgetWarning, "monitor", "", map[string]any{
				"id": "r2", "spent_usd": 8.0, "budget_usd": 10.0,
			}),
			wantTitle: "warning",
			wantBody:  []string{"$8.00", "$10.00"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title, body := Render(tc.evt)
			if !strings.Contains(title, tc.wantTitle) {
				t.Errorf("title %q should contain %q", title, tc.wantTitle)
			}
			for _, w := range tc.wantBody {
				if !strings.Contains(body, w) {
					t.Errorf("body %q should contain %q", body, w)
				}
			}
		})
	}
}

func TestRender_UnknownEventFallsBackToTypeName(t *testing.T) {
	title, _ := Render(state.NewEvent(state.EventType("SOME_NEW_EVENT"), "", "", nil))
	if !strings.Contains(title, "SOME_NEW_EVENT") {
		t.Errorf("unknown events must surface their type, got %q", title)
	}
}
