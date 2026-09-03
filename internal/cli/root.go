package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/update"
)

// version is the build version shown by `nxd --version`. cmd/nxd/main.go
// receives the real value via -ldflags "-X main.version=..." and forwards it
// with SetVersion; "dev" is what an un-stamped `go build` reports.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "nxd",
	Short:   "Nexus Dispatch -- AI agent orchestrator",
	Long:    "NXD orchestrates autonomous AI agents through the full software development lifecycle.\nHand off a requirement, walk away, come back to merged PRs.",
	Version: version,
	// Errors are printed exactly once, by main (or the caller of Execute).
	// Without this Cobra prints "Error: ..." and main prints "error: ..." too.
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if dir, _ := cmd.Flags().GetString("state-dir"); dir != "" {
			stateDirOverride = dir
		}
		checkForModelUpdates(cmd)
	},
}

// SetVersion sets the version reported by `nxd --version` / `nxd version`.
// Called by main with the ldflags-injected build version.
func SetVersion(v string) {
	if v == "" {
		return
	}
	version = v
	rootCmd.Version = v
}

// Version returns the version string currently reported by the CLI.
func Version() string { return version }

func init() {
	rootCmd.PersistentFlags().String("config", "nxd.yaml", "Path to config file")
	rootCmd.PersistentFlags().String("state-dir", "", "Override workspace.state_dir for this invocation (per-project state)")

	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newReqCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newPauseCmd())
	rootCmd.AddCommand(newResumeCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newEscalationsCmd())
	rootCmd.AddCommand(newGCCmd())
	rootCmd.AddCommand(newConfigCmd())
	rootCmd.AddCommand(newSecurityCmd())
	rootCmd.AddCommand(newEventsCmd())
	rootCmd.AddCommand(newDashboardCmd())
	rootCmd.AddCommand(newArchiveCmd())
	rootCmd.AddCommand(newModelsCmd())
	rootCmd.AddCommand(newMetricsCmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newPlanCmd())
	rootCmd.AddCommand(newApproveCmd())
	rootCmd.AddCommand(newRejectCmd())
	rootCmd.AddCommand(newReviewStoryCmd())
	rootCmd.AddCommand(newMergeStoryCmd())
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newEstimateCmd())
	rootCmd.AddCommand(newReportCmd())
	rootCmd.AddCommand(newLogsCmd())
	rootCmd.AddCommand(newReqLogsCmd())
	rootCmd.AddCommand(newDiffCmd())
	rootCmd.AddCommand(newLearnCmd())
	rootCmd.AddCommand(newSpecCmd())
	rootCmd.AddCommand(newDirectCmd())
	rootCmd.AddCommand(newImproveCmd())
	rootCmd.AddCommand(newDBCmd())
	rootCmd.AddCommand(newTimelineCmd())
	rootCmd.AddCommand(newStateCmd())
	rootCmd.AddCommand(newCancelCmd())
	rootCmd.AddCommand(newVersionCmd())
}

// newVersionCmd prints the build version (same value as --version).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the nxd build version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "nxd %s\n", Version())
		},
	}
}

func Execute() error {
	err := rootCmd.Execute()
	waitForUpdateCheck()
	return err
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
		// Best-effort: the refresh runs in the background with its own
		// timeout, and cobra's PersistentPostRun waits for it only up to
		// updateCheckMaxWait so a short command (status, events) still gets a
		// chance to write the cache instead of being abandoned at exit.
		updateCheckDone = make(chan struct{})
		go func() {
			defer close(updateCheckDone)
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

// updateCheckDone is closed when the background update refresh finishes.
// Nil when no refresh was started this process.
var updateCheckDone chan struct{}

// updateCheckMaxWait bounds how long Execute waits for the refresh on exit.
var updateCheckMaxWait = 1500 * time.Millisecond

// waitForUpdateCheck blocks until the background refresh completes or the
// bounded wait elapses. No-op when no refresh was started.
func waitForUpdateCheck() {
	if updateCheckDone == nil {
		return
	}
	select {
	case <-updateCheckDone:
	case <-time.After(updateCheckMaxWait):
	}
}
