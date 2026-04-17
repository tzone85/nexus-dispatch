package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ---------------------------------------------------------------------------
// truncateStr
// ---------------------------------------------------------------------------

func TestTruncateStr_ShortString(t *testing.T) {
	if got := truncateStr("hi", 10); got != "hi" {
		t.Errorf("truncateStr short = %q, want %q", got, "hi")
	}
}

func TestTruncateStr_ExactLength(t *testing.T) {
	if got := truncateStr("hello", 5); got != "hello" {
		t.Errorf("truncateStr exact = %q, want %q", got, "hello")
	}
}

func TestTruncateStr_LongString(t *testing.T) {
	got := truncateStr("abcdefghij", 6)
	if got != "abc..." {
		t.Errorf("truncateStr long = %q, want %q", got, "abc...")
	}
}

func TestTruncateStr_MaxLenThree(t *testing.T) {
	// maxLen <= 3: no ellipsis, just cut
	got := truncateStr("abcdef", 3)
	if got != "abc" {
		t.Errorf("truncateStr maxLen=3 = %q, want %q", got, "abc")
	}
}

func TestTruncateStr_MaxLenTwo(t *testing.T) {
	got := truncateStr("abcdef", 2)
	if got != "ab" {
		t.Errorf("truncateStr maxLen=2 = %q, want %q", got, "ab")
	}
}

func TestTruncateStr_EmptyString(t *testing.T) {
	if got := truncateStr("", 5); got != "" {
		t.Errorf("truncateStr empty = %q, want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// storyStatusStyle — verify all branches return a non-zero style
// ---------------------------------------------------------------------------

func TestStoryStatusStyle_AllStatuses(t *testing.T) {
	cases := []struct {
		status string
	}{
		{"draft"},
		{"planned"},
		{"estimated"},
		{"assigned"},
		{"in_progress"},
		{"review"},
		{"qa"},
		{"qa_failed"},
		{"pr_submitted"},
		{"merged"},
		{"paused"},
		{"unknown_status"},
	}
	for _, tc := range cases {
		style := storyStatusStyle(tc.status)
		// Render a test string to confirm the style is usable
		rendered := style.Render("test")
		if rendered == "" {
			t.Errorf("storyStatusStyle(%q).Render() returned empty string", tc.status)
		}
	}
}

// ---------------------------------------------------------------------------
// agentStatusStyle
// ---------------------------------------------------------------------------

func TestAgentStatusStyle_AllStatuses(t *testing.T) {
	cases := []string{"active", "stuck", "idle", "terminated", "unknown"}
	for _, status := range cases {
		style := agentStatusStyle(status)
		if style.Render("test") == "" {
			t.Errorf("agentStatusStyle(%q) returned empty-rendering style", status)
		}
	}
}

// ---------------------------------------------------------------------------
// eventCategoryStyle
// ---------------------------------------------------------------------------

func TestEventCategoryStyle_AllPrefixes(t *testing.T) {
	cases := []struct {
		eventType string
		wantStyle string // just verifying it doesn't panic and returns a style
	}{
		{"REQ_CREATED", "req"},
		{"STORY_STARTED", "story"},
		{"AGENT_SPAWNED", "agent"},
		{"ESCALATION_RAISED", "escalation"},
		{"SUPERVISOR_ACTION", "supervisor"},
		{"CONTROLLER_TICK", "default"},
		{"", "default"},
	}
	for _, tc := range cases {
		style := eventCategoryStyle(tc.eventType)
		if style.Render("x") == "" {
			t.Errorf("eventCategoryStyle(%q) returned empty-rendering style", tc.eventType)
		}
	}
}

// ---------------------------------------------------------------------------
// mapStatusToBucket
// ---------------------------------------------------------------------------

func TestMapStatusToBucket(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"draft", "planned"},
		{"estimated", "planned"},
		{"planned", "planned"},
		{"assigned", "planned"},
		{"in_progress", "in_progress"},
		{"review", "review"},
		{"qa", "qa"},
		{"qa_started", "qa"},
		{"qa_failed", "qa"},
		{"pr_submitted", "pr_submitted"},
		{"merged", "merged"},
		{"split", "split"},
		{"unknown_xyz", "planned"}, // default
		{"", "planned"},            // empty string → default
	}
	for _, tc := range cases {
		got := mapStatusToBucket(tc.status)
		if got != tc.want {
			t.Errorf("mapStatusToBucket(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatProgressDetail
// ---------------------------------------------------------------------------

func TestFormatProgressDetail_Thinking(t *testing.T) {
	payload := map[string]any{"phase": "thinking", "iteration": float64(2), "max_iter": float64(10)}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "thinking") {
		t.Errorf("thinking phase = %q, want it to contain 'thinking'", got)
	}
	if !strings.Contains(got, "[2/10]") {
		t.Errorf("thinking phase = %q, want prefix [2/10]", got)
	}
}

func TestFormatProgressDetail_ToolCall_WithFile(t *testing.T) {
	payload := map[string]any{"phase": "tool_call", "tool": "write_file", "file": "main.go"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "write_file") || !strings.Contains(got, "main.go") {
		t.Errorf("tool_call with file = %q, want 'write_file main.go'", got)
	}
}

func TestFormatProgressDetail_ToolCall_WithDetail(t *testing.T) {
	payload := map[string]any{"phase": "tool_call", "tool": "bash", "detail": "go build ./..."}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "go build") {
		t.Errorf("tool_call with detail = %q, want 'go build'", got)
	}
}

func TestFormatProgressDetail_ToolCall_ToolOnly(t *testing.T) {
	payload := map[string]any{"phase": "tool_call", "tool": "list_files"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "list_files") {
		t.Errorf("tool_call tool-only = %q, want 'list_files'", got)
	}
}

func TestFormatProgressDetail_ToolResult_Success(t *testing.T) {
	payload := map[string]any{"phase": "tool_result", "file": "main.go", "is_error": false}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "ok") {
		t.Errorf("tool_result success = %q, want 'main.go -> ok'", got)
	}
}

func TestFormatProgressDetail_ToolResult_Failure(t *testing.T) {
	payload := map[string]any{"phase": "tool_result", "tool": "bash", "is_error": true}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "FAIL") {
		t.Errorf("tool_result failure = %q, want 'FAIL'", got)
	}
}

func TestFormatProgressDetail_ToolResult_NoFile(t *testing.T) {
	payload := map[string]any{"phase": "tool_result", "tool": "bash", "is_error": false}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "bash") || !strings.Contains(got, "ok") {
		t.Errorf("tool_result no-file = %q, want 'bash -> ok'", got)
	}
}

func TestFormatProgressDetail_Error_WithDetail(t *testing.T) {
	payload := map[string]any{"phase": "error", "detail": "timeout exceeded"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "ERR:") || !strings.Contains(got, "timeout exceeded") {
		t.Errorf("error phase with detail = %q, want 'ERR: timeout exceeded'", got)
	}
}

func TestFormatProgressDetail_Error_NoDetail(t *testing.T) {
	payload := map[string]any{"phase": "error"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "error") {
		t.Errorf("error phase no-detail = %q, want 'error'", got)
	}
}

func TestFormatProgressDetail_Completed_WithDetail(t *testing.T) {
	payload := map[string]any{"phase": "completed", "detail": "all tests pass"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "done:") || !strings.Contains(got, "all tests pass") {
		t.Errorf("completed with detail = %q, want 'done: all tests pass'", got)
	}
}

func TestFormatProgressDetail_Completed_NoDetail(t *testing.T) {
	payload := map[string]any{"phase": "completed"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "done") {
		t.Errorf("completed no-detail = %q, want 'done'", got)
	}
}

func TestFormatProgressDetail_Default_WithDetail(t *testing.T) {
	payload := map[string]any{"phase": "unknown_phase", "detail": "some info"}
	got := formatProgressDetail(payload)
	if !strings.Contains(got, "some info") {
		t.Errorf("default phase with detail = %q, want 'some info'", got)
	}
}

func TestFormatProgressDetail_Default_NoDetail(t *testing.T) {
	payload := map[string]any{"phase": "unknown_phase"}
	got := formatProgressDetail(payload)
	if got != "" {
		t.Errorf("default phase no-detail = %q, want empty string", got)
	}
}

func TestFormatProgressDetail_NoMaxIter(t *testing.T) {
	// max_iter = 0: no prefix generated
	payload := map[string]any{"phase": "thinking", "iteration": float64(1), "max_iter": float64(0)}
	got := formatProgressDetail(payload)
	if strings.Contains(got, "[") {
		t.Errorf("no prefix expected when max_iter=0, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// progressDetailStyle
// ---------------------------------------------------------------------------

func TestProgressDetailStyle_AllPhases(t *testing.T) {
	cases := []struct {
		payload map[string]any
	}{
		{map[string]any{"phase": "thinking"}},
		{map[string]any{"phase": "tool_call"}},
		{map[string]any{"phase": "tool_result"}},
		{map[string]any{"phase": "completed"}},
		{map[string]any{"phase": "error"}},
		{map[string]any{"phase": "unknown"}},
		{map[string]any{"phase": "tool_result", "is_error": true}},
		{map[string]any{}},
	}
	for _, tc := range cases {
		style := progressDetailStyle(tc.payload)
		if style.Render("x") == "" {
			t.Errorf("progressDetailStyle(%v) returned empty-rendering style", tc.payload)
		}
	}
}

// ---------------------------------------------------------------------------
// progressDetail
// ---------------------------------------------------------------------------

func makeEvent(eventType state.EventType, payload map[string]any) state.Event {
	return state.NewEvent(eventType, "agent-1", "story-1", payload)
}

func TestProgressDetail_EmptyPayload(t *testing.T) {
	evt := state.Event{Type: state.EventStoryProgress, Payload: nil}
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail nil payload = %q, want empty", got)
	}
}

func TestProgressDetail_StoryProgress(t *testing.T) {
	evt := makeEvent(state.EventStoryProgress, map[string]any{
		"phase": "thinking", "iteration": float64(1), "max_iter": float64(5),
	})
	got := progressDetail(evt, 40)
	if !strings.Contains(got, "thinking") {
		t.Errorf("progressDetail STORY_PROGRESS = %q, want 'thinking'", got)
	}
}

func TestProgressDetail_StoryCompleted_WithSummary(t *testing.T) {
	evt := makeEvent(state.EventStoryCompleted, map[string]any{"summary": "All done"})
	got := progressDetail(evt, 40)
	if !strings.Contains(got, "All done") {
		t.Errorf("progressDetail STORY_COMPLETED = %q, want 'All done'", got)
	}
}

func TestProgressDetail_StoryCompleted_EmptySummary(t *testing.T) {
	evt := makeEvent(state.EventStoryCompleted, map[string]any{"summary": ""})
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail STORY_COMPLETED empty summary = %q, want empty", got)
	}
}

func TestProgressDetail_StoryCompleted_NoSummaryKey(t *testing.T) {
	evt := makeEvent(state.EventStoryCompleted, map[string]any{"other": "value"})
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail STORY_COMPLETED no summary key = %q, want empty", got)
	}
}

func TestProgressDetail_StoryReviewFailed_WithFeedback(t *testing.T) {
	evt := makeEvent(state.EventStoryReviewFailed, map[string]any{"feedback": "Missing tests"})
	got := progressDetail(evt, 40)
	if !strings.Contains(got, "Missing tests") {
		t.Errorf("progressDetail STORY_REVIEW_FAILED = %q, want 'Missing tests'", got)
	}
}

func TestProgressDetail_StoryReviewFailed_EmptyFeedback(t *testing.T) {
	evt := makeEvent(state.EventStoryReviewFailed, map[string]any{"feedback": ""})
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail STORY_REVIEW_FAILED empty feedback = %q, want empty", got)
	}
}

func TestProgressDetail_DefaultEventType(t *testing.T) {
	evt := makeEvent("AGENT_SPAWNED", map[string]any{"key": "val"})
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail default event type = %q, want empty", got)
	}
}

func TestProgressDetail_StoryProgress_EmptyDetail(t *testing.T) {
	// phase with no visible output returns empty from formatProgressDetail → progressDetail returns ""
	evt := makeEvent(state.EventStoryProgress, map[string]any{"phase": "unknown_phase"})
	got := progressDetail(evt, 40)
	if got != "" {
		t.Errorf("progressDetail STORY_PROGRESS empty detail = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// renderPausedBanner
// ---------------------------------------------------------------------------

func TestRenderPausedBanner_NoPaused(t *testing.T) {
	m := Model{
		requirements: []state.Requirement{
			{ID: "r-001", Title: "Feature A", Status: "active"},
		},
	}
	got := m.renderPausedBanner(80)
	if got != "" {
		t.Errorf("renderPausedBanner with no paused reqs = %q, want empty", got)
	}
}

func TestRenderPausedBanner_WithPaused(t *testing.T) {
	m := Model{
		requirements: []state.Requirement{
			{ID: "r-001", Title: "Feature A", Status: "paused"},
			{ID: "r-002", Title: "Feature B", Status: "active"},
		},
	}
	got := m.renderPausedBanner(80)
	if !strings.Contains(got, "PAUSED") {
		t.Errorf("renderPausedBanner = %q, want 'PAUSED'", got)
	}
	if !strings.Contains(got, "r-001") {
		t.Errorf("renderPausedBanner = %q, want req ID 'r-001'", got)
	}
}

func TestRenderPausedBanner_LongID_Truncated(t *testing.T) {
	m := Model{
		requirements: []state.Requirement{
			{ID: "req-very-long-id-1234", Title: "My Feature", Status: "paused"},
		},
	}
	got := m.renderPausedBanner(80)
	if !strings.Contains(got, "PAUSED") {
		t.Errorf("renderPausedBanner long ID = %q, want 'PAUSED'", got)
	}
	// ID should be truncated to 8 characters
	if strings.Contains(got, "req-very-long-id-1234") {
		t.Errorf("renderPausedBanner should truncate ID longer than 8 chars")
	}
	if !strings.Contains(got, "req-very") {
		t.Errorf("renderPausedBanner should show first 8 chars of ID, got %q", got)
	}
}

func TestRenderPausedBanner_MultiplePaused(t *testing.T) {
	m := Model{
		requirements: []state.Requirement{
			{ID: "r-001", Title: "Alpha", Status: "paused"},
			{ID: "r-002", Title: "Beta", Status: "paused"},
		},
	}
	got := m.renderPausedBanner(120)
	if !strings.Contains(got, "r-001") || !strings.Contains(got, "r-002") {
		t.Errorf("renderPausedBanner multiple paused = %q, want both IDs", got)
	}
}

// ---------------------------------------------------------------------------
// handleKey — uncovered branches
// ---------------------------------------------------------------------------

func TestHandleKey_QuitKey(t *testing.T) {
	m := Model{version: "1.0.0"}
	_, cmd := m.handleKey(newKeyMsg('q'))
	if cmd == nil {
		t.Error("'q' should return tea.Quit command")
	}
}

func TestHandleKey_WebKey(t *testing.T) {
	m := Model{version: "1.0.0"}
	updated, cmd := m.handleKey(newKeyMsg('w'))
	_ = updated
	// 'w' is a no-op in TUI mode, no command returned
	if cmd != nil {
		t.Error("'w' should be a no-op and return nil command")
	}
}

func TestHandleKey_CtrlC(t *testing.T) {
	m := Model{version: "1.0.0"}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("Ctrl+C should return tea.Quit command")
	}
}

func TestHandleKey_UnknownKey(t *testing.T) {
	m := Model{version: "1.0.0", storyScrollOffset: 3}
	updated, cmd := m.handleKey(newKeyMsg('x'))
	m2 := updated.(Model)
	if cmd != nil {
		t.Error("unknown key should return nil command")
	}
	// scroll offset should be unchanged
	if m2.storyScrollOffset != 3 {
		t.Errorf("unknown key should not change scroll offset, got %d", m2.storyScrollOffset)
	}
}

func TestHandleKey_EmptyRunes(t *testing.T) {
	m := Model{version: "1.0.0"}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{}})
	if cmd != nil {
		t.Error("empty runes should return nil command")
	}
}

func TestHandleKey_NonRuneKeyType(t *testing.T) {
	m := Model{version: "1.0.0"}
	// Use a key type that is not KeyCtrlC and not KeyRunes
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("unhandled key type should return nil command")
	}
}

// ---------------------------------------------------------------------------
// renderStatusBar — uncovered branches
// ---------------------------------------------------------------------------

func TestRenderStatusBar_WithLastRefresh(t *testing.T) {
	m := Model{
		version:     "1.0.0",
		width:       80,
		lastRefresh: time.Now(),
	}
	output := m.renderStatusBar()
	if !strings.Contains(output, "Last refresh:") {
		t.Errorf("renderStatusBar with lastRefresh = %q, want 'Last refresh:'", output)
	}
}

func TestRenderStatusBar_WithError(t *testing.T) {
	m := Model{
		version: "1.0.0",
		width:   80,
		err:     fmt.Errorf("db connection failed"),
	}
	output := m.renderStatusBar()
	if !strings.Contains(output, "ERR:") {
		t.Errorf("renderStatusBar with error = %q, want 'ERR:'", output)
	}
	if !strings.Contains(output, "db connection failed") {
		t.Errorf("renderStatusBar with error = %q, want error message", output)
	}
}

func TestRenderStatusBar_NoRefreshNoError(t *testing.T) {
	m := Model{version: "1.0.0", width: 80}
	output := m.renderStatusBar()
	if strings.Contains(output, "Last refresh:") {
		t.Errorf("renderStatusBar no refresh = %q, should not contain 'Last refresh:'", output)
	}
	if strings.Contains(output, "ERR:") {
		t.Errorf("renderStatusBar no error = %q, should not contain 'ERR:'", output)
	}
}

// ---------------------------------------------------------------------------
// countByStatus and renderPipeline helpers
// ---------------------------------------------------------------------------

func TestCountByStatus_Empty(t *testing.T) {
	m := Model{}
	buckets := m.countByStatus()
	if len(buckets) != 0 {
		t.Errorf("countByStatus empty model = %v, want empty map", buckets)
	}
}

func TestCountByStatus_MixedStatuses(t *testing.T) {
	m := Model{
		stories: []state.Story{
			{Status: "in_progress"},
			{Status: "in_progress"},
			{Status: "merged"},
			{Status: "review"},
			{Status: "split"},
		},
	}
	buckets := m.countByStatus()
	if buckets["in_progress"] != 2 {
		t.Errorf("countByStatus in_progress = %d, want 2", buckets["in_progress"])
	}
	if buckets["merged"] != 1 {
		t.Errorf("countByStatus merged = %d, want 1", buckets["merged"])
	}
	if buckets["split"] != 1 {
		t.Errorf("countByStatus split = %d, want 1", buckets["split"])
	}
}

// ---------------------------------------------------------------------------
// reverseEvents
// ---------------------------------------------------------------------------

func TestReverseEvents_Empty(t *testing.T) {
	result := reverseEvents([]state.Event{})
	if len(result) != 0 {
		t.Errorf("reverseEvents empty = %v, want empty", result)
	}
}

func TestReverseEvents_Single(t *testing.T) {
	evt := state.Event{Type: "TEST"}
	result := reverseEvents([]state.Event{evt})
	if len(result) != 1 || result[0].Type != "TEST" {
		t.Errorf("reverseEvents single = %v, want [TEST]", result)
	}
}

func TestReverseEvents_Multiple(t *testing.T) {
	events := []state.Event{
		{Type: "FIRST"},
		{Type: "SECOND"},
		{Type: "THIRD"},
	}
	result := reverseEvents(events)
	if result[0].Type != "THIRD" || result[1].Type != "SECOND" || result[2].Type != "FIRST" {
		t.Errorf("reverseEvents multiple = %v, want reversed order", result)
	}
}

func TestReverseEvents_Immutable(t *testing.T) {
	events := []state.Event{{Type: "A"}, {Type: "B"}}
	_ = reverseEvents(events)
	// original should be unchanged
	if events[0].Type != "A" {
		t.Error("reverseEvents mutated the original slice")
	}
}

// ---------------------------------------------------------------------------
// renderAgentSummary
// ---------------------------------------------------------------------------

func TestRenderAgentSummary_AllStatuses(t *testing.T) {
	agents := []state.Agent{
		{ID: "a1", Status: "active"},
		{ID: "a2", Status: "active"},
		{ID: "a3", Status: "idle"},
		{ID: "a4", Status: "stuck"},
		{ID: "a5", Status: "terminated"},
	}
	got := renderAgentSummary(agents)
	if !strings.Contains(got, "Active: 2") {
		t.Errorf("renderAgentSummary = %q, want 'Active: 2'", got)
	}
	if !strings.Contains(got, "Idle: 1") {
		t.Errorf("renderAgentSummary = %q, want 'Idle: 1'", got)
	}
	if !strings.Contains(got, "Stuck: 1") {
		t.Errorf("renderAgentSummary = %q, want 'Stuck: 1'", got)
	}
	if !strings.Contains(got, "Terminated: 1") {
		t.Errorf("renderAgentSummary = %q, want 'Terminated: 1'", got)
	}
}

func TestRenderAgentSummary_Empty(t *testing.T) {
	got := renderAgentSummary([]state.Agent{})
	if !strings.Contains(got, "Total: 0") {
		t.Errorf("renderAgentSummary empty = %q, want 'Total: 0'", got)
	}
}

// ---------------------------------------------------------------------------
// renderEscalations
// ---------------------------------------------------------------------------

func TestRenderEscalations_Empty(t *testing.T) {
	m := Model{}
	got := m.renderEscalations(80, 5)
	if !strings.Contains(got, "Escalations") {
		t.Errorf("renderEscalations empty = %q, want 'Escalations'", got)
	}
	if !strings.Contains(got, "No escalations") {
		t.Errorf("renderEscalations empty = %q, want 'No escalations'", got)
	}
}

func TestRenderEscalations_PendingAndResolved(t *testing.T) {
	m := Model{
		escalations: []state.Escalation{
			{StoryID: "story-1", FromAgent: "agent-1", Status: "pending", FromTier: 1, ToTier: 2, Reason: "stuck"},
			{StoryID: "story-2", FromAgent: "agent-2", Status: "resolved", FromTier: 2, ToTier: 3, Reason: "timeout"},
		},
	}
	got := m.renderEscalations(120, 5)
	if !strings.Contains(got, "[1 pending]") {
		t.Errorf("renderEscalations = %q, want '[1 pending]'", got)
	}
	if !strings.Contains(got, "story-1") {
		t.Errorf("renderEscalations = %q, want story-1", got)
	}
}

func TestRenderEscalations_NoPending(t *testing.T) {
	m := Model{
		escalations: []state.Escalation{
			{StoryID: "story-1", Status: "resolved", FromTier: 1, ToTier: 2},
		},
	}
	got := m.renderEscalations(80, 5)
	if strings.Contains(got, "pending") {
		t.Errorf("renderEscalations with no pending = %q, should not contain 'pending'", got)
	}
}

func TestRenderEscalations_EmptyStoryID(t *testing.T) {
	m := Model{
		escalations: []state.Escalation{
			{StoryID: "", FromAgent: "agent-1", Status: "pending", FromTier: 1, ToTier: 2},
		},
	}
	got := m.renderEscalations(80, 5)
	if !strings.Contains(got, "-") {
		t.Errorf("renderEscalations empty storyID = %q, want '-' placeholder", got)
	}
}
