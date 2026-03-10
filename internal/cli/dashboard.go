package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/dashboard"
)

func newDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the live TUI dashboard",
		Long:  "Opens an interactive terminal dashboard showing story pipeline, agent status, event activity, and escalations.",
		RunE:  runDashboard,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runDashboard(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	model := dashboard.New(s.Events, s.Proj, version)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}

	return nil
}
