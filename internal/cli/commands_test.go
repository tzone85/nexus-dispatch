package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ── Status command ───────────────────────────────────────────────────

func TestStatusCmd_EmptyState(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--all")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "No requirements found") {
		t.Errorf("expected 'No requirements found' message, got:\n%s", out)
	}
}

func TestStatusCmd_WithRequirements(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Build auth module", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Add login endpoint", 3)
	seedTestStory(t, env, "s-00200", "req-00100", "Add logout endpoint", 2)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--all")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Build auth module") {
		t.Error("expected requirement title in output")
	}
	if !strings.Contains(out, "2 total") {
		t.Error("expected story count in output")
	}
}

func TestStatusCmd_FilterByReq(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--req", "req-00100")
	if err != nil {
		t.Fatalf("status --req: %v", err)
	}
	if !strings.Contains(out, "Auth module") {
		t.Error("expected requirement title")
	}
	if !strings.Contains(out, "Login") {
		t.Error("expected story title")
	}
	if !strings.Contains(out, "Complexity: 3") {
		t.Error("expected complexity in story detail")
	}
}

func TestStatusCmd_ReqNotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newStatusCmd()
	_, err := execCmd(t, cmd, env.Config, "--req", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

func TestStatusCmd_JSONMode(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--all", "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	var result jsonStatusOutput
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("expected 1 requirement in JSON, got %d", len(result.Requirements))
	}
	if result.Requirements[0].Title != "Auth module" {
		t.Errorf("title = %q, want 'Auth module'", result.Requirements[0].Title)
	}
	if len(result.Requirements[0].Stories) != 1 {
		t.Errorf("stories = %d, want 1", len(result.Requirements[0].Stories))
	}
}

func TestStatusCmd_JSONFilterByReq(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestReq(t, env, "req-00200", "Dashboard", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)
	seedTestStory(t, env, "s-00200", "req-00200", "Charts", 5)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--json", "--req", "req-00100")
	if err != nil {
		t.Fatalf("status --json --req: %v", err)
	}
	var result jsonStatusOutput
	json.Unmarshal([]byte(out), &result)
	if len(result.Requirements) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(result.Requirements))
	}
	if result.Requirements[0].ID != "req-00100" {
		t.Errorf("expected req-00100, got %s", result.Requirements[0].ID)
	}
}

// ── Agents command ───────────────────────────────────────────────────

func TestAgentsCmd_NoAgents(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newAgentsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	if !strings.Contains(out, "No agents found") {
		t.Errorf("expected 'No agents found', got:\n%s", out)
	}
}

func TestAgentsCmd_WithAgents(t *testing.T) {
	env := setupTestEnv(t)
	seedTestAgent(t, env, "agent-00100", "senior", "nxd-s-00100")
	seedTestAgent(t, env, "agent-00200", "junior", "nxd-s-00200")

	cmd := newAgentsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	if !strings.Contains(out, "agent-00100") {
		t.Error("expected agent-00100 in output")
	}
	if !strings.Contains(out, "agent-00200") {
		t.Error("expected agent-00200 in output")
	}
	if !strings.Contains(out, "Agents (2)") {
		t.Error("expected agent count header")
	}
}

func TestAgentsCmd_StatusFilter(t *testing.T) {
	env := setupTestEnv(t)
	seedTestAgent(t, env, "agent-00100", "senior", "nxd-s-00100")

	cmd := newAgentsCmd()
	out, err := execCmd(t, cmd, env.Config, "--status", "terminated")
	if err != nil {
		t.Fatalf("agents --status: %v", err)
	}
	if !strings.Contains(out, "No agents with status") {
		t.Errorf("expected no agents with terminated status, got:\n%s", out)
	}
}

// ── Events command ───────────────────────────────────────────────────

func TestEventsCmd_NoEvents(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !strings.Contains(out, "No events found") {
		t.Errorf("expected 'No events found', got:\n%s", out)
	}
}

func TestEventsCmd_WithEvents(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if !strings.Contains(out, "REQ_SUBMITTED") {
		t.Error("expected REQ_SUBMITTED in events")
	}
	if !strings.Contains(out, "STORY_CREATED") {
		t.Error("expected STORY_CREATED in events")
	}
}

func TestEventsCmd_TypeFilter(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config, "--type", "STORY_CREATED")
	if err != nil {
		t.Fatalf("events --type: %v", err)
	}
	if !strings.Contains(out, "STORY_CREATED") {
		t.Error("expected STORY_CREATED")
	}
	if strings.Contains(out, "REQ_SUBMITTED") {
		t.Error("should not contain REQ_SUBMITTED when filtering by STORY_CREATED")
	}
}

func TestEventsCmd_StoryFilter(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)
	seedTestStory(t, env, "s-00200", "req-00100", "Logout", 2)

	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config, "--story", "s-00100")
	if err != nil {
		t.Fatalf("events --story: %v", err)
	}
	if !strings.Contains(out, "s-00100") {
		t.Error("expected s-001 in filtered events")
	}
}

func TestEventsCmd_Limit(t *testing.T) {
	env := setupTestEnv(t)
	for i := 0; i < 10; i++ {
		env.Events.Append(state.NewEvent(state.EventStoryProgress, "agent", "story", map[string]any{
			"iteration": i,
		}))
	}

	cmd := newEventsCmd()
	out, err := execCmd(t, cmd, env.Config, "--limit", "3")
	if err != nil {
		t.Fatalf("events --limit: %v", err)
	}
	if !strings.Contains(out, "3 shown of 10 total") {
		t.Errorf("expected '3 shown of 10 total', got:\n%s", out)
	}
}

// ── Escalations command ──────────────────────────────────────────────

func TestEscalationsCmd_NoEscalations(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newEscalationsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("escalations: %v", err)
	}
	if !strings.Contains(out, "No escalations found") {
		t.Errorf("expected 'No escalations found', got:\n%s", out)
	}
}

func TestEscalationsCmd_WithEscalations(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)
	seedTestEscalation(t, env, "s-00100", "agent-junior-001", "tests failing")

	cmd := newEscalationsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("escalations: %v", err)
	}
	if !strings.Contains(out, "Escalations (1)") {
		t.Errorf("expected escalation count header, got:\n%s", out)
	}
	if !strings.Contains(out, "tests failing") {
		t.Error("expected escalation reason in output")
	}
}

// ── Pause command ────────────────────────────────────────────────────

func TestPauseCmd_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)

	// Move to planned status (pausable).
	planned := state.NewEvent(state.EventReqPlanned, "system", "", map[string]any{"id": "req-00100"})
	env.Events.Append(planned)
	env.Proj.Project(planned)

	cmd := newPauseCmd()
	out, err := execCmd(t, cmd, env.Config, "req-00100")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if !strings.Contains(out, "Paused requirement") {
		t.Errorf("expected success message, got:\n%s", out)
	}

	// Verify state changed.
	req, _ := env.Proj.GetRequirement("req-00100")
	if req.Status != "paused" {
		t.Errorf("expected status=paused, got %q", req.Status)
	}
}

func TestPauseCmd_AlreadyPaused(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)

	paused := state.NewEvent(state.EventReqPaused, "system", "", map[string]any{"id": "req-00100"})
	env.Events.Append(paused)
	env.Proj.Project(paused)

	cmd := newPauseCmd()
	_, err := execCmd(t, cmd, env.Config, "req-00100")
	if err == nil {
		t.Error("expected error when pausing already-paused requirement")
	}
}

func TestPauseCmd_Completed(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)

	completed := state.NewEvent(state.EventReqCompleted, "system", "", map[string]any{"id": "req-00100"})
	env.Events.Append(completed)
	env.Proj.Project(completed)

	cmd := newPauseCmd()
	_, err := execCmd(t, cmd, env.Config, "req-00100")
	if err == nil {
		t.Error("expected error when pausing completed requirement")
	}
}

func TestPauseCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newPauseCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

// ── Config command ───────────────────────────────────────────────────

func TestConfigShowCmd(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newConfigShowCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	// Should contain YAML output with workspace section.
	if !strings.Contains(out, "workspace") {
		t.Errorf("expected 'workspace' in config output, got:\n%s", out)
	}
	if !strings.Contains(out, "state_dir") {
		t.Errorf("expected 'state_dir' in config output")
	}
}

func TestConfigValidateCmd(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newConfigValidateCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("config validate: %v", err)
	}
	if !strings.Contains(out, "PASSED") {
		t.Errorf("expected 'PASSED' in validate output, got:\n%s", out)
	}
}

// ── GC command (dry-run only) ────────────────────────────────────────

func TestGCCmd_NoMergedStories(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newGCCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if !strings.Contains(out, "No merged stories") {
		t.Errorf("expected 'No merged stories' message, got:\n%s", out)
	}
}

func TestGCCmd_DryRun(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login", 3)

	// Move story to merged with a branch.
	assigned := state.NewEvent(state.EventStoryAssigned, "agent-1", "s-00100", map[string]any{
		"agent_id": "agent-1", "wave": 1,
	})
	env.Events.Append(assigned)
	env.Proj.Project(assigned)

	merged := state.NewEvent(state.EventStoryMerged, "monitor", "s-00100", nil)
	env.Events.Append(merged)
	env.Proj.Project(merged)

	cmd := newGCCmd()
	out, err := execCmd(t, cmd, env.Config, "--dry-run")
	if err != nil {
		t.Fatalf("gc --dry-run: %v", err)
	}
	if !strings.Contains(out, "Dry run") {
		t.Errorf("expected dry run output, got:\n%s", out)
	}
}

// ── Metrics command ──────────────────────────────────────────────────

func TestMetricsCmd_NoData(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newMetricsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if !strings.Contains(out, "No metrics recorded") {
		t.Errorf("expected 'No metrics recorded', got:\n%s", out)
	}
}

// ── Utility functions ────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 10, "this is..."},
		{"ab", 3, "ab"},
		{"abcdef", 3, "abc"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestCountByStatus(t *testing.T) {
	stories := []state.Story{
		{Status: "draft"},
		{Status: "draft"},
		{Status: "in_progress"},
		{Status: "merged"},
	}
	counts := countByStatus(stories)
	if counts["draft"] != 2 {
		t.Errorf("draft count = %d, want 2", counts["draft"])
	}
	if counts["in_progress"] != 1 {
		t.Errorf("in_progress count = %d, want 1", counts["in_progress"])
	}
	if counts["merged"] != 1 {
		t.Errorf("merged count = %d, want 1", counts["merged"])
	}
}

func TestReverseEvents(t *testing.T) {
	events := []state.Event{
		{ID: "1"}, {ID: "2"}, {ID: "3"},
	}
	reversed := reverseEvents(events)
	if reversed[0].ID != "3" || reversed[1].ID != "2" || reversed[2].ID != "1" {
		t.Errorf("reverseEvents failed: got [%s, %s, %s]", reversed[0].ID, reversed[1].ID, reversed[2].ID)
	}
	// Verify original is not mutated.
	if events[0].ID != "1" {
		t.Error("original slice was mutated")
	}
}

func TestReverseEvents_Empty(t *testing.T) {
	reversed := reverseEvents(nil)
	if len(reversed) != 0 {
		t.Errorf("expected empty slice, got %d", len(reversed))
	}
}

func TestFormatPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantSub string
	}{
		{"valid json", []byte(`{"key":"value"}`), "key"},
		{"empty", nil, ""},
		{"invalid json", []byte(`not json`), "not json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPayload(tt.payload)
			if tt.wantSub != "" && !strings.Contains(got, tt.wantSub) {
				t.Errorf("formatPayload() = %q, want substring %q", got, tt.wantSub)
			}
		})
	}
}

func TestFormatPayload_LongTruncation(t *testing.T) {
	// Build a payload > 200 chars.
	long := `{"data":"` + strings.Repeat("x", 250) + `"}`
	got := formatPayload([]byte(long))
	if len(got) > 200 {
		t.Errorf("expected truncated payload <= 200 chars, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected truncated payload to end with '...'")
	}
}

func TestValidatePausable(t *testing.T) {
	tests := []struct {
		status  string
		wantErr bool
	}{
		{"planned", false},
		{"in_progress", false},
		{"paused", true},
		{"completed", true},
		{"pending", true},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			req := state.Requirement{ID: "r-001", Status: tt.status}
			err := validatePausable(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePausable(%q) error = %v, wantErr = %v", tt.status, err, tt.wantErr)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	got := expandHome("~/.nxd")
	if strings.HasPrefix(got, "~") {
		t.Error("expandHome did not expand ~")
	}
	if !strings.HasSuffix(got, ".nxd") {
		t.Errorf("expected path ending with .nxd, got %s", got)
	}
}

func TestExpandHome_Empty(t *testing.T) {
	got := expandHome("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExpandHome_Absolute(t *testing.T) {
	got := expandHome("/usr/local/bin")
	if got != "/usr/local/bin" {
		t.Errorf("expected /usr/local/bin, got %q", got)
	}
}

// ── Logs command ─────────────────────────────────────────────────────

func TestLogsCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newLogsCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error for nonexistent story trace")
	}
}

// ── Diff command ─────────────────────────────────────────────────────

func TestDiffCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newDiffCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error for nonexistent story worktree")
	}
}

// ── Approve command ─────────────────────────────────────────

func TestApproveCmd_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)

	// Move to pending_review.
	pending := state.NewEvent(state.EventReqPendingReview, "system", "", map[string]any{"id": "req-00100"})
	env.Events.Append(pending)
	env.Proj.Project(pending)

	cmd := newApproveCmd()
	out, err := execCmd(t, cmd, env.Config, "req-00100")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !strings.Contains(out, "Approved requirement") {
		t.Errorf("expected success message, got:\n%s", out)
	}

	req, _ := env.Proj.GetRequirement("req-00100")
	if req.Status != "planned" {
		t.Errorf("expected status=planned, got %q", req.Status)
	}
}

func TestApproveCmd_NotPendingReview(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)

	cmd := newApproveCmd()
	_, err := execCmd(t, cmd, env.Config, "req-00100")
	if err == nil {
		t.Error("expected error when requirement is not pending_review")
	}
}

func TestApproveCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newApproveCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

// ── Reject command ──────────────────────────────────────────

func TestRejectCmd_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)

	pending := state.NewEvent(state.EventReqPendingReview, "system", "", map[string]any{"id": "req-00100"})
	env.Events.Append(pending)
	env.Proj.Project(pending)

	cmd := newRejectCmd()
	out, err := execCmd(t, cmd, env.Config, "req-00100")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if !strings.Contains(out, "Rejected requirement") {
		t.Errorf("expected success message, got:\n%s", out)
	}

	req, _ := env.Proj.GetRequirement("req-00100")
	if req.Status != "rejected" {
		t.Errorf("expected status=rejected, got %q", req.Status)
	}
}

func TestRejectCmd_NotPendingReview(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)

	cmd := newRejectCmd()
	_, err := execCmd(t, cmd, env.Config, "req-00100")
	if err == nil {
		t.Error("expected error when not pending_review")
	}
}

// ── Report command ──────────────────────────────────────────

func TestReportCmd_Success(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth module", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Login endpoint", 3)

	cmd := newReportCmd()
	out, err := execCmd(t, cmd, env.Config, "req-00100")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(out, "Auth module") {
		t.Errorf("expected requirement title in report, got:\n%s", out)
	}
}

func TestReportCmd_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newReportCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

// ── Estimate print functions ────────────────────────────────

func TestPrintEstimateJSON(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Build auth",
		Summary: engine.EstimateSummary{
			StoryCount:  2,
			TotalPoints: 6,
			HoursLow:    4,
			HoursHigh:   8,
			Rate:        150,
			Currency:    "USD",
		},
	}
	err := printEstimateJSON(est)
	if err != nil {
		t.Fatalf("printEstimateJSON: %v", err)
	}
}

func TestPrintEstimateTable_Live(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Build auth",
		Stories: []engine.StoryEstimate{
			{Title: "Login", Complexity: 3, Role: "junior", HoursLow: 2, HoursHigh: 3},
		},
		Summary: engine.EstimateSummary{
			StoryCount: 1, TotalPoints: 3,
			HoursLow: 2, HoursHigh: 3,
			QuoteLow: 300, QuoteHigh: 450,
			Rate: 150, Currency: "USD",
		},
	}
	err := printEstimateTable(est)
	if err != nil {
		t.Fatalf("printEstimateTable (live): %v", err)
	}
}

func TestPrintEstimateTable_Quick(t *testing.T) {
	est := engine.Estimate{
		IsQuick:     true,
		Requirement: "Build auth",
		Summary: engine.EstimateSummary{
			StoryCount: 3, HoursLow: 6, HoursHigh: 12,
			QuoteLow: 900, QuoteHigh: 1800,
		},
	}
	err := printEstimateTable(est)
	if err != nil {
		t.Fatalf("printEstimateTable (quick): %v", err)
	}
}

// ── Logs utility functions ──────────────────────────────────

func TestFormatEntry_ToolCall(t *testing.T) {
	var buf strings.Builder
	line := `{"timestamp":"2026-04-12T00:00:00Z","phase":"tool_call","tool":"write_file","detail":"writing main.go"}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "write_file") {
		t.Errorf("expected tool name in output, got: %s", out)
	}
	if !strings.Contains(out, "writing main.go") {
		t.Errorf("expected detail in output, got: %s", out)
	}
}

func TestFormatEntry_Phase(t *testing.T) {
	var buf strings.Builder
	line := `{"timestamp":"2026-04-12T00:00:00Z","phase":"thinking","iteration":3,"detail":"iteration 3/20"}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "thinking") {
		t.Errorf("expected phase in output, got: %s", out)
	}
	if !strings.Contains(out, "iter=3") {
		t.Errorf("expected iteration in output, got: %s", out)
	}
}

func TestFormatEntry_Error(t *testing.T) {
	var buf strings.Builder
	line := `{"timestamp":"2026-04-12T00:00:00Z","type":"error","is_error":true,"detail":"timeout"}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "[ERROR]") {
		t.Errorf("expected [ERROR] marker, got: %s", out)
	}
}

func TestFormatEntry_InvalidJSON(t *testing.T) {
	var buf strings.Builder
	formatEntry(&buf, "not json at all")
	out := buf.String()
	if !strings.Contains(out, "not json at all") {
		t.Errorf("expected raw line passthrough, got: %s", out)
	}
}

func TestTailLog(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "trace.jsonl")
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, `{"timestamp":"2026-04-12T00:00:00Z","type":"progress","detail":"line"}`)
	}
	os.WriteFile(tmpFile, []byte(strings.Join(lines, "\n")), 0o644)

	var buf strings.Builder
	err := tailLog(tmpFile, 3, true, &buf)
	if err != nil {
		t.Fatalf("tailLog: %v", err)
	}
	// Should only show last 3 lines in raw mode.
	outLines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(outLines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(outLines))
	}
}

// ── Models utility functions ─────────────────────────────────

func TestCollectConfiguredModels(t *testing.T) {
	cfg := config.DefaultConfig()
	ollama, google := collectConfiguredModels(cfg)

	if len(ollama) == 0 {
		t.Error("expected at least one Ollama model")
	}
	// Default config uses google+ollama provider, so google models may be present.
	_ = google // may be empty if no google_model is configured
}

// ── Resume utility functions ─────────────────────────────────

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if !dirExists(dir) {
		t.Error("expected true for existing directory")
	}
	if dirExists(filepath.Join(dir, "nonexistent")) {
		t.Error("expected false for nonexistent path")
	}
	// File is not a directory.
	f := filepath.Join(dir, "file.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	if dirExists(f) {
		t.Error("expected false for file (not directory)")
	}
}

func TestRebuildDAG(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00100", "Auth", env.Dir)
	seedTestStory(t, env, "s-00100", "req-00100", "Scaffold", 2)
	seedTestStory(t, env, "s-00200", "req-00100", "Feature", 5)

	// Add dependency: s-00200 depends on s-00100.
	depEvt := state.NewEvent(state.EventStoryCreated, "tl", "s-00300", map[string]any{
		"id": "s-00300", "req_id": "req-00100", "title": "Tests",
		"description": "d", "complexity": 3,
		"depends_on": []any{"s-00100"},
	})
	env.Events.Append(depEvt)
	env.Proj.Project(depEvt)

	stories, _ := env.Proj.ListStories(state.StoryFilter{ReqID: "req-00100"})
	dag, planned, err := rebuildDAG(env.Proj, "req-00100", stories)
	if err != nil {
		t.Fatalf("rebuildDAG: %v", err)
	}
	if len(planned) != 3 {
		t.Fatalf("expected 3 planned stories, got %d", len(planned))
	}
	// DAG should have the dependency edge.
	ready := dag.ReadyNodes(map[string]bool{})
	// s-00100 and s-00200 should be ready (no deps or deps not in DAG).
	// s-00300 depends on s-00100, so it should NOT be ready.
	readySet := make(map[string]bool)
	for _, id := range ready {
		readySet[id] = true
	}
	if readySet["s-00300"] {
		t.Error("s-00300 should not be ready (depends on s-00100)")
	}
	if !readySet["s-00100"] {
		t.Error("s-00100 should be ready (no dependencies)")
	}
}

// ── ResolveRequirement ─────────────────────────────────────

func TestResolveRequirement_FromArgs(t *testing.T) {
	cmd := newReqCmd()
	cmd.Flags().String("config", "", "")
	text, err := resolveRequirement(cmd, []string{"Build a REST API"})
	if err != nil {
		t.Fatalf("resolveRequirement: %v", err)
	}
	if text != "Build a REST API" {
		t.Errorf("expected 'Build a REST API', got %q", text)
	}
}

func TestResolveRequirement_FromFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "req.txt")
	os.WriteFile(tmpFile, []byte("Add health check endpoint\n"), 0o644)

	cmd := newReqCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("file", tmpFile)
	text, err := resolveRequirement(cmd, nil)
	if err != nil {
		t.Fatalf("resolveRequirement: %v", err)
	}
	if text != "Add health check endpoint" {
		t.Errorf("expected 'Add health check endpoint', got %q", text)
	}
}

func TestResolveRequirement_EmptyFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "empty.txt")
	os.WriteFile(tmpFile, []byte("  \n"), 0o644)

	cmd := newReqCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("file", tmpFile)
	_, err := resolveRequirement(cmd, nil)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestResolveRequirement_NoInput(t *testing.T) {
	cmd := newReqCmd()
	cmd.Flags().String("config", "", "")
	_, err := resolveRequirement(cmd, nil)
	if err == nil {
		t.Error("expected error when no args and no --file")
	}
}

func TestResolveRequirement_BothArgsAndFile(t *testing.T) {
	cmd := newReqCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("file", "some-file.txt")
	_, err := resolveRequirement(cmd, []string{"some text"})
	if err == nil {
		t.Error("expected error when both args and --file provided")
	}
}

// ── Command registration ────────────────────────────────────────

func TestAllCommandsRegistered(t *testing.T) {
	expected := []string{
		"init", "req", "status", "pause", "resume", "agents",
		"escalations", "gc", "config", "events", "dashboard",
		"archive", "models", "metrics", "watch", "plan",
		"approve", "reject", "review", "merge",
		"doctor", "estimate", "report", "logs", "diff",
	}

	registered := map[string]bool{}
	for _, cmd := range rootCmd.Commands() {
		registered[cmd.Use] = true
		// Handle commands with args: "pause <req-id>" → "pause"
		parts := strings.Fields(cmd.Use)
		registered[parts[0]] = true
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("command %q not registered on rootCmd", name)
		}
	}
}
