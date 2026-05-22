package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newReqLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "req-logs <req-id>",
		Short: "Print log file for a background-dispatched requirement",
		Long: `Print the log file captured when 'nxd req --background' self-daemonized.

The daemon redirects stdout+stderr to:
  ~/.nxd/logs/req-<req-id>.log

Use 'tail -f <path>' for live following — the --follow flag is not supported
in this command.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         runReqLogs,
	}
	return cmd
}

func runReqLogs(cmd *cobra.Command, args []string) error {
	reqID := args[0]

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	logPath := reqLogPath(stateDir, reqID)

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no log file found for requirement %s at %s\n"+
				"(the req may have been run without --background — check your terminal output)", reqID, logPath)
		}
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(cmd.OutOrStdout(), f); err != nil {
		return fmt.Errorf("read log file: %w", err)
	}
	return nil
}
