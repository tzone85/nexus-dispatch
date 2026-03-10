package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

const (
	refreshInterval = 2 * time.Second
	panelCount      = 4
)

// Panel index constants.
const (
	panelPipeline    = 0
	panelAgents      = 1
	panelActivity    = 2
	panelEscalations = 3
)

// panelNames maps panel index to display name.
var panelNames = []string{
	"Pipeline",
	"Agents",
	"Activity",
	"Escalations",
}

// tickMsg triggers periodic data refresh.
type tickMsg time.Time

// dataMsg carries refreshed data from the stores.
type dataMsg struct {
	stories     []state.Story
	agents      []state.Agent
	events      []state.Event
	escalations []state.Escalation
	err         error
}

// Model is the top-level Bubbletea model for the NXD dashboard.
type Model struct {
	eventStore state.EventStore
	projStore  *state.SQLiteStore
	version    string

	activePanel int
	width       int
	height      int

	// Cached data from last refresh.
	stories     []state.Story
	agents      []state.Agent
	events      []state.Event
	escalations []state.Escalation
	lastRefresh time.Time
	err         error
}

// New creates a new dashboard Model with the given stores and version string.
func New(es state.EventStore, ps *state.SQLiteStore, version string) Model {
	return Model{
		eventStore:  es,
		projStore:   ps,
		version:     version,
		activePanel: panelPipeline,
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

// View implements tea.Model. It renders the complete dashboard UI.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading dashboard..."
	}

	tabs := m.renderTabs()
	statusBar := m.renderStatusBar()

	// Available height for the panel content.
	panelHeight := m.height - 4 // tabs (1) + status bar (1) + margins (2)
	panelWidth := m.width - 2   // small margin

	var content string
	switch m.activePanel {
	case panelPipeline:
		content = renderPipeline(m.stories, panelWidth, panelHeight)
	case panelAgents:
		content = renderAgents(m.agents, panelWidth, panelHeight)
	case panelActivity:
		content = renderActivity(m.events, panelWidth, panelHeight)
	case panelEscalations:
		content = renderEscalations(m.escalations, panelWidth, panelHeight)
	}

	// Wrap content in the panel style, sized to fill available space.
	panel := panelStyle.
		Width(panelWidth).
		Height(panelHeight).
		Render(content)

	return lipgloss.JoinVertical(lipgloss.Left, tabs, panel, statusBar)
}

// handleKey processes keyboard input for navigation and quitting.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyTab:
		m.activePanel = (m.activePanel + 1) % panelCount
		return m, nil

	case tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return m, nil
		}
		switch msg.Runes[0] {
		case 'q':
			return m, tea.Quit
		case '1':
			m.activePanel = panelPipeline
		case '2':
			m.activePanel = panelAgents
		case '3':
			m.activePanel = panelActivity
		case '4':
			m.activePanel = panelEscalations
		}
		return m, nil
	}

	return m, nil
}

// renderTabs renders the tab bar showing all panels with the active one highlighted.
func (m Model) renderTabs() string {
	var tabs []string
	for i, name := range panelNames {
		label := fmt.Sprintf(" %d:%s ", i+1, name)
		if i == m.activePanel {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
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

	right := "1-4:panels  Tab:next  q:quit "

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
	return func() tea.Msg {
		var d dataMsg

		stories, err := ps.ListStories(state.StoryFilter{})
		if err != nil {
			d.err = fmt.Errorf("list stories: %w", err)
			return d
		}
		d.stories = stories

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

// applyData updates the model with freshly fetched data.
func (m Model) applyData(d dataMsg) Model {
	return Model{
		eventStore:  m.eventStore,
		projStore:   m.projStore,
		version:     m.version,
		activePanel: m.activePanel,
		width:       m.width,
		height:      m.height,
		stories:     d.stories,
		agents:      d.agents,
		events:      d.events,
		escalations: d.escalations,
		lastRefresh: time.Now(),
		err:         d.err,
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

