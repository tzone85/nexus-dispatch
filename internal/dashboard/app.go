package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

const refreshInterval = 2 * time.Second

// tickMsg triggers periodic data refresh.
type tickMsg time.Time

// dataMsg carries refreshed data from the stores.
type dataMsg struct {
	requirements []state.Requirement
	stories      []state.Story
	agents       []state.Agent
	events       []state.Event
	escalations  []state.Escalation
	err          error
}

// Model is the top-level Bubbletea model for the NXD dashboard.
type Model struct {
	eventStore state.EventStore
	projStore  *state.SQLiteStore
	version    string
	reqFilter  state.ReqFilter

	storyScrollOffset int
	width             int
	height            int

	// Cached data from last refresh.
	requirements []state.Requirement
	stories      []state.Story
	agents       []state.Agent
	events       []state.Event
	escalations  []state.Escalation
	lastRefresh  time.Time
	err          error
}

// New creates a new dashboard Model with the given stores, version string,
// and requirement filter for workspace scoping.
func New(es state.EventStore, ps *state.SQLiteStore, version string, filter state.ReqFilter) Model {
	return Model{
		eventStore: es,
		projStore:  ps,
		version:    version,
		reqFilter:  filter,
	}
}

// Init implements tea.Model. It triggers the first data fetch and starts the tick timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchData(),
		tickCmd(),
	)
}

// Update implements tea.Model. It handles key presses, window resizes, and data refresh ticks.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchData(), tickCmd())

	case dataMsg:
		return m.applyData(msg), nil
	}

	return m, nil
}

// View implements tea.Model. It renders the complete dashboard UI with all sections stacked.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	availHeight := m.height - 2 // status bar + top border

	// Fixed-height sections
	agentRows := len(m.agents) + 2 // header + rows + border
	pipelineRows := 3               // summary + progress bar + border
	escalationRows := 2             // summary line + border (or more if pending)
	if m.pendingEscalations() > 0 {
		escalationRows = min(m.pendingEscalations()+2, 5)
	}

	fixedRows := agentRows + pipelineRows + escalationRows
	remainingRows := availHeight - fixedRows
	storyRows := remainingRows * 2 / 3
	activityRows := remainingRows - storyRows

	sections := []string{
		m.renderHeader(),
		m.renderAgents(m.width, agentRows),
		m.renderPipeline(m.width),
		m.renderStories(m.width, storyRows),
		m.renderActivity(m.width, activityRows),
		m.renderEscalations(m.width, escalationRows),
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...) + "\n" + m.renderStatusBar()
}

// renderHeader renders the dashboard title bar.
func (m Model) renderHeader() string {
	title := headerStyle.Render("NXD DASHBOARD " + m.version)
	return title
}

// renderStories renders the stories table section with a scrollable list.
func (m Model) renderStories(width, maxRows int) string {
	header := headingStyle.Render("─ Stories ")
	colHeader := fmt.Sprintf("  %-20s %-16s %-4s %-4s %s",
		columnHeaderStyle.Render("ID"),
		columnHeaderStyle.Render("STATUS"),
		columnHeaderStyle.Render("C"),
		columnHeaderStyle.Render("T"),
		columnHeaderStyle.Render("TITLE"))

	var rows []string
	start := m.storyScrollOffset
	end := min(start+maxRows-3, len(m.stories))
	if end < start {
		end = start
	}
	for i := start; i < end; i++ {
		s := m.stories[i]
		statusStr := storyStatusStyle(s.Status).Render(s.Status)
		if s.EscalationTier > 0 {
			statusStr += fmt.Sprintf("|T%d", s.EscalationTier)
		}
		row := fmt.Sprintf("  %-20s %-16s [C%d] %-4d %s",
			truncateStr(s.ID, 20), statusStr, s.Complexity, s.EscalationTier,
			truncateStr(s.Title, width-60))
		rows = append(rows, row)
	}

	if len(m.stories) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorGray).Render("  No stories — run 'nxd plan' to create a requirement"))
	}

	scrollInfo := ""
	if len(m.stories) > maxRows-3 {
		scrollInfo = fmt.Sprintf(" (%d-%d of %d)", start+1, end, len(m.stories))
	}

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{header + scrollInfo, colHeader}, rows...)...)
}

// pendingEscalations returns the count of escalations with status "pending".
func (m Model) pendingEscalations() int {
	count := 0
	for _, e := range m.escalations {
		if e.Status == "pending" {
			count++
		}
	}
	return count
}

// handleKey processes keyboard input for navigation and quitting.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return m, nil
		}
		switch msg.Runes[0] {
		case 'q':
			return m, tea.Quit
		case 'j':
			m.storyScrollOffset++
			return m, nil
		case 'k':
			if m.storyScrollOffset > 0 {
				m.storyScrollOffset--
			}
			return m, nil
		case 'w':
			// Open browser — handled by caller via tea.ExecProcess or similar.
			// No-op in TUI mode; web mode is launched via --web flag.
			return m, nil
		}
		return m, nil
	}

	return m, nil
}

// renderStatusBar renders the bottom status bar with version and key hints.
func (m Model) renderStatusBar() string {
	left := fmt.Sprintf(" NXD v%s", m.version)

	refreshInfo := ""
	if !m.lastRefresh.IsZero() {
		refreshInfo = fmt.Sprintf("  Last refresh: %s", m.lastRefresh.Format("15:04:05"))
	}

	errInfo := ""
	if m.err != nil {
		errInfo = fmt.Sprintf("  ERR: %s", m.err.Error())
	}

	right := "j/k:scroll  w:web  q:quit "

	// Fill the status bar to full width.
	middle := refreshInfo + errInfo
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - lipgloss.Width(middle)
	if gap < 0 {
		gap = 0
	}

	bar := left + middle + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(bar)
}

// fetchData returns a Cmd that queries both stores and returns a dataMsg.
func (m Model) fetchData() tea.Cmd {
	es := m.eventStore
	ps := m.projStore
	filter := m.reqFilter
	return func() tea.Msg {
		var d dataMsg

		reqs, err := ps.ListRequirementsFiltered(filter)
		if err != nil {
			d.err = fmt.Errorf("list requirements: %w", err)
			return d
		}
		d.requirements = reqs

		// Build a set of requirement IDs to scope stories
		reqIDs := make(map[string]bool, len(reqs))
		for _, r := range reqs {
			reqIDs[r.ID] = true
		}

		allStories, err := ps.ListStories(state.StoryFilter{})
		if err != nil {
			d.err = fmt.Errorf("list stories: %w", err)
			return d
		}

		// Filter stories to only those belonging to visible requirements
		if len(reqIDs) > 0 {
			var filtered []state.Story
			for _, s := range allStories {
				if reqIDs[s.ReqID] {
					filtered = append(filtered, s)
				}
			}
			d.stories = filtered
		} else {
			d.stories = allStories
		}

		agents, err := ps.ListAgents(state.AgentFilter{})
		if err != nil {
			d.err = fmt.Errorf("list agents: %w", err)
			return d
		}
		d.agents = agents

		events, err := es.List(state.EventFilter{Limit: maxActivityEvents})
		if err != nil {
			d.err = fmt.Errorf("list events: %w", err)
			return d
		}
		d.events = events

		escalations, err := ps.ListEscalations()
		if err != nil {
			d.err = fmt.Errorf("list escalations: %w", err)
			return d
		}
		d.escalations = escalations

		return d
	}
}

// applyData updates the model with freshly fetched data using immutable update.
// L6: clamp storyScrollOffset against the new story count so a refresh that
// shrinks the list doesn't leave the offset pointing past the end.
func (m Model) applyData(d dataMsg) Model {
	scroll := m.storyScrollOffset
	if scroll >= len(d.stories) {
		scroll = 0
	}
	return Model{
		eventStore:        m.eventStore,
		projStore:         m.projStore,
		version:           m.version,
		reqFilter:         m.reqFilter,
		storyScrollOffset: scroll,
		width:             m.width,
		height:            m.height,
		requirements:      d.requirements,
		stories:           d.stories,
		agents:            d.agents,
		events:            d.events,
		escalations:       d.escalations,
		lastRefresh:       time.Now(),
		err:               d.err,
	}
}

// tickCmd returns a Cmd that sends a tickMsg after the refresh interval.
func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// truncateStr shortens a string, appending "..." if it exceeds maxLen.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
