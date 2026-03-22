package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/dashboard"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/web"
)

func newDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the live TUI dashboard",
		Long:  "Opens an interactive terminal dashboard showing story pipeline, agent status, event activity, and escalations. Use --web to launch a browser-based dashboard. Use --all to show requirements from all repos and archived ones.",
		RunE:  runDashboard,
	}
	cmd.Flags().Bool("all", false, "Show all requirements including archived and from other repos")
	cmd.Flags().Bool("web", false, "Launch web dashboard instead of TUI")
	cmd.Flags().Int("port", 8787, "Web server port")
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

	isWeb, _ := cmd.Flags().GetBool("web")
	port, _ := cmd.Flags().GetInt("port")

	if isWeb {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		srv := web.NewServer(s.Events, s.Proj, port, filter)
		if err := srv.Start(ctx); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("web server: %w", err)
		}
		fmt.Println("Dashboard server stopped")
		return nil
	}

	model := dashboard.New(s.Events, s.Proj, version, filter)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("dashboard error: %w", err)
	}

	return nil
}
