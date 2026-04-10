package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
)

func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show pipeline metrics and token usage",
		RunE:  runMetrics,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func runMetrics(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}

	jsonMode, _ := cmd.Flags().GetBool("json")

	stateDir := expandHome(cfg.Workspace.StateDir)
	metricsPath := filepath.Join(stateDir, "metrics.jsonl")
	rec := metrics.NewRecorder(metricsPath)

	entries, err := rec.ReadAll()
	if err != nil {
		return fmt.Errorf("read metrics: %w", err)
	}

	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "No metrics recorded yet. Run 'nxd req' to start collecting.")
		return nil
	}

	summary := metrics.Summarize(entries)
	if jsonMode {
		data, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintln(out, string(data))
	} else {
		metrics.PrintSummary(out, summary)
	}

	return nil
}
