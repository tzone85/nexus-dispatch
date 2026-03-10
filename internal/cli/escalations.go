package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEscalationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "escalations",
		Short: "List all escalation events",
		Long:  "Lists all escalation events showing story ID, from agent, reason, and status.",
		RunE:  runEscalations,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runEscalations(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	escalations, err := s.Proj.ListEscalations()
	if err != nil {
		return fmt.Errorf("list escalations: %w", err)
	}

	if len(escalations) == 0 {
		fmt.Fprintf(out, "No escalations found.\n")
		return nil
	}

	fmt.Fprintf(out, "Escalations (%d):\n\n", len(escalations))
	fmt.Fprintf(out, "  %-12s %-20s %-10s %s\n", "STORY", "FROM", "STATUS", "REASON")
	fmt.Fprintf(out, "  %-12s %-20s %-10s %s\n", "-----", "----", "------", "------")

	for _, e := range escalations {
		storyID := e.StoryID
		if storyID == "" {
			storyID = "-"
		}
		fmt.Fprintf(out, "  %-12s %-20s %-10s %s\n",
			truncate(storyID, 12), truncate(e.FromAgent, 20), e.Status, e.Reason)
	}

	return nil
}
