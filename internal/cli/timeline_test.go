package cli

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestTimelineCmd_RendersRequirementHistory(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-tl", "Timeline demo", "")
	seedTestStory(t, env, "story-a", "req-tl", "First story", 3)

	for _, evt := range []state.Event{
		state.NewEvent(state.EventStoryStarted, "agent-1", "story-a", nil),
		state.NewEvent(state.EventStoryMerged, "agent-1", "story-a", nil),
		state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": "req-tl"}),
	} {
		if err := env.Events.Append(evt); err != nil {
			t.Fatalf("append: %v", err)
		}
		_ = env.Proj.Project(evt)
	}

	out, err := execCmd(t, newTimelineCmd(), env.Config, "req-tl")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	for _, want := range []string{"Timeline demo", "started", "merged", "requirement completed", "Summary:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTimelineCmd_JSON(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-js", "JSON demo", "")
	seedTestStory(t, env, "story-j", "req-js", "Story", 1)

	out, err := execCmd(t, newTimelineCmd(), env.Config, "req-js", "--json")
	if err != nil {
		t.Fatalf("timeline --json: %v", err)
	}
	if !strings.Contains(out, `"ReqID": "req-js"`) {
		t.Errorf("JSON output missing ReqID:\n%s", out)
	}
}

func TestTimelineCmd_AutoSelectsSoleRequirement(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-solo", "Solo", "")

	out, err := execCmd(t, newTimelineCmd(), env.Config)
	if err != nil {
		t.Fatalf("timeline (auto-select): %v", err)
	}
	if !strings.Contains(out, "Solo") {
		t.Errorf("should auto-select the only requirement:\n%s", out)
	}
}

func TestTimelineCmd_ErrorsWhenAmbiguousOrMissing(t *testing.T) {
	env := setupTestEnv(t)
	if _, err := execCmd(t, newTimelineCmd(), env.Config); err == nil {
		t.Error("no requirements must be an error")
	}

	seedTestReq(t, env, "r1", "One", "")
	seedTestReq(t, env, "r2", "Two", "")
	if _, err := execCmd(t, newTimelineCmd(), env.Config); err == nil {
		t.Error("multiple requirements without an arg must be an error")
	}
	if _, err := execCmd(t, newTimelineCmd(), env.Config, "nope"); err == nil {
		t.Error("unknown requirement id must be an error")
	}
}
