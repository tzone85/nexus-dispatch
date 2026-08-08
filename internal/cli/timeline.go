package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newTimelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timeline [req-id]",
		Short: "Show a requirement's chronological timeline",
		Long: "Reconstructs the requirement's full history from the event log: planning, waves, per-story " +
			"lifecycle with durations, review/QA/security outcomes, escalations, pauses, and completion.\n\n" +
			"If req-id is omitted and only one requirement exists, it is selected automatically.",
		Args: cobra.MaximumNArgs(1),
		RunE: runTimeline,
	}
	cmd.Flags().Bool("json", false, "Emit the timeline as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func runTimeline(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	asJSON, _ := cmd.Flags().GetBool("json")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	reqID, err := resolveTimelineReq(s.Proj, args)
	if err != nil {
		return err
	}

	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement not found: %w", err)
	}
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return fmt.Errorf("list stories: %w", err)
	}
	events, err := s.Events.List(state.EventFilter{})
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	tl := engine.BuildTimeline(req, stories, events)

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(tl)
	}
	renderTimeline(out, tl)
	return nil
}

// resolveTimelineReq picks the requirement: explicit arg wins; otherwise a
// sole existing requirement is auto-selected.
func resolveTimelineReq(proj *state.SQLiteStore, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	reqs, err := proj.ListRequirements()
	if err != nil {
		return "", fmt.Errorf("list requirements: %w", err)
	}
	switch len(reqs) {
	case 0:
		return "", fmt.Errorf("no requirements found — run 'nxd req' first")
	case 1:
		return reqs[0].ID, nil
	default:
		return "", fmt.Errorf("multiple requirements exist — specify one: nxd timeline <req-id>")
	}
}

func renderTimeline(out io.Writer, tl engine.Timeline) {
	fmt.Fprintf(out, "Timeline — %s (%s) [%s]\n", tl.Title, shortID(tl.ReqID), tl.Status)
	if !tl.Start.IsZero() {
		line := "Started " + tl.Start.Local().Format("2006-01-02 15:04:05")
		if !tl.End.IsZero() && tl.End.After(tl.Start) {
			line += fmt.Sprintf(" · last event %s · span %s",
				tl.End.Local().Format("15:04:05"), formatDuration(tl.End.Sub(tl.Start)))
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out)

	if len(tl.Entries) == 0 {
		fmt.Fprintln(out, "No events recorded for this requirement.")
		return
	}

	for _, e := range tl.Entries {
		fmt.Fprintf(out, "  %s  %s\n", e.Time.Local().Format("15:04:05"), e.Label)
	}

	if len(tl.Stories) > 0 {
		fmt.Fprintln(out, "\nStories:")
		for _, s := range tl.Stories {
			mark := statusMark(s.Status)
			dur := ""
			if s.Duration > 0 {
				dur = "  " + formatDuration(s.Duration)
			}
			fmt.Fprintf(out, "  %s %-12s %-40s %s%s (wave %d)\n",
				mark, shortID(s.ID), truncateTitle(s.Title, 40), s.Status, dur, s.Wave)
		}
	}
	fmt.Fprintf(out, "\nSummary: %d/%d stories merged\n", tl.Merged, tl.Total)
}

func statusMark(status string) string {
	switch status {
	case "merged":
		return "✓"
	case "failed", "cancelled":
		return "✗"
	case "in_progress", "merging":
		return "▶"
	default:
		return "·"
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func truncateTitle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
