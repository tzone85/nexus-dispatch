package dashboard

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newKeyMsg constructs a tea.KeyMsg for a single printable rune.
func newKeyMsg(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestView_LoadingState verifies that View() returns "Loading..." before dimensions are set.
func TestView_LoadingState(t *testing.T) {
	m := Model{version: "test"}
	output := m.View()
	if output != "Loading..." {
		t.Errorf("View() with zero width = %q, want %q", output, "Loading...")
	}
}

// TestView_AllSectionsPresent verifies that all required sections appear in the output.
func TestView_AllSectionsPresent(t *testing.T) {
	m := Model{
		version: "1.0.0",
		width:   120,
		height:  40,
		agents: []state.Agent{
			{ID: "agent-1", Type: "coder", Model: "qwen2.5-coder:14b", Status: "active", CurrentStoryID: "story-1"},
		},
		stories: []state.Story{
			{ID: "story-1", Title: "Test story", Status: "in_progress", ReqID: "req-1", Complexity: 2},
		},
		events: []state.Event{
			{Type: "STORY_CREATED", AgentID: "agent-1", StoryID: "story-1", Timestamp: time.Now()},
		},
		escalations: []state.Escalation{
			{StoryID: "story-1", FromAgent: "agent-1", Status: "pending", FromTier: 1, ToTier: 2, Reason: "stuck"},
		},
		lastRefresh: time.Now(),
	}

	output := m.View()

	for _, section := range []string{"Agents", "Pipeline", "Stories", "Activity", "Escalations"} {
		if !strings.Contains(output, section) {
			t.Errorf("View() missing section: %s", section)
		}
	}
}

// TestView_StatusBarKeyHints verifies that the new key hints appear in the status bar.
func TestView_StatusBarKeyHints(t *testing.T) {
	m := Model{version: "1.0.0", width: 120, height: 40}
	output := m.View()

	for _, hint := range []string{"j/k:scroll", "w:web", "q:quit"} {
		if !strings.Contains(output, hint) {
			t.Errorf("renderStatusBar() missing key hint: %s", hint)
		}
	}
}

// TestView_NoTabLabels verifies that old tab-related labels no longer appear.
func TestView_NoTabLabels(t *testing.T) {
	m := Model{version: "1.0.0", width: 120, height: 40}
	output := m.View()

	for _, old := range []string{"1:Pipeline", "2:Agents", "3:Activity", "4:Escalations", "Tab:next"} {
		if strings.Contains(output, old) {
			t.Errorf("View() should not contain old tab label: %s", old)
		}
	}
}

// TestPendingEscalations verifies that pendingEscalations() counts correctly.
func TestPendingEscalations(t *testing.T) {
	m := Model{
		escalations: []state.Escalation{
			{Status: "pending"},
			{Status: "pending"},
			{Status: "resolved"},
		},
	}
	if got := m.pendingEscalations(); got != 2 {
		t.Errorf("pendingEscalations() = %d, want 2", got)
	}
}

// TestPendingEscalations_Empty verifies zero count when no escalations.
func TestPendingEscalations_Empty(t *testing.T) {
	m := Model{}
	if got := m.pendingEscalations(); got != 0 {
		t.Errorf("pendingEscalations() on empty model = %d, want 0", got)
	}
}

// TestHandleKey_ScrollDown verifies 'j' increments storyScrollOffset.
func TestHandleKey_ScrollDown(t *testing.T) {
	m := Model{version: "1.0.0", width: 120, height: 40}

	updated, _ := m.handleKey(newKeyMsg('j'))
	m2 := updated.(Model)
	if m2.storyScrollOffset != 1 {
		t.Errorf("after 'j', storyScrollOffset = %d, want 1", m2.storyScrollOffset)
	}

	updated2, _ := m2.handleKey(newKeyMsg('j'))
	m3 := updated2.(Model)
	if m3.storyScrollOffset != 2 {
		t.Errorf("after 2x 'j', storyScrollOffset = %d, want 2", m3.storyScrollOffset)
	}
}

// TestHandleKey_ScrollUp verifies 'k' decrements storyScrollOffset but not below 0.
func TestHandleKey_ScrollUp(t *testing.T) {
	m := Model{storyScrollOffset: 2}

	updated, _ := m.handleKey(newKeyMsg('k'))
	m2 := updated.(Model)
	if m2.storyScrollOffset != 1 {
		t.Errorf("after 'k' from 2, storyScrollOffset = %d, want 1", m2.storyScrollOffset)
	}
}

// TestHandleKey_ScrollFloor verifies 'k' does not go below 0.
func TestHandleKey_ScrollFloor(t *testing.T) {
	m := Model{storyScrollOffset: 0}

	updated, _ := m.handleKey(newKeyMsg('k'))
	m2 := updated.(Model)
	if m2.storyScrollOffset != 0 {
		t.Errorf("'k' at offset 0 should stay 0, got %d", m2.storyScrollOffset)
	}
}

// TestApplyData_ImmutableUpdate verifies applyData returns a new Model without mutating the receiver.
func TestApplyData_ImmutableUpdate(t *testing.T) {
	original := Model{
		version:           "1.0.0",
		width:             100,
		height:            30,
		storyScrollOffset: 2,
	}

	d := dataMsg{
		agents:  []state.Agent{{ID: "agent-1", Status: "active"}},
		stories: []state.Story{{ID: "s-1"}, {ID: "s-2"}, {ID: "s-3"}, {ID: "s-4"}, {ID: "s-5"}},
	}

	updated := original.applyData(d)

	// Original must not be mutated.
	if len(original.agents) != 0 {
		t.Errorf("applyData mutated original model agents")
	}
	// Updated model should have the new data.
	if len(updated.agents) != 1 {
		t.Errorf("updated model agents len = %d, want 1", len(updated.agents))
	}
	// Scroll offset survives when still within range (2 < 5 stories).
	if updated.storyScrollOffset != 2 {
		t.Errorf("applyData did not preserve storyScrollOffset: got %d, want 2", updated.storyScrollOffset)
	}
	if updated.width != 100 {
		t.Errorf("applyData did not preserve width: got %d, want 100", updated.width)
	}
}

// TestApplyData_ClampsScrollOffset verifies L6: when stories shrink below the
// scroll offset, the offset clamps to 0 instead of pointing past the end.
func TestApplyData_ClampsScrollOffset(t *testing.T) {
	original := Model{storyScrollOffset: 10}
	d := dataMsg{
		stories: []state.Story{{ID: "s-1"}}, // only one story now
	}
	updated := original.applyData(d)
	if updated.storyScrollOffset != 0 {
		t.Errorf("expected scroll clamped to 0, got %d", updated.storyScrollOffset)
	}
}

// TestRenderHeader verifies the header contains the dashboard title and version.
func TestRenderHeader(t *testing.T) {
	m := Model{version: "2.3.4"}
	output := m.renderHeader()

	if !strings.Contains(output, "2.3.4") {
		t.Errorf("renderHeader() does not contain version: %s", output)
	}
	if !strings.Contains(output, "NXD DASHBOARD") {
		t.Errorf("renderHeader() does not contain 'NXD DASHBOARD': %s", output)
	}
}

// TestRenderStatusBar_Format verifies the status bar structure.
func TestRenderStatusBar_Format(t *testing.T) {
	m := Model{version: "0.1.0", width: 80}
	output := m.renderStatusBar()

	if !strings.Contains(output, "NXD v0.1.0") {
		t.Errorf("renderStatusBar() missing version info: %s", output)
	}
	if !strings.Contains(output, "j/k:scroll") {
		t.Errorf("renderStatusBar() missing scroll hint: %s", output)
	}
}
