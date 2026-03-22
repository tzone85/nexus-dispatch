package dashboard

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// renderEscalations renders active and recent escalations in a compact,
// fixed-height layout. When empty it collapses to a single line. When pending
// escalations exist it renders up to maxRows rows without column headers.
func (m Model) renderEscalations(width, maxRows int) string {
	escalations := m.escalations
	if len(escalations) == 0 {
		return headingStyle.Render("─ Escalations ") +
			lipgloss.NewStyle().Foreground(colorGray).Render(" No escalations")
	}

	pending := 0
	for _, e := range escalations {
		if e.Status == "pending" {
			pending++
		}
	}

	header := headingStyle.Render("─ Escalations ")
	if pending > 0 {
		header += escalationPendingStyle.Render(fmt.Sprintf("[%d pending]", pending))
	}

	// Compact rows — no column headers, just data.
	var rows []string
	rowLimit := maxRows - 1 // 1 row for header
	if rowLimit < 1 {
		rowLimit = 1
	}

	for i, e := range escalations {
		if i >= rowLimit {
			break
		}

		storyID := e.StoryID
		if storyID == "" {
			storyID = "-"
		}
		tier := fmt.Sprintf("T%d→%d", e.FromTier, e.ToTier)

		var statusStyle lipgloss.Style
		if e.Status == "pending" {
			statusStyle = escalationPendingStyle
		} else {
			statusStyle = escalationResolvedStyle
		}

		reasonWidth := width - 70
		if reasonWidth < 10 {
			reasonWidth = 10
		}

		row := fmt.Sprintf("  %s  %s  %s  %s  %s",
			truncateStr(storyID, 20),
			truncateStr(e.FromAgent, 12),
			tier,
			statusStyle.Render(e.Status),
			truncateStr(e.Reason, reasonWidth),
		)
		rows = append(rows, row)
	}

	return lipgloss.JoinVertical(lipgloss.Left, append([]string{header}, rows...)...)
}
