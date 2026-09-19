package state

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestProject_AllEventTypes_NoErrors drives one event of every
// supported EventType through SQLiteStore.Project. Each case in the
// switch statement either updates the projection or returns nil for
// unhandled types — both are valid. The point is to ensure no case
// panics, returns spurious errors, or leaves the DB in an
// inconsistent state.
//
// Without this batch, Project's per-function coverage stayed at
// ~46% because tests only seeded a handful of event types directly.
func TestProject_AllEventTypes_NoErrors(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	// Order matters: requirements must be projected before their
	// stories. seedReq/seedStory both project through Project()
	// itself so we exercise those cases too.
	feed := []struct {
		typ     EventType
		storyID string
		payload map[string]any
	}{
		{EventReqSubmitted, "", map[string]any{"id": "R", "title": "t", "description": "d", "repo_path": "/tmp"}},
		{EventReqAnalyzed, "", map[string]any{"id": "R"}},
		{EventReqPlanned, "", map[string]any{"id": "R"}},
		{EventReqPaused, "", map[string]any{"id": "R"}},
		{EventReqResumed, "", map[string]any{"id": "R"}},
		{EventReqClassified, "", map[string]any{"id": "R", "type": "feature", "confidence": 0.9}},
		{EventInvestigationCompleted, "", map[string]any{"id": "R"}},
		{EventReqPendingReview, "", map[string]any{"id": "R"}},
		{EventStoryCreated, "S1", map[string]any{"id": "S1", "req_id": "R", "title": "story", "description": "d", "complexity": 3}},
		{EventStoryEstimated, "S1", map[string]any{"id": "S1"}},
		{EventStoryAssigned, "S1", map[string]any{"id": "S1", "role": "junior", "branch": "story/S1", "agent_id": "agent-1"}},
		{EventStoryStarted, "S1", map[string]any{"id": "S1"}},
		{EventStoryProgress, "S1", map[string]any{"iteration": 1, "phase": "read", "detail": "scanning"}},
		{EventStoryReviewRequested, "S1", nil},
		{EventStoryReviewPassed, "S1", nil},
		{EventStoryReviewFailed, "S1", map[string]any{"reason": "rejected"}},
		{EventStoryQAStarted, "S1", nil},
		{EventStoryQAPassed, "S1", nil},
		{EventStoryQAFailed, "S1", map[string]any{"reason": "qa fail"}},
		{EventStoryPRCreated, "S1", map[string]any{"pr_number": 42, "pr_url": "https://example/42"}},
		{EventStoryMergeReady, "S1", nil},
		{EventStoryMerged, "S1", nil},
		{EventStoryRecovery, "S1", map[string]any{"type": "worktree_pruned", "description": "wt removed"}},
		{EventStoryEscalated, "S1", map[string]any{"from_tier": 0, "to_tier": 1, "reason": "stuck"}},
		{EventStoryRewritten, "S1", map[string]any{"changes": map[string]any{"title": "Updated"}}},
		{EventStoryReset, "S1", map[string]any{"reason": "ops reset"}},
		{EventStoryCompleted, "S1", map[string]any{"iterations": 1}},
		{EventReqCompleted, "", map[string]any{"id": "R"}},
		// Story under a 2nd req for the rejected/split paths.
		{EventReqSubmitted, "", map[string]any{"id": "R2", "title": "t2", "description": "d", "repo_path": "/tmp"}},
		{EventReqRejected, "", map[string]any{"id": "R2", "reason": "operator declined"}},
		{EventStoryCreated, "S2", map[string]any{"id": "S2", "req_id": "R", "title": "split-parent", "complexity": 5}},
		{EventStorySplit, "S2", map[string]any{"child_story_ids": []string{"S2-a"}}},
		// Reaper events: explicit no-op cases.
		{EventBranchDeleted, "S1", map[string]any{"branch": "nxd/S1", "reason": "gc_retention_expired"}},
		{EventGCCompleted, "", map[string]any{"branches_deleted": 1, "repo_path": "/tmp/repo"}},
		{EventWorktreePruned, "S1", map[string]any{"worktree_path": "/tmp/wt"}},
		// Unknown event type: projectLocked returns errUnprojected, which Project ignores.
		{EventType("UNKNOWN_TEST_TYPE"), "", nil},
	}

	for _, step := range feed {
		t.Run(string(step.typ), func(t *testing.T) {
			evt := NewEvent(step.typ, "test", step.storyID, step.payload)
			if err := ps.Project(evt); err != nil {
				t.Errorf("Project(%s): %v", step.typ, err)
			}
		})
	}

	// After all events, the projection should know about the
	// requirements and at least one story.
	reqs, err := ps.ListRequirementsFiltered(ReqFilter{})
	if err != nil {
		t.Fatalf("ListRequirementsFiltered: %v", err)
	}
	if len(reqs) < 1 {
		t.Errorf("expected at least 1 requirement in projection; got %d", len(reqs))
	}
}

// TestProject_AgentTerminated_TransitionsRow locks in the wiring fix: an
// AGENT_TERMINATED event (dashboard kill / controller auto-cancel) must
// transition the agent row to status='terminated' and clear its
// current_story_id. Before the fix the event fell through Project's default
// case and silently no-op'd, so a killed agent stayed status='idle' with a
// live session→story mapping that crash recovery still consumed.
func TestProject_AgentTerminated_TransitionsRow(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	spawn := NewEvent(EventAgentSpawned, "agent-42", "S1", map[string]any{
		"role": "junior", "session_name": "nxd-S1",
	})
	if err := ps.Project(spawn); err != nil {
		t.Fatalf("project spawn: %v", err)
	}

	// Precondition: the spawned agent is idle and mapped to its story.
	idle, err := ps.ListAgents(AgentFilter{Status: "idle"})
	if err != nil {
		t.Fatalf("list idle: %v", err)
	}
	if len(idle) != 1 || idle[0].CurrentStoryID != "S1" {
		t.Fatalf("precondition: want 1 idle agent on S1, got %+v", idle)
	}

	// Kill it.
	term := NewEvent(EventAgentTerminated, "agent-42", "", map[string]any{
		"reason": "killed from dashboard", "source": "dashboard",
	})
	if err := ps.Project(term); err != nil {
		t.Fatalf("project terminate: %v", err)
	}

	// The agent must no longer be idle...
	if idle, _ := ps.ListAgents(AgentFilter{Status: "idle"}); len(idle) != 0 {
		t.Errorf("killed agent still listed as idle: %+v", idle)
	}
	// ...it must be terminated with its story mapping cleared.
	term2, err := ps.ListAgents(AgentFilter{Status: "terminated"})
	if err != nil {
		t.Fatalf("list terminated: %v", err)
	}
	if len(term2) != 1 {
		t.Fatalf("want 1 terminated agent, got %d", len(term2))
	}
	if term2[0].CurrentStoryID != "" {
		t.Errorf("terminated agent should have current_story_id cleared, got %q", term2[0].CurrentStoryID)
	}
}

// TestProject_StoryStarted_PersistsBranch verifies the STORY_STARTED handler
// writes the branch carried in its payload into the stories projection. The
// branch column feeds the standalone CLI commands (nxd merge/review/gc/archive/
// status) that read a story from the projection; before the fix the handler
// only set status and the branch stayed empty forever.
func TestProject_StoryStarted_PersistsBranch(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	req := NewEvent(EventReqSubmitted, "", "", map[string]any{
		"id": "R", "title": "t", "description": "d", "repo_path": "/tmp",
	})
	if err := ps.Project(req); err != nil {
		t.Fatalf("project req: %v", err)
	}
	created := NewEvent(EventStoryCreated, "", "S1", map[string]any{
		"id": "S1", "req_id": "R", "title": "story", "description": "d", "complexity": 3,
	})
	if err := ps.Project(created); err != nil {
		t.Fatalf("project story created: %v", err)
	}

	// Precondition: no branch yet.
	pre, err := ps.GetStory("S1")
	if err != nil {
		t.Fatalf("precondition get story: %v", err)
	}
	if pre.Branch != "" {
		t.Fatalf("precondition: want empty branch, got %q", pre.Branch)
	}

	started := NewEvent(EventStoryStarted, "agent-1", "S1", map[string]any{
		"branch": "nxd/S1", "worktree_path": "/tmp/wt", "role": "junior",
	})
	if err := ps.Project(started); err != nil {
		t.Fatalf("project story started: %v", err)
	}

	got, err := ps.GetStory("S1")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if got.Status != "in_progress" {
		t.Errorf("want status in_progress, got %q", got.Status)
	}
	if got.Branch != "nxd/S1" {
		t.Errorf("want branch nxd/S1, got %q", got.Branch)
	}

	// A later STORY_STARTED with no branch (e.g. a replay of an older event)
	// must not clobber the persisted branch.
	restart := NewEvent(EventStoryStarted, "agent-1", "S1", map[string]any{"role": "junior"})
	if err := ps.Project(restart); err != nil {
		t.Fatalf("project restart: %v", err)
	}
	after, err := ps.GetStory("S1")
	if err != nil {
		t.Fatalf("get story after restart: %v", err)
	}
	if after.Branch != "nxd/S1" {
		t.Errorf("branch clobbered by branchless event: got %q", after.Branch)
	}
}

// seedStory projects a requirement and one story so tests can drive later
// lifecycle events against it.
func seedStory(t *testing.T, ps *SQLiteStore, storyID string) {
	t.Helper()
	req := NewEvent(EventReqSubmitted, "", "", map[string]any{
		"id": "R", "title": "t", "description": "d", "repo_path": "/tmp",
	})
	if err := ps.Project(req); err != nil {
		t.Fatalf("project req: %v", err)
	}
	created := NewEvent(EventStoryCreated, "", storyID, map[string]any{
		"id": storyID, "req_id": "R", "title": "story", "description": "d", "complexity": 3,
	})
	if err := ps.Project(created); err != nil {
		t.Fatalf("project story created: %v", err)
	}
}

// TestProject_StoryStarted_RedispatchOverwritesBranch: a second STORY_STARTED
// carrying a different, non-empty branch (an escalation re-dispatch) wins.
func TestProject_StoryStarted_RedispatchOverwritesBranch(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()
	seedStory(t, ps, "S1")

	for _, branch := range []string{"nxd/S1", "nxd/S1-retry"} {
		evt := NewEvent(EventStoryStarted, "agent-1", "S1", map[string]any{"branch": branch})
		if err := ps.Project(evt); err != nil {
			t.Fatalf("project started (%s): %v", branch, err)
		}
	}
	got, err := ps.GetStory("S1")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if got.Branch != "nxd/S1-retry" {
		t.Errorf("want re-dispatch branch nxd/S1-retry, got %q", got.Branch)
	}
}

// TestBackfillStoryBranches: a row projected before the branch was persisted
// gets it from the STORY_STARTED event (last event wins, as on replay); a row
// that already has a branch is untouched; events without a branch or story id
// are ignored.
func TestBackfillStoryBranches(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()
	seedStory(t, ps, "S1")
	created2 := NewEvent(EventStoryCreated, "", "S2", map[string]any{
		"id": "S2", "req_id": "R", "title": "story2", "description": "d", "complexity": 2,
	})
	if err := ps.Project(created2); err != nil {
		t.Fatalf("project S2: %v", err)
	}
	// S2 already has a branch via the projection.
	if err := ps.Project(NewEvent(EventStoryStarted, "a", "S2", map[string]any{"branch": "nxd/S2"})); err != nil {
		t.Fatalf("project S2 started: %v", err)
	}

	err = ps.BackfillStoryBranches([]Event{
		NewEvent(EventStoryStarted, "a", "S1", map[string]any{"branch": "nxd/S1"}),
		NewEvent(EventStoryStarted, "a", "S1", map[string]any{"branch": "nxd/S1-later"}), // last wins
		NewEvent(EventStoryStarted, "a", "S2", map[string]any{"branch": "nxd/S2-other"}),
		NewEvent(EventStoryStarted, "a", "", map[string]any{"branch": "nxd/none"}),
		NewEvent(EventStoryCompleted, "a", "S1", map[string]any{"branch": "nxd/wrong-type"}),
	})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	s1, err := ps.GetStory("S1")
	if err != nil {
		t.Fatalf("get S1: %v", err)
	}
	if s1.Branch != "nxd/S1-later" {
		t.Errorf("S1: want the last STORY_STARTED branch nxd/S1-later, got %q", s1.Branch)
	}
	s2, err := ps.GetStory("S2")
	if err != nil {
		t.Fatalf("get S2: %v", err)
	}
	if s2.Branch != "nxd/S2" {
		t.Errorf("S2: existing branch must be untouched, got %q", s2.Branch)
	}
}

// TestStoryBranch_FallsBackToCanonicalName: the projected branch wins; a row
// without one resolves to the dispatcher's "nxd/<id>", never to "".
func TestStoryBranch_FallsBackToCanonicalName(t *testing.T) {
	if got := StoryBranch(Story{ID: "s-1", Branch: "feature/x"}); got != "feature/x" {
		t.Errorf("projected branch must win, got %q", got)
	}
	if got := StoryBranch(Story{ID: "s-1"}); got != "nxd/s-1" {
		t.Errorf("empty branch must fall back to the canonical name, got %q", got)
	}
	if got := StoryBranch(Story{}); got != "" {
		t.Errorf("a story without an ID has no branch, got %q", got)
	}
	if got := CanonicalStoryBranch("s-1"); got != "nxd/s-1" {
		t.Errorf("canonical name: got %q", got)
	}
}

// knownEventTypes reads every EventType constant declared in this package's
// non-test files. The strict pattern is checked against a loose count of
// `EventType = "` declarations, so a constant declared in another file, or
// with a digit in its value, cannot slip past the test.
func knownEventTypes(t *testing.T) []EventType {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	strict := regexp.MustCompile(`(?m)^\s*Event\w+\s+EventType\s*=\s*"([A-Z0-9_]+)"`)
	loose := regexp.MustCompile(`EventType\s*=\s*"`)
	var types []EventType
	declared := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range strict.FindAllStringSubmatch(string(src), -1) {
			types = append(types, EventType(m[1]))
		}
		declared += len(loose.FindAllString(string(src), -1))
	}
	if len(types) == 0 || len(types) != declared {
		t.Fatalf("matched %d event type constants but %d `EventType = \"` declarations; the pattern is stale", len(types), declared)
	}
	return types
}

// TestProjectLocked_EveryKnownTypeHasACase drives every EventType constant
// through projectLocked: none may fall to the default arm. A new event type must pick a projection or be listed as
// a deliberate no-op. Unknown types (a newer log read by an older binary)
// are still ignored by Project.
func TestProjectLocked_EveryKnownTypeHasACase(t *testing.T) {
	types := knownEventTypes(t)

	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()
	for _, typ := range types {
		// Payloads are empty: handlers may fail on that, but only the default
		// arm returns errUnprojected.
		if err := ps.projectLocked(NewEvent(typ, "test", "S", nil)); errors.Is(err, errUnprojected) {
			t.Errorf("%s has no projection case: add one, or list it as a deliberate no-op", typ)
		}
	}
	if err := ps.projectLocked(NewEvent(EventType("UNKNOWN_TEST_TYPE"), "test", "", nil)); !errors.Is(err, errUnprojected) {
		t.Errorf("an unknown type must reach the default arm, got %v", err)
	}
	if err := ps.Project(NewEvent(EventType("UNKNOWN_TEST_TYPE"), "test", "", nil)); err != nil {
		t.Errorf("Project must ignore an unknown type for forward compatibility, got %v", err)
	}
}

// TestProject_UnknownType_LogsAndAdvancesWatermark: forward compatibility is
// not silence — the skipped event is logged and the watermark still moves
// past it, so the next open does not rebuild forever.
func TestProject_UnknownType_LogsAndAdvancesWatermark(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()
	before, err := ps.AppliedEventCount()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	if err := ps.Project(NewEvent(EventType("UNKNOWN_TEST_TYPE"), "test", "", nil)); err != nil {
		t.Fatalf("Project must ignore an unknown type, got %v", err)
	}
	after, err := ps.AppliedEventCount()
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("watermark must advance past the ignored event: %d -> %d", before, after)
	}
	if !strings.Contains(logs.String(), `ignoring unknown event type "UNKNOWN_TEST_TYPE"`) {
		t.Fatalf("the ignored event must be logged, got %q", logs.String())
	}
}

// TestProject_UnknownType_LogQuoted: the event type and id come from
// events.jsonl; a newline in either must not forge a second log line.
func TestProject_UnknownType_LogQuoted(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	evt := NewEvent(EventType("UNKNOWN\nnxd: all clear"), "test", "", nil)
	if err := ps.Project(evt); err != nil {
		t.Fatalf("Project must ignore an unknown type, got %v", err)
	}
	body := strings.TrimSuffix(logs.String(), "\n")
	if strings.Count(body, "\n") != 0 {
		t.Fatalf("the ignored event must produce exactly one log line, got:\n%s", body)
	}
	if !strings.Contains(body, `\n`) {
		t.Fatalf("the newline must be escaped by %%q, got: %s", body)
	}
}
