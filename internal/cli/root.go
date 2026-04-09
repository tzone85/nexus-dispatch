package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

var version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "nxd",
	Short: "Nexus Dispatch -- AI agent orchestrator",
	Long:  "NXD orchestrates autonomous AI agents through the full software development lifecycle.\nHand off a requirement, walk away, come back to merged PRs.",
	Version: version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		checkForModelUpdates(cmd)
	},
}

func init() {
	rootCmd.PersistentFlags().String("config", "nxd.yaml", "Path to config file")

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newReqCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newPauseCmd())
	rootCmd.AddCommand(newResumeCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newEscalationsCmd())
	rootCmd.AddCommand(newGCCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newEventsCmd())
	rootCmd.AddCommand(newDashboardCmd())
	rootCmd.AddCommand(newArchiveCmd())
	rootCmd.AddCommand(newModelsCmd())
}

func Execute() error {
	return rootCmd.Execute()
}

// checkForModelUpdates prints cached update notices and, if the cache is stale,
// launches a background goroutine to refresh it. It silently returns on any
// error so it never blocks or breaks normal CLI operation.
func checkForModelUpdates(cmd *cobra.Command) {
	if os.Getenv("NXD_UPDATE_CHECK") == "false" {
		return
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return
	}

	if !cfg.Workspace.UpdateCheck || cfg.Workspace.UpdateIntervalHours <= 0 {
		return
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	cachePath := filepath.Join(stateDir, "update-status.json")

	cached, err := update.ReadCache(cachePath)
	if err != nil {
		return
	}

	if len(update.UpdatesAvailable(cached)) > 0 {
		update.PrintNotices(os.Stderr, cached)
	}

	if update.IsStale(cached, cfg.Workspace.UpdateIntervalHours) {
		go func() {
			ollamaModels, googleModels := collectConfiguredModels(cfg)

			opts := []update.CheckerOption{}
			if host := os.Getenv("OLLAMA_HOST"); host != "" {
				opts = append(opts, update.WithOllamaLocalURL(host))
			}
			if key := os.Getenv("GOOGLE_AI_API_KEY"); key != "" {
				opts = append(opts, update.WithGoogleAPIKey(key))
			}

			checker := update.NewChecker(opts...)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()

			result := checker.RunCheck(ctx, ollamaModels, googleModels)
			_ = update.WriteCache(cachePath, result)
		}()
	}
}
