package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// renderAgents renders the agent status list with summary.
func (m Model) renderAgents(width, maxRows int) string {
	heading := headingStyle.Render("Agents")
	summary := renderAgentSummary(m.agents)
	table := renderAgentTable(m.agents, width, maxRows-4)
	return lipgloss.JoinVertical(lipgloss.Left, heading, summary, "", table)
}

// renderAgentSummary renders a one-line count of agents by status.
func renderAgentSummary(agents []state.Agent) string {
	counts := make(map[string]int)
	for _, a := range agents {
		counts[a.Status]++
	}

	parts := []string{
		fmt.Sprintf("Total: %d", len(agents)),
	}

	if n := counts["active"]; n > 0 {
		parts = append(parts, agentActiveStyle.Render(fmt.Sprintf("Active: %d", n)))
	}
	if n := counts["idle"]; n > 0 {
		parts = append(parts, agentIdleStyle.Render(fmt.Sprintf("Idle: %d", n)))
	}
	if n := counts["stuck"]; n > 0 {
		parts = append(parts, agentStuckStyle.Render(fmt.Sprintf("Stuck: %d", n)))
	}
	if n := counts["terminated"]; n > 0 {
		parts = append(parts, agentIdleStyle.Render(fmt.Sprintf("Terminated: %d", n)))
	}

	return headingStyle.Render(strings.Join(parts, "  |  "))
}

// renderAgentTable renders the agents as a table with columns.
func renderAgentTable(agents []state.Agent, width, maxRows int) string {
	// Column widths.
	colID := 16
	colRole := 12
	colModel := 14
	colStatus := 10
	colStory := 14
	colSession := max(width-colID-colRole-colModel-colStatus-colStory-12, 10)

	header := fmt.Sprintf("  %-*s %-*s %-*s %-*s %-*s %-*s",
		colID, "ID",
		colRole, "ROLE",
		colModel, "MODEL",
		colStatus, "STATUS",
		colStory, "STORY",
		colSession, "SESSION",
	)

	separator := fmt.Sprintf("  %-*s %-*s %-*s %-*s %-*s %-*s",
		colID, strings.Repeat("─", colID-1),
		colRole, strings.Repeat("─", colRole-1),
		colModel, strings.Repeat("─", colModel-1),
		colStatus, strings.Repeat("─", colStatus-1),
		colStory, strings.Repeat("─", colStory-1),
		colSession, strings.Repeat("─", colSession-1),
	)

	var lines []string
	lines = append(lines, columnHeaderStyle.Render(header))
	lines = append(lines, lipgloss.NewStyle().Foreground(colorDimGray).Render(separator))

	for i, a := range agents {
		if maxRows > 0 && i >= maxRows {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorGray).Render(
				fmt.Sprintf("  ... and %d more", len(agents)-maxRows),
			))
			break
		}

		storyID := a.CurrentStoryID
		if storyID == "" {
			storyID = "-"
		}
		session := a.SessionName
		if session == "" {
			session = "-"
		}

		statusRendered := agentStatusStyle(a.Status).Render(fmt.Sprintf("%-*s", colStatus, a.Status))

		row := fmt.Sprintf("  %-*s %-*s %-*s %s %-*s %-*s",
			colID, truncateStr(a.ID, colID-1),
			colRole, truncateStr(a.Type, colRole-1),
			colModel, truncateStr(a.Model, colModel-1),
			statusRendered,
			colStory, truncateStr(storyID, colStory-1),
			colSession, truncateStr(session, colSession-1),
		)

		lines = append(lines, row)
	}

	if len(agents) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorGray).Render("  No agents found."))
	}

	return strings.Join(lines, "\n")
}
