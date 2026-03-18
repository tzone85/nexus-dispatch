package cli

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/dashboard"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the live TUI dashboard",
		Long:  "Opens an interactive terminal dashboard showing story pipeline, agent status, event activity, and escalations. Use --all to show requirements from all repos and archived ones.",
		RunE:  runDashboard,
	}
	cmd.Flags().Bool("all", false, "Show all requirements including archived and from other repos")
	cmd.SilenceUsage = true
	return cmd
}

func runDashboard(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	showAll, _ := cmd.Flags().GetBool("all")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	var filter state.ReqFilter
	if !showAll {
		cwd, _ := os.Getwd()
		filter.RepoPath = cwd
		filter.ExcludeArchived = true
	}

	model := dashboard.New(s.Events, s.Proj, version, filter)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}

	return nil
}
