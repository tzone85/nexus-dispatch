package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newAgentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List all agents and their status",
		Long:  "Lists all agents with their current story, status, and session name. Use --status to filter.",
		RunE:  runAgents,
	}
	cmd.Flags().String("status", "", "Filter by agent status (active, idle, stuck, terminated)")
	cmd.SilenceUsage = true
	return cmd
}

func runAgents(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	statusFilter, _ := cmd.Flags().GetString("status")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	agents, err := s.Proj.ListAgents(state.AgentFilter{Status: statusFilter})
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	if len(agents) == 0 {
		if statusFilter != "" {
			fmt.Fprintf(out, "No agents with status %q found.\n", statusFilter)
		} else {
			fmt.Fprintf(out, "No agents found.\n")
		}
		return nil
	}

	fmt.Fprintf(out, "Agents (%d):\n\n", len(agents))
	fmt.Fprintf(out, "  %-20s %-12s %-10s %-20s %s\n", "ID", "TYPE", "STATUS", "SESSION", "STORY")
	fmt.Fprintf(out, "  %-20s %-12s %-10s %-20s %s\n", "----", "----", "------", "-------", "-----")

	for _, a := range agents {
		storyID := a.CurrentStoryID
		if storyID == "" {
			storyID = "-"
		}
		session := a.SessionName
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(out, "  %-20s %-12s %-10s %-20s %s\n",
			truncate(a.ID, 20), a.Type, a.Status, truncate(session, 20), storyID)
	}

	return nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
