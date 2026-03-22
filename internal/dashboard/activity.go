package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

const maxActivityEvents = 30

// renderActivity renders the recent event activity feed.
func (m Model) renderActivity(width, maxRows int) string {
	heading := headingStyle.Render("─ Activity ")
	events := m.events
	if len(events) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			heading,
			lipgloss.NewStyle().Foreground(colorGray).Render("  No events recorded yet."),
		)
	}

	// Show events in reverse chronological order (newest first).
	reversed := reverseEvents(events)

	// Column widths.
	colTime := 20
	colType := 28
	colAgent := 14
	colStory := max(width-colTime-colType-colAgent-12, 10)

	header := fmt.Sprintf("  %-*s %-*s %-*s %-*s",
		colTime, "TIMESTAMP",
		colType, "EVENT",
		colAgent, "AGENT",
		colStory, "STORY",
	)

	separator := fmt.Sprintf("  %-*s %-*s %-*s %-*s",
		colTime, strings.Repeat("─", colTime-1),
		colType, strings.Repeat("─", colType-1),
		colAgent, strings.Repeat("─", colAgent-1),
		colStory, strings.Repeat("─", colStory-1),
	)

	var lines []string
	lines = append(lines, columnHeaderStyle.Render(header))
	lines = append(lines, lipgloss.NewStyle().Foreground(colorDimGray).Render(separator))

	rowLimit := maxRows - 4
	if rowLimit < 1 {
		rowLimit = 10
	}

	for i, evt := range reversed {
		if i >= rowLimit {
			remaining := len(reversed) - rowLimit
			if remaining > 0 {
				lines = append(lines, lipgloss.NewStyle().Foreground(colorGray).Render(
					fmt.Sprintf("  ... and %d more events", remaining),
				))
			}
			break
		}

		timestamp := evt.Timestamp.Format("15:04:05 2006-01-02")
		eventType := string(evt.Type)
		agentID := evt.AgentID
		if agentID == "" {
			agentID = "-"
		}
		storyID := evt.StoryID
		if storyID == "" {
			storyID = "-"
		}

		style := eventCategoryStyle(eventType)

		row := fmt.Sprintf("  %-*s %s %-*s %-*s",
			colTime, timestamp,
			style.Render(fmt.Sprintf("%-*s", colType, truncateStr(eventType, colType-1))),
			colAgent, truncateStr(agentID, colAgent-1),
			colStory, truncateStr(storyID, colStory-1),
		)

		lines = append(lines, row)
	}

	return lipgloss.JoinVertical(lipgloss.Left, heading, strings.Join(lines, "\n"))
}

// reverseEvents returns a new slice with events in reverse order.
func reverseEvents(events []state.Event) []state.Event {
	n := len(events)
	result := make([]state.Event, n)
	for i, evt := range events {
		result[n-1-i] = evt
	}
	return result
}
