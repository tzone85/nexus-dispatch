package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// escalationFixture drives dispatchNextWave's tier interception: a
// requirement with one story escalated to the given tier, a manager and/or
// planner backed by a replay LLM, and auto-resume wired with an empty
// runtime registry (so nothing can actually spawn).
type escalationFixture struct {
	t        *testing.T
	es       state.EventStore
	ps       state.ProjectionStore
	cfg      config.Config
	stateDir string
	req      string
	story    string
	m        *Monitor
	rc       *RunContext
}

func newEscalationFixture(t *testing.T, tier int) *escalationFixture {
	t.Helper()
	es, ps := pipelineStores(t)
	const req, story = "REQ-ESC", "s-esc"
	seedCapacityStory(t, es, ps, req, story)
	for from := 0; from < tier; from++ {
		evt := state.NewEvent(state.EventStoryEscalated, "reviewer", story, map[string]any{
			"from_tier": from, "to_tier": from + 1, "reason": fmt.Sprintf("failed at tier %d", from),
		})
		if err := es.Append(evt); err != nil {
			t.Fatal(err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = t.TempDir()
	cfg.Planning.MaxStoryComplexity = 5
	if err := os.MkdirAll(filepath.Join(cfg.Workspace.StateDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	m.SetAutoResume(NewDispatcher(cfg, es, ps), NewExecutor(reg, cfg, es, ps, nil))

	dag := graph.New()
	dag.AddNode(story)
	dag.AddNode("s-dep")
	dag.AddEdge("s-dep", story)
	rc := &RunContext{ReqID: req, DAG: dag, PlannedStories: []PlannedStory{
		{ID: story, Title: "Task", Description: "d", Complexity: 3},
		{ID: "s-dep", Title: "Dependent", Complexity: 1, DependsOn: []string{story}},
	}}
	return &escalationFixture{t: t, es: es, ps: ps, cfg: cfg, stateDir: cfg.Workspace.StateDir, req: req, story: story, m: m, rc: rc}
}

func (f *escalationFixture) withManager(client llm.Client) *escalationFixture {
	f.m.SetManager(NewManager(client, "test", "test-model", 2000, f.es, f.ps))
	return f
}

func (f *escalationFixture) withPlanner(client llm.Client) *escalationFixture {
	f.m.SetPlanner(NewPlanner(client, f.cfg, f.es, f.ps))
	return f
}

func (f *escalationFixture) dispatch() []ActiveAgent {
	f.t.Helper()
	return f.m.dispatchNextWave(context.Background(), f.rc, f.t.TempDir())
}

func (f *escalationFixture) events(typ state.EventType, storyID string) []state.Event {
	f.t.Helper()
	evts, err := f.es.List(state.EventFilter{Type: typ, StoryID: storyID})
	if err != nil {
		f.t.Fatal(err)
	}
	return evts
}

func (f *escalationFixture) storyStatus(id string) string {
	f.t.Helper()
	st, err := f.ps.GetStory(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Status
}

func (f *escalationFixture) reqStatus() string {
	f.t.Helper()
	req, err := f.ps.GetRequirement(f.req)
	if err != nil {
		f.t.Fatal(err)
	}
	return req.Status
}

func (f *escalationFixture) lastPauseReason() string {
	f.t.Helper()
	pauses, err := f.es.List(state.EventFilter{Type: state.EventReqPaused})
	if err != nil || len(pauses) == 0 {
		f.t.Fatalf("expected a REQ_PAUSED event (err=%v)", err)
	}
	reason, _ := state.DecodePayload(pauses[len(pauses)-1].Payload)["reason"].(string)
	return reason
}

func (f *escalationFixture) resetReasons(storyID string) []string {
	f.t.Helper()
	var out []string
	for _, evt := range f.events(state.EventStoryReviewFailed, storyID) {
		if r, ok := state.DecodePayload(evt.Payload)["reason"].(string); ok {
			out = append(out, r)
		}
	}
	return out
}

func managerJSON(action string) llm.CompletionResponse {
	return llm.CompletionResponse{Content: action}
}

func TestDispatchNextWave_PausedRequirementDispatchesNothing(t *testing.T) {
	f := newEscalationFixture(t, 0)
	pause := state.NewEvent(state.EventReqPaused, "cli", "", map[string]any{"id": f.req, "reason": "operator"})
	if err := f.es.Append(pause); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.Project(pause); err != nil {
		t.Fatal(err)
	}
	if agents := f.dispatch(); len(agents) != 0 {
		t.Fatalf("paused requirement must not dispatch, got %d agents", len(agents))
	}
	if n := len(f.events(state.EventStoryAssigned, f.story)); n != 0 {
		t.Errorf("STORY_ASSIGNED emitted for a paused requirement: %d", n)
	}
	if f.rc.WaveNumber != 0 {
		t.Errorf("wave number advanced to %d on a paused requirement", f.rc.WaveNumber)
	}
}

func TestDispatchNextWave_SpawnErrorsAreDroppedFromActiveSet(t *testing.T) {
	f := newEscalationFixture(t, 0)
	agents := f.dispatch()
	// The empty registry cannot spawn "aider": the story is assigned but no
	// agent is tracked, and the dispatch stage still records success.
	if len(agents) != 0 {
		t.Fatalf("expected no active agents from a failed spawn, got %d", len(agents))
	}
	if n := len(f.events(state.EventStoryAssigned, f.story)); n != 1 {
		t.Errorf("STORY_ASSIGNED = %d, want 1", n)
	}
	if f.rc.WaveNumber != 1 {
		t.Errorf("wave number = %d, want 1", f.rc.WaveNumber)
	}
	var dispatchOutcome string
	for _, evt := range f.events(state.EventStageCompleted, "") {
		p := state.DecodePayload(evt.Payload)
		if p["stage"] == "dispatch" {
			dispatchOutcome, _ = p["outcome"].(string)
		}
	}
	if dispatchOutcome != "success" {
		t.Errorf("dispatch stage outcome = %q, want success", dispatchOutcome)
	}
}

func TestDispatchNextWave_ManagerEscalatesToTechLead(t *testing.T) {
	f := newEscalationFixture(t, 2).withManager(llm.NewReplayClient(managerJSON(
		`{"diagnosis": "structural problem", "category": "design", "action": "escalate_to_techlead"}`)))
	agents := f.dispatch()

	if len(agents) != 0 {
		t.Fatalf("intercepted story must not be dispatched, got %d agents", len(agents))
	}
	if n := len(f.events(state.EventStoryAssigned, f.story)); n != 0 {
		t.Errorf("tier-2 story was dispatched (%d STORY_ASSIGNED) instead of intercepted", n)
	}
	escalations := f.events(state.EventStoryEscalated, f.story)
	last := state.DecodePayload(escalations[len(escalations)-1].Payload)
	if last["from_tier"].(float64) != 2 || last["to_tier"].(float64) != 3 || last["reason"] != "manager escalated: structural problem" {
		t.Errorf("last escalation = %v", last)
	}
	if tier, _ := f.m.escalation.CurrentTier(f.story); tier != 3 {
		t.Errorf("current tier = %d, want 3", tier)
	}
	logBody, err := os.ReadFile(filepath.Join(f.stateDir, "logs", f.story+"-manager.log"))
	if err != nil || !strings.Contains(string(logBody), "Diagnosis: structural problem") || !strings.Contains(string(logBody), "Action: escalate_to_techlead") {
		t.Errorf("manager diagnosis log = %q (err=%v)", logBody, err)
	}
}

func TestDispatchNextWave_ManagerSplitCreatesChildren(t *testing.T) {
	f := newEscalationFixture(t, 2).withManager(llm.NewReplayClient(managerJSON(`{
		"diagnosis": "too big", "category": "scope", "action": "split",
		"split_config": {"children": [
			{"suffix": "a", "title": "Part A", "description": "first", "complexity": 2, "owned_files": ["a.go"]},
			{"suffix": "b", "title": "Part B", "description": "second", "complexity": 2, "owned_files": ["b.go"]}
		], "dependency_edges": [["s-esc-b", "s-esc-a"]]}}`)))
	f.dispatch()

	split := f.events(state.EventStorySplit, f.story)
	if len(split) != 1 {
		t.Fatalf("STORY_SPLIT = %d, want 1", len(split))
	}
	sp := state.DecodePayload(split[0].Payload)
	if ids, _ := sp["child_story_ids"].([]any); len(ids) != 2 || ids[0] != "s-esc-a" || ids[1] != "s-esc-b" {
		t.Errorf("child_story_ids = %v", sp["child_story_ids"])
	}
	if sp["reason"] != "too big" {
		t.Errorf("split reason = %v", sp["reason"])
	}
	if got := f.storyStatus(f.story); got != "split" {
		t.Errorf("parent status = %q, want split", got)
	}
	for _, id := range []string{"s-esc-a", "s-esc-b"} {
		created := f.events(state.EventStoryCreated, id)
		if len(created) != 1 || created[0].AgentID != "manager" {
			t.Fatalf("%s: STORY_CREATED = %d events (agent %q)", id, len(created), created[0].AgentID)
		}
		p := state.DecodePayload(created[0].Payload)
		if p["req_id"] != f.req || p["split_depth"].(float64) != 1 {
			t.Errorf("%s payload = %v", id, p)
		}
		if deps := f.rc.DAG.DependenciesOf("s-dep"); !containsString(deps, id) {
			t.Errorf("s-dep must depend on child %s, deps = %v", id, deps)
		}
	}
	// s-dep depended on the parent; now it depends on both children, and
	// b depends on a.
	// (The split parent stays in the DAG as a completed node.)
	ready := f.rc.DAG.ReadyNodes(map[string]bool{f.story: true})
	if strings.Join(ready, ",") != "s-esc-a" {
		t.Errorf("ready nodes after split = %v, want [s-esc-a]", ready)
	}
	if len(f.rc.PlannedStories) != 4 {
		t.Errorf("planned stories = %d, want parent+dep+2 children", len(f.rc.PlannedStories))
	}
}

func TestDispatchNextWave_ManagerSplitInvalidResets(t *testing.T) {
	f := newEscalationFixture(t, 2).withManager(llm.NewReplayClient(managerJSON(`{
		"diagnosis": "too big", "category": "scope", "action": "split",
		"split_config": {"children": [
			{"suffix": "a", "title": "Part A", "description": "first", "complexity": 9}
		]}}`)))
	f.dispatch()

	if n := len(f.events(state.EventStorySplit, f.story)); n != 0 {
		t.Fatalf("invalid split must not emit STORY_SPLIT, got %d", n)
	}
	reasons := f.resetReasons(f.story)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "invalid split: child complexity 9 exceeds max 5") {
		t.Errorf("reset reasons = %v", reasons)
	}
	if got := f.storyStatus(f.story); got != "draft" {
		t.Errorf("story status = %q, want draft", got)
	}
}

func TestDispatchNextWave_ManagerDiagnosisErrors(t *testing.T) {
	t.Run("unparseable action resets with the error", func(t *testing.T) {
		f := newEscalationFixture(t, 2).withManager(llm.NewReplayClient(managerJSON(`{"action": "dance"}`)))
		f.dispatch()
		reasons := f.resetReasons(f.story)
		if len(reasons) != 1 || !strings.Contains(reasons[0], "diagnosis error:") || !strings.Contains(reasons[0], `invalid manager action: "dance"`) {
			t.Errorf("reset reasons = %v", reasons)
		}
		if got := f.reqStatus(); got == "paused" {
			t.Error("a bad diagnosis must not pause the requirement")
		}
	})

	t.Run("capacity error pauses without burning the tier", func(t *testing.T) {
		capErr := fmt.Errorf("ollama API error (status 503): server busy, please try again")
		f := newEscalationFixture(t, 2).withManager(llm.NewErrorClient(capErr))
		f.dispatch()
		if got := f.reqStatus(); got != "paused" {
			t.Fatalf("requirement status = %q, want paused", got)
		}
		if reason := f.lastPauseReason(); !strings.Contains(reason, "Ollama capacity/overload during manager-diagnosis") {
			t.Errorf("pause reason = %q", reason)
		}
		if reasons := f.resetReasons(f.story); len(reasons) != 0 {
			t.Errorf("capacity pause must not reset the story, got %v", reasons)
		}
		if tier, _ := f.m.escalation.CurrentTier(f.story); tier != 2 {
			t.Errorf("tier changed to %d during a capacity pause", tier)
		}
	})

	t.Run("fatal API error pauses", func(t *testing.T) {
		fatal := &llm.APIError{Provider: "anthropic", StatusCode: 403, Message: "forbidden"}
		f := newEscalationFixture(t, 2).withManager(llm.NewErrorClient(fatal))
		f.dispatch()
		if reason := f.lastPauseReason(); !strings.Contains(reason, "fatal API error in manager") {
			t.Errorf("pause reason = %q", reason)
		}
	})
}

func TestDispatchNextWave_ManagerSkipsStoriesAwaitingMerge(t *testing.T) {
	f := newEscalationFixture(t, 2).withManager(llm.NewErrorClient(errors.New("manager must not be called")))
	// The tier-2 story already passed QA: it is awaiting merge, so the
	// manager has nothing to diagnose and its dependent stays blocked.
	qa := state.NewEvent(state.EventStoryQAPassed, "qa", f.story, nil)
	if err := f.es.Append(qa); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.Project(qa); err != nil {
		t.Fatal(err)
	}
	// A second ready story that is not in the planned list exercises the
	// "not found in planned stories" branch without dispatching it.
	f.rc.DAG.AddNode("s-unplanned")
	f.dispatch()

	if n := len(f.events(state.EventReqPendingReview, "")); n != 1 {
		t.Errorf("REQ_PENDING_REVIEW = %d, want 1 (only awaiting merge)", n)
	}
	if reasons := f.resetReasons(f.story); len(reasons) != 0 {
		t.Errorf("awaiting-merge story was reset: %v", reasons)
	}
}

func replanJSON(stories string) llm.CompletionResponse {
	return llm.CompletionResponse{Content: stories}
}

func TestDispatchNextWave_TechLeadReplanSplitsStory(t *testing.T) {
	client := llm.NewReplayClient(replanJSON(`[
		{"id": "s-esc-a", "title": "Step A", "description": "first half", "acceptance_criteria": "A works", "complexity": 2, "owned_files": ["a.go"]},
		{"id": "other-b", "title": "Step B", "description": "second half", "acceptance_criteria": ["B works"], "complexity": 2, "owned_files": ["b.go"]}
	]`))
	f := newEscalationFixture(t, 3).withManager(llm.NewErrorClient(errors.New("tier 3 bypasses the manager"))).withPlanner(client)
	logDir := filepath.Join(f.stateDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, f.story+".log"), []byte("panic: nil map write\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.dispatch()

	// The re-plan prompt carried the event history and the agent log.
	if client.CallCount() != 1 {
		t.Fatalf("planner calls = %d, want 1", client.CallCount())
	}
	prompt := client.CallAt(0).Messages[0].Content
	if !strings.Contains(prompt, "STORY_ESCALATED") || !strings.Contains(prompt, "Agent log:\npanic: nil map write") {
		t.Errorf("re-plan prompt lacks failure context:\n%s", prompt)
	}

	split := f.events(state.EventStorySplit, f.story)
	if len(split) != 1 || split[0].AgentID != "tech_lead" {
		t.Fatalf("STORY_SPLIT = %+v", split)
	}
	sp := state.DecodePayload(split[0].Payload)
	if ids, _ := sp["child_story_ids"].([]any); len(ids) != 2 || ids[0] != "s-esc-a" || ids[1] != "other-b" {
		t.Errorf("child_story_ids = %v", sp["child_story_ids"])
	}
	if sp["reason"] != "tech lead re-plan" {
		t.Errorf("split reason = %v", sp["reason"])
	}
	for _, id := range []string{"s-esc-a", "other-b"} {
		created := f.events(state.EventStoryCreated, id)
		if len(created) != 1 || created[0].AgentID != "tech_lead" {
			t.Fatalf("%s: STORY_CREATED = %d (agent %q)", id, len(created), created[0].AgentID)
		}
		if p := state.DecodePayload(created[0].Payload); p["split_depth"].(float64) != 1 || p["req_id"] != f.req {
			t.Errorf("%s payload = %v", id, p)
		}
	}
	// Array-valued acceptance criteria is flattened onto the child story.
	if st, err := f.ps.GetStory("other-b"); err != nil || !strings.Contains(st.AcceptanceCriteria, "B works") {
		t.Errorf("other-b acceptance criteria = %q (err=%v)", st.AcceptanceCriteria, err)
	}
	// Re-planned children run sequentially: only the first is ready.
	if ready := f.rc.DAG.ReadyNodes(map[string]bool{f.story: true}); strings.Join(ready, ",") != "s-esc-a" {
		t.Errorf("ready nodes = %v, want [s-esc-a]", ready)
	}
	if got := f.storyStatus(f.story); got != "split" {
		t.Errorf("parent status = %q, want split", got)
	}
	if got := f.reqStatus(); got == "paused" {
		t.Error("a successful re-plan must not pause")
	}
}

func TestDispatchNextWave_TechLeadReplanFailures(t *testing.T) {
	cases := []struct {
		name   string
		client llm.Client
		want   string
	}{
		{"llm error", llm.NewErrorClient(errors.New("boom")), "tech lead re-plan failed: replan LLM call: boom"},
		{"capacity error", llm.NewErrorClient(fmt.Errorf("ollama API error (status 503): server busy, please try again")), "Ollama capacity/overload during tech-lead-replan"},
		{"unparseable", llm.NewReplayClient(replanJSON("no json here")), "parse replan stories"},
		{"all filtered", llm.NewReplayClient(replanJSON(`[{"id": "s-esc-a", "title": "empty"}]`)), "replan produced no valid sub-stories"},
		{"invalid split", llm.NewReplayClient(replanJSON(`[{"id": "s-esc-a", "title": "huge", "description": "x", "complexity": 9}]`)), "tech lead split invalid: child complexity 9 exceeds max 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEscalationFixture(t, 3).withManager(llm.NewErrorClient(errors.New("unused"))).withPlanner(tc.client)
			f.dispatch()
			if got := f.reqStatus(); got != "paused" {
				t.Fatalf("requirement status = %q, want paused", got)
			}
			if reason := f.lastPauseReason(); !strings.Contains(reason, tc.want) {
				t.Errorf("pause reason = %q, want substring %q", reason, tc.want)
			}
			if n := len(f.events(state.EventStorySplit, f.story)); n != 0 {
				t.Errorf("STORY_SPLIT emitted on failure: %d", n)
			}
			if got := f.storyStatus(f.story); got == "split" {
				t.Error("story must not be marked split on a failed re-plan")
			}
		})
	}
}

func TestDispatchNextWave_TechLeadWithoutPlannerPauses(t *testing.T) {
	f := newEscalationFixture(t, 3).withManager(llm.NewErrorClient(errors.New("unused")))
	f.dispatch()
	if reason := f.lastPauseReason(); !strings.Contains(reason, "tech lead escalation: no planner configured") {
		t.Errorf("pause reason = %q", reason)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
