package cli

import (
	"github.com/spf13/cobra"
)

var version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "nxd",
	Short: "Nexus Dispatch -- AI agent orchestrator",
	Long:  "NXD orchestrates autonomous AI agents through the full software development lifecycle.\nHand off a requirement, walk away, come back to merged PRs.",
	Version: version,
}

func init() {
	rootCmd.PersistentFlags().String("config", "nxd.yaml", "Path to config file")

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newReqCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newResumeCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newEscalationsCmd())
	rootCmd.AddCommand(newGCCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newEventsCmd())
	rootCmd.AddCommand(newDashboardCmd())
}

func Execute() error {
	return rootCmd.Execute()
}
