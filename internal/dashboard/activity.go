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
	colType := 22
	colAgent := 14
	colStory := 18
	colDetail := max(width-colTime-colType-colAgent-colStory-14, 10)

	header := fmt.Sprintf("  %-*s %-*s %-*s %-*s %-*s",
		colTime, "TIMESTAMP",
		colType, "EVENT",
		colAgent, "AGENT",
		colStory, "STORY",
		colDetail, "DETAIL",
	)

	separator := fmt.Sprintf("  %-*s %-*s %-*s %-*s %-*s",
		colTime, strings.Repeat("─", colTime-1),
		colType, strings.Repeat("─", colType-1),
		colAgent, strings.Repeat("─", colAgent-1),
		colStory, strings.Repeat("─", colStory-1),
		colDetail, strings.Repeat("─", colDetail-1),
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

		detail := progressDetail(evt, colDetail)
		style := eventCategoryStyle(eventType)

		row := fmt.Sprintf("  %-*s %s %-*s %-*s %s",
			colTime, timestamp,
			style.Render(fmt.Sprintf("%-*s", colType, truncateStr(eventType, colType-1))),
			colAgent, truncateStr(agentID, colAgent-1),
			colStory, truncateStr(storyID, colStory-1),
			detail,
		)

		lines = append(lines, row)
	}

	return lipgloss.JoinVertical(lipgloss.Left, heading, strings.Join(lines, "\n"))
}

// progressDetail extracts a human-readable detail string from an event's
// payload. For STORY_PROGRESS events this shows iteration, tool, and file
// information. For other events, returns an empty string.
func progressDetail(evt state.Event, maxWidth int) string {
	payload := state.DecodePayload(evt.Payload)
	if len(payload) == 0 {
		return ""
	}

	var detail string

	switch evt.Type {
	case state.EventStoryProgress:
		detail = formatProgressDetail(payload)
	case state.EventStoryCompleted:
		if summary, ok := payload["summary"].(string); ok && summary != "" {
			detail = truncateStr(summary, maxWidth)
		}
	case state.EventStoryReviewFailed:
		if fb, ok := payload["feedback"].(string); ok && fb != "" {
			detail = truncateStr(fb, maxWidth)
		}
	default:
		return ""
	}

	if detail == "" {
		return ""
	}

	style := progressDetailStyle(payload)
	return style.Render(truncateStr(detail, maxWidth))
}

// formatProgressDetail builds a compact string from a STORY_PROGRESS payload.
func formatProgressDetail(payload map[string]any) string {
	iter, _ := payload["iteration"].(float64)
	maxIter, _ := payload["max_iter"].(float64)
	phase, _ := payload["phase"].(string)
	detail, _ := payload["detail"].(string)
	tool, _ := payload["tool"].(string)
	file, _ := payload["file"].(string)

	var prefix string
	if maxIter > 0 {
		prefix = fmt.Sprintf("[%d/%d] ", int(iter), int(maxIter))
	}

	switch phase {
	case "thinking":
		return prefix + "thinking..."
	case "tool_call":
		if file != "" {
			return prefix + tool + " " + file
		}
		if detail != "" {
			return prefix + detail
		}
		return prefix + tool
	case "tool_result":
		isErr, _ := payload["is_error"].(bool)
		status := "ok"
		if isErr {
			status = "FAIL"
		}
		target := file
		if target == "" {
			target = tool
		}
		return prefix + target + " -> " + status
	case "error":
		if detail != "" {
			return prefix + "ERR: " + detail
		}
		return prefix + "error"
	case "completed":
		if detail != "" {
			return prefix + "done: " + detail
		}
		return prefix + "done"
	default:
		if detail != "" {
			return prefix + detail
		}
		return ""
	}
}

// progressDetailStyle returns a style based on the progress phase.
func progressDetailStyle(payload map[string]any) lipgloss.Style {
	phase, _ := payload["phase"].(string)
	isErr, _ := payload["is_error"].(bool)

	if isErr {
		return lipgloss.NewStyle().Foreground(colorRed)
	}

	switch phase {
	case "thinking":
		return lipgloss.NewStyle().Foreground(colorGray).Italic(true)
	case "tool_call":
		return lipgloss.NewStyle().Foreground(colorCyan)
	case "tool_result":
		return lipgloss.NewStyle().Foreground(colorGreen)
	case "completed":
		return lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	case "error":
		return lipgloss.NewStyle().Foreground(colorRed)
	default:
		return lipgloss.NewStyle().Foreground(colorGray)
	}
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
