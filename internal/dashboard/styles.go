package dashboard

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette for the dashboard.
const (
	colorWhite   = lipgloss.Color("#FFFFFF")
	colorGray    = lipgloss.Color("#888888")
	colorDimGray = lipgloss.Color("#555555")
	colorYellow  = lipgloss.Color("#FFCC00")
	colorGreen   = lipgloss.Color("#00CC66")
	colorBlue    = lipgloss.Color("#3399FF")
	colorCyan    = lipgloss.Color("#00CCCC")
	colorRed     = lipgloss.Color("#FF4444")
	colorMagenta = lipgloss.Color("#CC66FF")
	colorOrange  = lipgloss.Color("#FF9933")

	colorBgDark      = lipgloss.Color("#1A1A2E")
	colorBgPanel     = lipgloss.Color("#16213E")
	colorBgActiveTab = lipgloss.Color("#0F3460")
	colorBgStatusBar = lipgloss.Color("#0A0A1A")
)

var (
	// Tab styles.
	tabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorGray)

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorWhite).
			Background(colorBgActiveTab).
			Bold(true)

	// Panel container with a border.
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDimGray).
			Padding(1, 2)

	// Status bar at the bottom.
	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorGray).
			Background(colorBgStatusBar).
			Padding(0, 1)

	// Heading within panels.
	headingStyle = lipgloss.NewStyle().
			Foreground(colorWhite).
			Bold(true).
			MarginBottom(1)

	// Column header in tables.
	columnHeaderStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	// Story status colors.
	statusPlannedStyle    = lipgloss.NewStyle().Foreground(colorGray)
	statusAssignedStyle   = lipgloss.NewStyle().Foreground(colorMagenta)
	statusProgressStyle   = lipgloss.NewStyle().Foreground(colorYellow)
	statusReviewStyle     = lipgloss.NewStyle().Foreground(colorBlue)
	statusQAStyle         = lipgloss.NewStyle().Foreground(colorCyan)
	statusMergedStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	statusPausedStyle     = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
	statusDefaultStyle    = lipgloss.NewStyle().Foreground(colorWhite)

	// Agent status colors.
	agentActiveStyle = lipgloss.NewStyle().Foreground(colorGreen)
	agentStuckStyle  = lipgloss.NewStyle().Foreground(colorRed)
	agentIdleStyle   = lipgloss.NewStyle().Foreground(colorGray)

	// Event category colors.
	eventReqStyle        = lipgloss.NewStyle().Foreground(colorCyan)
	eventStoryStyle      = lipgloss.NewStyle().Foreground(colorBlue)
	eventAgentStyle      = lipgloss.NewStyle().Foreground(colorYellow)
	eventEscalationStyle = lipgloss.NewStyle().Foreground(colorRed)
	eventDefaultStyle    = lipgloss.NewStyle().Foreground(colorGray)

	// Escalation styles.
	escalationPendingStyle  = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	escalationResolvedStyle = lipgloss.NewStyle().Foreground(colorGreen)

	// Complexity badge.
	complexityStyle = lipgloss.NewStyle().
			Foreground(colorOrange).
			Bold(true)
)

// storyStatusStyle returns the lipgloss style for a given story status string.
func storyStatusStyle(status string) lipgloss.Style {
	switch status {
	case "draft", "planned", "estimated":
		return statusPlannedStyle
	case "assigned":
		return statusAssignedStyle
	case "in_progress":
		return statusProgressStyle
	case "review":
		return statusReviewStyle
	case "qa", "qa_failed":
		return statusQAStyle
	case "pr_submitted", "merged":
		return statusMergedStyle
	case "paused":
		return statusPausedStyle
	default:
		return statusDefaultStyle
	}
}

// agentStatusStyle returns the lipgloss style for a given agent status.
func agentStatusStyle(status string) lipgloss.Style {
	switch status {
	case "active":
		return agentActiveStyle
	case "stuck":
		return agentStuckStyle
	case "idle", "terminated":
		return agentIdleStyle
	default:
		return statusDefaultStyle
	}
}

// eventCategoryStyle returns the lipgloss style based on event type prefix.
func eventCategoryStyle(eventType string) lipgloss.Style {
	switch {
	case strings.HasPrefix(eventType, "REQ"):
		return eventReqStyle
	case strings.HasPrefix(eventType, "STORY"):
		return eventStoryStyle
	case strings.HasPrefix(eventType, "AGENT"):
		return eventAgentStyle
	case strings.HasPrefix(eventType, "ESCALATION"):
		return eventEscalationStyle
	case strings.HasPrefix(eventType, "SUPERVISOR"):
		return eventEscalationStyle
	default:
		return eventDefaultStyle
	}
}
