package engine

import (
	"fmt"
	"strings"
	"time"
)

// RenderMarkdown produces a markdown delivery report for the given ReportData.
// When internal is true, additional sections with story detail, agent performance,
// and full timeline are appended. The project parameter identifies the client project.
func RenderMarkdown(data ReportData, project string, internal bool) string {
	var b strings.Builder

	// 1. Header
	fmt.Fprintf(&b, "# Delivery Report: %s\n\n", data.Title)
	fmt.Fprintf(&b, "**Project:** %s  \n", project)
	fmt.Fprintf(&b, "**Requirement ID:** %s  \n", data.RequirementID)
	fmt.Fprintf(&b, "**Status:** %s  \n", formatStatus(data))
	fmt.Fprintf(&b, "**Generated:** %s  \n\n", data.GeneratedAt.Format("2006-01-02 15:04 UTC"))

	// 2. Requirement
	b.WriteString("## Requirement\n\n")
	fmt.Fprintf(&b, "%s\n\n", data.Description)

	// 3. Deliverables
	b.WriteString("## Deliverables\n\n")
	b.WriteString("| Story | Status | Complexity | PR |\n")
	b.WriteString("|-------|--------|------------|----|\n")
	for _, s := range data.Stories {
		prCell := "—"
		if s.PRNumber > 0 {
			prCell = fmt.Sprintf("[#%d](%s)", s.PRNumber, s.PRUrl)
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", s.Title, s.Status, s.Complexity, prCell)
	}
	b.WriteString("\n")

	// 4. Timeline
	b.WriteString("## Timeline\n\n")
	if len(data.Timeline) == 0 {
		b.WriteString("No significant events recorded.\n\n")
	} else {
		for _, entry := range data.Timeline {
			fmt.Fprintf(&b, "- **%s** — %s\n", entry.Timestamp.Format("2006-01-02 15:04"), entry.Description)
		}
		b.WriteString("\n")
	}

	// 5. Effort Summary
	b.WriteString("## Effort Summary\n\n")
	s := data.Effort.Summary
	fmt.Fprintf(&b, "- **Stories:** %d  \n", s.StoryCount)
	fmt.Fprintf(&b, "- **Total Complexity:** %d points  \n", s.TotalPoints)
	fmt.Fprintf(&b, "- **HoursLow–HoursHigh:** %.1f–%.1f h  \n", s.HoursLow, s.HoursHigh)
	fmt.Fprintf(&b, "- **Estimated Cost:** %s %.0f–%.0f  \n\n", s.Currency, s.QuoteLow, s.QuoteHigh)

	if !internal {
		return b.String()
	}

	// 6. Internal: Story Detail
	b.WriteString("## Internal: Story Detail\n\n")
	b.WriteString("| Story | Escalations | Retries | Duration | Tier |\n")
	b.WriteString("|-------|-------------|---------|----------|------|\n")
	for _, story := range data.Stories {
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %d |\n",
			story.Title, story.EscalationCount, story.RetryCount,
			FormatDuration(story.Duration), story.EscalationTier)
	}
	b.WriteString("\n")

	// 7. Internal: Agent Performance
	b.WriteString("## Internal: Agent Performance\n\n")
	b.WriteString("| Agent | Stories Worked | Escalations |\n")
	b.WriteString("|-------|----------------|-------------|\n")
	for _, stat := range data.AgentStats {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", stat.AgentID, stat.StoriesWorked, stat.Escalations)
	}
	b.WriteString("\n")

	// 8. Internal: LLM Cost Breakdown
	b.WriteString("## Internal: LLM Cost Breakdown\n\n")
	if len(data.LLMUsage) == 0 {
		b.WriteString("No LLM metrics recorded.\n\n")
	} else {
		b.WriteString("| Story | Model | Calls | Input Tokens | Output Tokens | Cost |\n")
		b.WriteString("|-------|-------|-------|--------------|---------------|------|\n")
		for _, usage := range data.LLMUsage {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %s %.4f |\n",
				usage.StoryID, usage.Model, usage.Calls,
				usage.TokensIn, usage.TokensOut, usage.Currency, usage.Cost)
		}
		b.WriteString("\n")
	}

	// 9. Internal: Timeline Detail
	b.WriteString("## Internal: Timeline Detail\n\n")
	b.WriteString("| Time | Event | Story | Description |\n")
	b.WriteString("|------|-------|-------|-------------|\n")
	for _, entry := range data.Timeline {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			entry.Timestamp.Format("2006-01-02 15:04"),
			entry.EventType,
			entry.StoryID,
			entry.Description)
	}
	b.WriteString("\n")

	return b.String()
}

// RenderHTML produces a self-contained HTML delivery report for the given ReportData.
// When internal is true, internal sections are included. All user data is HTML-escaped.
// No JavaScript is used.
func RenderHTML(data ReportData, project string, internal bool) string {
	var b strings.Builder

	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>Delivery Report: %s</title>\n", escapeHTML(data.Title))
	b.WriteString(htmlStyle())
	b.WriteString("</head>\n<body>\n")

	// Header
	fmt.Fprintf(&b, "<h1>Delivery Report: %s</h1>\n", escapeHTML(data.Title))
	b.WriteString("<table class=\"meta\">\n")
	fmt.Fprintf(&b, "<tr><th>Project</th><td>%s</td></tr>\n", escapeHTML(project))
	fmt.Fprintf(&b, "<tr><th>Requirement ID</th><td>%s</td></tr>\n", escapeHTML(data.RequirementID))
	fmt.Fprintf(&b, "<tr><th>Status</th><td>%s</td></tr>\n", escapeHTML(formatStatus(data)))
	fmt.Fprintf(&b, "<tr><th>Generated</th><td>%s</td></tr>\n", data.GeneratedAt.Format("2006-01-02 15:04 UTC"))
	b.WriteString("</table>\n\n")

	// Requirement
	b.WriteString("<h2>Requirement</h2>\n")
	fmt.Fprintf(&b, "<p>%s</p>\n\n", escapeHTML(data.Description))

	// Deliverables
	b.WriteString("<h2>Deliverables</h2>\n")
	b.WriteString("<table>\n<thead><tr><th>Story</th><th>Status</th><th>Complexity</th><th>PR</th></tr></thead>\n<tbody>\n")
	for _, s := range data.Stories {
		prCell := "—"
		if s.PRNumber > 0 {
			prCell = fmt.Sprintf(`<a href="%s">#%d</a>`, escapeHTML(s.PRUrl), s.PRNumber)
		}
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%d</td><td>%s</td></tr>\n",
			escapeHTML(s.Title), escapeHTML(s.Status), s.Complexity, prCell)
	}
	b.WriteString("</tbody>\n</table>\n\n")

	// Timeline
	b.WriteString("<h2>Timeline</h2>\n")
	if len(data.Timeline) == 0 {
		b.WriteString("<p>No significant events recorded.</p>\n\n")
	} else {
		b.WriteString("<ul>\n")
		for _, entry := range data.Timeline {
			fmt.Fprintf(&b, "<li><strong>%s</strong> — %s</li>\n",
				entry.Timestamp.Format("2006-01-02 15:04"),
				escapeHTML(entry.Description))
		}
		b.WriteString("</ul>\n\n")
	}

	// Effort Summary
	s := data.Effort.Summary
	b.WriteString("<h2>Effort Summary</h2>\n")
	b.WriteString("<table class=\"meta\">\n")
	fmt.Fprintf(&b, "<tr><th>Stories</th><td>%d</td></tr>\n", s.StoryCount)
	fmt.Fprintf(&b, "<tr><th>Total Complexity</th><td>%d points</td></tr>\n", s.TotalPoints)
	fmt.Fprintf(&b, "<tr><th>Estimated Hours</th><td>%.1f–%.1f h</td></tr>\n", s.HoursLow, s.HoursHigh)
	fmt.Fprintf(&b, "<tr><th>Estimated Cost</th><td>%s %.0f–%.0f</td></tr>\n",
		escapeHTML(s.Currency), s.QuoteLow, s.QuoteHigh)
	b.WriteString("</table>\n\n")

	if internal {
		// Internal: Story Detail
		b.WriteString("<h2>Internal: Story Detail</h2>\n")
		b.WriteString("<table>\n<thead><tr><th>Story</th><th>Escalations</th><th>Retries</th><th>Duration</th><th>Tier</th></tr></thead>\n<tbody>\n")
		for _, story := range data.Stories {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%s</td><td>%d</td></tr>\n",
				escapeHTML(story.Title), story.EscalationCount, story.RetryCount,
				FormatDuration(story.Duration), story.EscalationTier)
		}
		b.WriteString("</tbody>\n</table>\n\n")

		// Internal: Agent Performance
		b.WriteString("<h2>Internal: Agent Performance</h2>\n")
		b.WriteString("<table>\n<thead><tr><th>Agent</th><th>Stories Worked</th><th>Escalations</th></tr></thead>\n<tbody>\n")
		for _, stat := range data.AgentStats {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td></tr>\n",
				escapeHTML(stat.AgentID), stat.StoriesWorked, stat.Escalations)
		}
		b.WriteString("</tbody>\n</table>\n\n")

		// Internal: LLM Cost Breakdown
		b.WriteString("<h2>Internal: LLM Cost Breakdown</h2>\n")
		if len(data.LLMUsage) == 0 {
			b.WriteString("<p>No LLM metrics recorded.</p>\n\n")
		} else {
			b.WriteString("<table>\n<thead><tr><th>Story</th><th>Model</th><th>Calls</th><th>Input Tokens</th><th>Output Tokens</th><th>Cost</th></tr></thead>\n<tbody>\n")
			for _, usage := range data.LLMUsage {
				fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%s %.4f</td></tr>\n",
					escapeHTML(usage.StoryID), escapeHTML(usage.Model), usage.Calls,
					usage.TokensIn, usage.TokensOut, escapeHTML(usage.Currency), usage.Cost)
			}
			b.WriteString("</tbody>\n</table>\n\n")
		}

		// Internal: Timeline Detail
		b.WriteString("<h2>Internal: Timeline Detail</h2>\n")
		b.WriteString("<table>\n<thead><tr><th>Time</th><th>Event</th><th>Story</th><th>Description</th></tr></thead>\n<tbody>\n")
		for _, entry := range data.Timeline {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				entry.Timestamp.Format("2006-01-02 15:04"),
				escapeHTML(entry.EventType),
				escapeHTML(entry.StoryID),
				escapeHTML(entry.Description))
		}
		b.WriteString("</tbody>\n</table>\n\n")
	}

	b.WriteString("</body>\n</html>\n")
	return b.String()
}

// FormatDuration converts a time.Duration to a human-readable string.
// Examples: "0s", "45s", "5m 30s", "2h 15m".
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

// escapeHTML escapes the five HTML-special characters: &, <, >, ", '.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

// formatStatus returns a human-readable status string for the report.
// For NEEDS_CONTEXT it counts merged vs total stories.
func formatStatus(data ReportData) string {
	switch data.Status {
	case ReportStatusDone:
		return "Completed"
	case ReportStatusDoneWithConcerns:
		return "Completed (with concerns)"
	case ReportStatusBlocked:
		return "Blocked"
	case ReportStatusNeedsContext:
		merged := 0
		for _, s := range data.Stories {
			if s.Status == "merged" {
				merged++
			}
		}
		return fmt.Sprintf("In Progress (%d of %d merged)", merged, len(data.Stories))
	default:
		return string(data.Status)
	}
}

// htmlStyle returns an inline <style> block for print-friendly HTML reports.
func htmlStyle() string {
	return `<style>
body { font-family: Arial, sans-serif; color: #111; background: #fff; max-width: 900px; margin: 40px auto; padding: 0 20px; }
h1 { border-bottom: 2px solid #333; padding-bottom: 8px; }
h2 { border-bottom: 1px solid #ccc; padding-bottom: 4px; margin-top: 32px; }
table { border-collapse: collapse; width: 100%; margin-bottom: 16px; }
th, td { border: 1px solid #ccc; padding: 8px 12px; text-align: left; }
th { background: #f4f4f4; }
table.meta { width: auto; }
a { color: #0066cc; }
ul { padding-left: 20px; }
@media print { body { margin: 0; } a { color: #000; } }
</style>
`
}
