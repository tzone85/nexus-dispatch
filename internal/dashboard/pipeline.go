package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// renderPipeline renders the pipeline summary bar with counts and a progress bar.
func (m Model) renderPipeline(width int) string {
	buckets := m.countByStatus()
	total := 0
	completed := 0
	for status, count := range buckets {
		if status != "split" {
			total += count
		}
		if status == "merged" || status == "pr_submitted" {
			completed += count
		}
	}

	pct := 0
	if total > 0 {
		pct = completed * 100 / total
	}

	// Summary line
	summary := fmt.Sprintf(
		"Planned: %d  In Prog: %d  Review: %d  QA: %d  PR: %d  Merged: %d",
		buckets["planned"], buckets["in_progress"], buckets["review"],
		buckets["qa"], buckets["pr_submitted"], buckets["merged"],
	)

	// Progress bar
	barWidth := width - 20
	if barWidth < 10 {
		barWidth = 10
	}
	filled := barWidth * pct / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	progressLine := fmt.Sprintf("%s %d%% complete", bar, pct)

	header := headingStyle.Render("─ Pipeline ")

	// Include paused banner if any requirements are paused.
	pausedBanner := m.renderPausedBanner(width)
	if pausedBanner != "" {
		return lipgloss.JoinVertical(lipgloss.Left, header, pausedBanner, "  "+summary, "  "+progressLine)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "  "+summary, "  "+progressLine)
}

// countByStatus returns a map of pipeline bucket names to story counts.
func (m Model) countByStatus() map[string]int {
	buckets := make(map[string]int)
	for _, s := range m.stories {
		bucket := mapStatusToBucket(s.Status)
		buckets[bucket]++
	}
	return buckets
}

// renderPausedBanner returns a warning banner for any paused requirements,
// or an empty string if none are paused.
func (m Model) renderPausedBanner(width int) string {
	var paused []state.Requirement
	for _, r := range m.requirements {
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

// mapStatusToBucket maps a story status to one of the pipeline summary buckets.
func mapStatusToBucket(status string) string {
	switch status {
	case "draft", "estimated", "planned", "assigned":
		return "planned"
	case "in_progress":
		return "in_progress"
	case "review":
		return "review"
	case "qa", "qa_started", "qa_failed":
		return "qa"
	case "pr_submitted":
		return "pr_submitted"
	case "merged":
		return "merged"
	case "split":
		return "split"
	default:
		return "planned"
	}
}
