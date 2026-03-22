package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// pipelineStatuses defines the ordered columns in the story pipeline view.
var pipelineStatuses = []string{
	"planned", "assigned", "in_progress", "review", "qa", "merged",
}

// pipelineLabels maps status keys to display labels.
var pipelineLabels = map[string]string{
	"planned":     "PLANNED",
	"assigned":    "ASSIGNED",
	"in_progress": "IN PROGRESS",
	"review":      "REVIEW",
	"qa":          "QA",
	"merged":      "MERGED",
}

// renderPipeline renders Panel 1: the story pipeline grouped by status.
// If any requirements are paused, a banner is shown at the top.
func renderPipeline(stories []state.Story, reqs []state.Requirement, width, height int) string {
	var rows []string

	// Show paused requirement banners
	pausedBanner := renderPausedBanner(reqs, width)
	if pausedBanner != "" {
		rows = append(rows, pausedBanner)
		height -= 2 // account for banner line + spacing
	}

	grouped := groupStoriesByStatus(stories)

	columnCount := len(pipelineStatuses)
	if columnCount == 0 {
		return strings.Join(rows, "\n")
	}

	// Reserve space for borders and padding between columns.
	availableWidth := width - 4 // panel padding
	colWidth := availableWidth / columnCount
	if colWidth < 14 {
		colWidth = 14
	}

	var columns []string
	for _, status := range pipelineStatuses {
		col := renderPipelineColumn(status, grouped[status], colWidth, height-6)
		columns = append(columns, col)
	}

	rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, columns...))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderPausedBanner returns a warning banner for any paused requirements,
// or an empty string if none are paused.
func renderPausedBanner(reqs []state.Requirement, width int) string {
	var paused []state.Requirement
	for _, r := range reqs {
		if r.Status == "paused" {
			paused = append(paused, r)
		}
	}

	if len(paused) == 0 {
		return ""
	}

	var labels []string
	for _, r := range paused {
		id := r.ID
		if len(id) > 8 {
			id = id[:8]
		}
		labels = append(labels, fmt.Sprintf("%s (%s)", id, r.Title))
	}

	banner := fmt.Sprintf("PAUSED: %s", strings.Join(labels, ", "))
	return statusPausedStyle.Width(width).Align(lipgloss.Center).Render(banner)
}

// renderPipelineColumn renders a single status column in the pipeline.
func renderPipelineColumn(status string, stories []state.Story, width, maxHeight int) string {
	label := pipelineLabels[status]
	style := storyStatusStyle(status)

	header := style.Copy().Bold(true).Width(width).Align(lipgloss.Center).Render(
		fmt.Sprintf("%s (%d)", label, len(stories)),
	)

	var rows []string
	rows = append(rows, header)
	rows = append(rows, strings.Repeat("─", width))

	for _, s := range stories {
		card := renderStoryCard(s, width-2)
		rows = append(rows, card)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// Truncate if exceeding max height.
	lines := strings.Split(content, "\n")
	if maxHeight > 0 && len(lines) > maxHeight {
		lines = lines[:maxHeight]
		lines = append(lines, style.Render("  ..."))
	}

	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// renderStoryCard renders a single story as a compact card.
func renderStoryCard(s state.Story, width int) string {
	style := storyStatusStyle(s.Status)

	id := truncateStr(s.ID, 20)
	title := truncateStr(s.Title, width-4)
	badge := complexityStyle.Render(fmt.Sprintf("[C%d]", s.Complexity))

	line1 := style.Render(id) + " " + badge
	if s.EscalationTier > 0 {
		tierBadge := lipgloss.NewStyle().Foreground(colorGray).Render(fmt.Sprintf("[T%d]", s.EscalationTier))
		line1 = line1 + " " + tierBadge
	}
	line2 := lipgloss.NewStyle().Foreground(colorWhite).Width(width).Render(title)

	return lipgloss.JoinVertical(lipgloss.Left, line1, line2, "")
}

// groupStoriesByStatus groups stories into a map keyed by status.
// Stories with statuses not in pipelineStatuses are mapped to the closest match:
//   - "draft", "estimated" -> "planned"
//   - "pr_submitted" -> "merged"
//   - "qa_failed" -> "qa"
func groupStoriesByStatus(stories []state.Story) map[string][]state.Story {
	groups := make(map[string][]state.Story, len(pipelineStatuses))
	for _, s := range stories {
		bucket := mapStatusToBucket(s.Status)
		groups[bucket] = append(groups[bucket], s)
	}
	return groups
}

// mapStatusToBucket maps a story status to one of the pipeline columns.
func mapStatusToBucket(status string) string {
	switch status {
	case "draft", "estimated", "planned":
		return "planned"
	case "assigned":
		return "assigned"
	case "in_progress":
		return "in_progress"
	case "review":
		return "review"
	case "qa", "qa_failed":
		return "qa"
	case "pr_submitted", "merged":
		return "merged"
	default:
		return "planned"
	}
}
