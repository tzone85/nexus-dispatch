package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// fakeTmux records session lookups and kills.
type fakeTmux struct {
	alive   map[string]bool
	killed  []string
	killErr error
}

func (f *fakeTmux) SessionExists(name string) bool { return f.alive[name] }
func (f *fakeTmux) KillSession(name string) error {
	f.killed = append(f.killed, name)
	return f.killErr
}

func useFakeTmux(t *testing.T, f *fakeTmux) {
	t.Helper()
	prev := cancelSessions
	cancelSessions = f
	t.Cleanup(func() { cancelSessions = prev })
}

// setStoryStatus drives a story into the given status via real events.
func setStoryStatus(t *testing.T, env *testEnv, storyID, status string) {
	t.Helper()
	var evt state.Event
	switch status {
	case "assigned":
		evt = state.NewEvent(state.EventStoryAssigned, "dispatcher", storyID, map[string]any{"agent_id": "agent-1", "wave": 1})
	case "in_progress":
		evt = state.NewEvent(state.EventStoryStarted, "agent-1", storyID, nil)
	case "review":
		evt = state.NewEvent(state.EventStoryCompleted, "agent-1", storyID, nil)
	case "merged":
		evt = state.NewEvent(state.EventStoryMerged, "merger", storyID, nil)
	default:
		t.Fatalf("unsupported status %s", status)
	}
	if err := env.Events.Append(evt); err != nil {
		t.Fatal(err)
	}
	if err := env.Proj.Project(evt); err != nil {
		t.Fatal(err)
	}
}

func spawnAgent(t *testing.T, env *testEnv, agentID, storyID, session string) {
	t.Helper()
	evt := state.NewEvent(state.EventAgentSpawned, agentID, storyID, map[string]any{"role": "junior", "session_name": session})
	env.Events.Append(evt)
	if err := env.Proj.Project(evt); err != nil {
		t.Fatal(err)
	}
}

func eventTypes(t *testing.T, env *testEnv, storyID string) []state.EventType {
	t.Helper()
	events, err := env.Events.List(state.EventFilter{StoryID: storyID})
	if err != nil {
		t.Fatal(err)
	}
	var out []state.EventType
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func TestCancel_Story_ResetsTerminatesAndKillsSession(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "Story", 2)
	setStoryStatus(t, env, "s1", "assigned")
	setStoryStatus(t, env, "s1", "in_progress")
	spawnAgent(t, env, "agent-1", "s1", "nxd-custom-s1")
	ft := &fakeTmux{alive: map[string]bool{"nxd-custom-s1": true}}
	useFakeTmux(t, ft)

	out, err := execCmd(t, newCancelCmd(), env.Config, "s1", "--reason", "wrong approach")
	if err != nil {
		t.Fatalf("cancel: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Cancelled story s1 (in_progress → draft): cancelled by operator: wrong approach") {
		t.Errorf("output:\n%s", out)
	}
	if len(ft.killed) != 1 || ft.killed[0] != "nxd-custom-s1" {
		t.Errorf("killed = %v, want the agent's recorded session", ft.killed)
	}

	story, _ := env.Proj.GetStory("s1")
	if story.Status != "draft" {
		t.Errorf("story status = %s, want draft", story.Status)
	}
	agents, _ := env.Proj.ListAgents(state.AgentFilter{Status: "terminated"})
	if len(agents) != 1 || agents[0].ID != "agent-1" {
		t.Errorf("agent should be terminated, got %+v", agents)
	}
	types := eventTypes(t, env, "s1")
	joined := ""
	for _, ty := range types {
		joined += string(ty) + " "
	}
	if !strings.Contains(joined, "STORY_RESET AGENT_TERMINATED") {
		t.Errorf("events = %v, want STORY_RESET then AGENT_TERMINATED", types)
	}
	events, _ := env.Events.List(state.EventFilter{Type: state.EventStoryReset, StoryID: "s1"})
	if p := state.DecodePayload(events[0].Payload); p["reason"] != "cancelled by operator: wrong approach" || p["previous_status"] != "in_progress" {
		t.Errorf("STORY_RESET payload = %v", p)
	}
}

func TestCancel_Story_NoSessionAliveUsesConvention(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "Story", 2)
	setStoryStatus(t, env, "s1", "assigned")
	ft := &fakeTmux{alive: map[string]bool{}}
	useFakeTmux(t, ft)

	out, err := execCmd(t, newCancelCmd(), env.Config, "s1")
	if err != nil {
		t.Fatalf("cancel: %v\n%s", err, out)
	}
	if len(ft.killed) != 0 {
		t.Errorf("no session alive → nothing to kill, got %v", ft.killed)
	}
	if strings.Contains(out, "killed tmux session") {
		t.Errorf("should not report a kill:\n%s", out)
	}
	if !strings.Contains(out, "Cancelled story s1 (assigned → draft): cancelled by operator\n") {
		t.Errorf("default reason expected:\n%s", out)
	}
}

func TestCancel_Story_KillErrorIsWarningNotFailure(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "Story", 2)
	setStoryStatus(t, env, "s1", "assigned")
	ft := &fakeTmux{alive: map[string]bool{"nxd-s1": true}, killErr: errors.New("tmux gone")}
	useFakeTmux(t, ft)

	out, err := execCmd(t, newCancelCmd(), env.Config, "s1")
	if err != nil {
		t.Fatalf("kill failure must not abort the cancel: %v", err)
	}
	if !strings.Contains(out, "warning: could not kill tmux session nxd-s1: tmux gone") {
		t.Errorf("output:\n%s", out)
	}
	story, _ := env.Proj.GetStory("s1")
	if story.Status != "draft" {
		t.Errorf("status = %s", story.Status)
	}
}

func TestCancel_Story_InactiveRefused(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "Story", 2) // draft
	useFakeTmux(t, &fakeTmux{})

	_, err := execCmd(t, newCancelCmd(), env.Config, "s1")
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected refusal for a draft story, got %v", err)
	}
	if n, _ := env.Events.Count(state.EventFilter{Type: state.EventStoryReset}); n != 0 {
		t.Error("no events must be emitted when refused")
	}
}

func TestCancel_Requirement_CancelsActiveStoriesAndPauses(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "A", 2)
	seedTestStory(t, env, "s2", "r1", "B", 2)
	seedTestStory(t, env, "s3", "r1", "C", 2)
	setStoryStatus(t, env, "s1", "assigned")
	setStoryStatus(t, env, "s1", "in_progress")
	setStoryStatus(t, env, "s2", "review")
	setStoryStatus(t, env, "s3", "merged")
	ft := &fakeTmux{alive: map[string]bool{"nxd-s1": true}}
	useFakeTmux(t, ft)

	out, err := execCmd(t, newCancelCmd(), env.Config, "r1", "--reason", "budget")
	if err != nil {
		t.Fatalf("cancel: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Cancelled requirement r1 (2 active stories reset); status → paused") {
		t.Errorf("output:\n%s", out)
	}
	req, _ := env.Proj.GetRequirement("r1")
	if req.Status != "paused" {
		t.Errorf("req status = %s, want paused", req.Status)
	}
	for id, want := range map[string]string{"s1": "draft", "s2": "draft", "s3": "merged"} {
		st, _ := env.Proj.GetStory(id)
		if st.Status != want {
			t.Errorf("%s status = %s, want %s", id, st.Status, want)
		}
	}
	if len(ft.killed) != 1 || ft.killed[0] != "nxd-s1" {
		t.Errorf("killed = %v", ft.killed)
	}
	paused, _ := env.Events.List(state.EventFilter{Type: state.EventReqPaused})
	if len(paused) != 1 {
		t.Fatalf("REQ_PAUSED count = %d", len(paused))
	}
	p := state.DecodePayload(paused[0].Payload)
	if p["id"] != "r1" || p["reason"] != "cancelled by operator: budget" || p["stories_cancelled"] != float64(2) {
		t.Errorf("REQ_PAUSED payload = %v", p)
	}
	if n, _ := env.Events.Count(state.EventFilter{Type: state.EventReqCompleted}); n != 0 {
		t.Error("cancel must not invent a new terminal event")
	}
}

func TestCancel_Requirement_NoActiveStoriesStillPauses(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	seedTestStory(t, env, "s1", "r1", "A", 2)
	useFakeTmux(t, &fakeTmux{})

	out, err := execCmd(t, newCancelCmd(), env.Config, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(0 active stories reset)") {
		t.Errorf("output:\n%s", out)
	}
	req, _ := env.Proj.GetRequirement("r1")
	if req.Status != "paused" {
		t.Errorf("status = %s", req.Status)
	}
}

func TestCancel_Requirement_CompletedRefused(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r1", "Req", "/repo")
	done := state.NewEvent(state.EventReqCompleted, "system", "", map[string]any{"id": "r1"})
	env.Events.Append(done)
	env.Proj.Project(done)
	useFakeTmux(t, &fakeTmux{})

	_, err := execCmd(t, newCancelCmd(), env.Config, "r1")
	if err == nil || !strings.Contains(err.Error(), "completed") {
		t.Fatalf("expected refusal, got %v", err)
	}
}

func TestCancel_UnknownID(t *testing.T) {
	env := setupTestEnv(t)
	useFakeTmux(t, &fakeTmux{})
	_, err := execCmd(t, newCancelCmd(), env.Config, "nope")
	if err == nil || !strings.Contains(err.Error(), `no requirement or story with id "nope"`) {
		t.Fatalf("got %v", err)
	}
}

func TestCancel_BadConfig(t *testing.T) {
	if _, err := execCmd(t, newCancelCmd(), "/nonexistent/nxd.yaml", "x"); err == nil {
		t.Fatal("expected config error")
	}
}

func TestCancel_RequiresExactlyOneArg(t *testing.T) {
	if _, err := execCmd(t, newCancelCmd(), "cfg"); err == nil {
		t.Fatal("expected arg error")
	}
}

func TestFullReason(t *testing.T) {
	if got := fullReason("  "); got != "cancelled by operator" {
		t.Errorf("blank → %q", got)
	}
	if got := fullReason(" x "); got != "cancelled by operator: x" {
		t.Errorf("x → %q", got)
	}
}

func TestRealTmux_SatisfiesInterface(t *testing.T) {
	var _ sessionKiller = realTmux{}
	// SessionExists on a name that cannot exist must simply be false (no tmux
	// server in tests) and must not panic.
	rt := realTmux{}
	if rt.SessionExists("nxd-definitely-not-a-session-xyz") {
		t.Error("unexpected live session")
	}
}
