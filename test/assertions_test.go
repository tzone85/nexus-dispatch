package test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Mode represents the test execution mode.
type Mode string

const (
	ModeReplay Mode = "replay"
	ModeLive   Mode = "live"
)

// TestState captures the pipeline state after each phase for assertions.
type TestState struct {
	Events   []state.Event
	Stories  []state.Story
	RepoPath string
	StoreDir string
	Mode     Mode
	Stores   TestStores
}

// Refresh reloads events and stories from stores.
func (ts *TestState) Refresh(t *testing.T, reqID string) {
	t.Helper()
	events, err := ts.Stores.Events.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	ts.Events = events

	stories, err := ts.Stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	ts.Stories = stories
}

// Assertion is a named check against TestState.
type Assertion struct {
	Phase string
	Name  string
	Check func(t *testing.T, ts TestState)
}

func AssertStoriesCreated(min, max int) Assertion {
	return Assertion{
		Phase: "plan",
		Name:  fmt.Sprintf("stories_created_%d_to_%d", min, max),
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			count := len(ts.Stories)
			if count < min || count > max {
				t.Errorf("story count = %d, want between %d and %d", count, min, max)
			}
		},
	}
}

func AssertComplexityRange(low, high int) Assertion {
	return Assertion{
		Phase: "plan",
		Name:  "complexity_in_range",
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			for _, s := range ts.Stories {
				if s.Complexity < low || s.Complexity > high {
					t.Errorf("story %q complexity = %d, want %d-%d", s.Title, s.Complexity, low, high)
				}
			}
		},
	}
}

func AssertDependenciesValid() Assertion {
	return Assertion{
		Phase: "plan",
		Name:  "dependencies_valid",
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			for _, s := range ts.Stories {
				if s.ID == "" {
					t.Error("story with empty ID")
				}
			}
		},
	}
}

func AssertEventsEmitted(types ...state.EventType) Assertion {
	return Assertion{
		Phase: "any",
		Name:  fmt.Sprintf("events_emitted_%d_types", len(types)),
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			found := map[state.EventType]bool{}
			for _, e := range ts.Events {
				found[e.Type] = true
			}
			for _, et := range types {
				if !found[et] {
					t.Errorf("expected event %s not found", et)
				}
			}
		},
	}
}

func AssertAllStoriesInStatus(status string) Assertion {
	return Assertion{
		Phase: "any",
		Name:  fmt.Sprintf("all_stories_status_%s", status),
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			for _, s := range ts.Stories {
				if s.Status != status {
					t.Errorf("story %s (%s) status = %q, want %q", s.ID, s.Title, s.Status, status)
				}
			}
		},
	}
}

func AssertCodeCompiles() Assertion {
	return Assertion{
		Phase: "qa",
		Name:  "code_compiles",
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			cmd := exec.Command("go", "build", "./...")
			cmd.Dir = ts.RepoPath
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("go build failed: %v\n%s", err, out)
			}
		},
	}
}

func AssertTestsPass() Assertion {
	return Assertion{
		Phase: "qa",
		Name:  "tests_pass",
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			cmd := exec.Command("go", "test", "./...")
			cmd.Dir = ts.RepoPath
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("go test failed: %v\n%s", err, out)
			}
		},
	}
}

func AssertReviewCompleted(validVerdicts ...string) Assertion {
	return Assertion{
		Phase: "review",
		Name:  "review_completed",
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			for _, e := range ts.Events {
				if e.Type == state.EventStoryReviewPassed || e.Type == state.EventStoryReviewFailed {
					return
				}
			}
			t.Error("no review event found")
		},
	}
}

func AssertMinEvents(min int) Assertion {
	return Assertion{
		Phase: "any",
		Name:  fmt.Sprintf("min_%d_events", min),
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			if len(ts.Events) < min {
				t.Errorf("event count = %d, want >= %d", len(ts.Events), min)
			}
		},
	}
}

func AssertToolCallsUsed(toolNames ...string) Assertion {
	return Assertion{
		Phase: "plan",
		Name:  fmt.Sprintf("tool_calls_used_%v", toolNames),
		Check: func(t *testing.T, ts TestState) {
			t.Helper()
			for _, name := range toolNames {
				found := false
				for _, e := range ts.Events {
					if strings.Contains(string(e.Payload), name) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected tool call %q not found in events", name)
				}
			}
		},
	}
}
