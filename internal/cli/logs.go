package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <story-id>",
		Short: "Show trace log for a story",
		Long:  "Displays the artifact trace JSONL for a story, showing LLM exchanges, tool calls, and progress events.",
		Args:  cobra.ExactArgs(1),
		RunE:  runLogs,
	}
	cmd.Flags().IntP("lines", "n", 50, "Number of recent entries to show")
	cmd.Flags().BoolP("follow", "f", false, "Follow the log (tail -f style)")
	cmd.Flags().Bool("raw", false, "Output raw JSONL without formatting")
	cmd.SilenceUsage = true
	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	storyID := args[0]
	lines, _ := cmd.Flags().GetInt("lines")
	follow, _ := cmd.Flags().GetBool("follow")
	raw, _ := cmd.Flags().GetBool("raw")

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	stateDir := expandHome(cfg.Workspace.StateDir)
	tracePath := filepath.Join(stateDir, "artifacts", storyID, "trace_events.jsonl")

	if _, err := os.Stat(tracePath); os.IsNotExist(err) {
		return fmt.Errorf("no trace log found for story %s (expected at %s)", storyID, tracePath)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Trace log for story: %s\n", storyID)
	fmt.Fprintf(out, "%s\n\n", strings.Repeat("─", 40))

	if follow {
		return followLog(tracePath, raw, out)
	}
	return tailLog(tracePath, lines, raw, out)
}

// traceEntry represents a single trace JSONL entry.
type traceEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Phase     string    `json:"phase,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Iteration int       `json:"iteration,omitempty"`
	Model     string    `json:"model,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
}

func tailLog(path string, n int, raw bool, out io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read trace: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	for _, line := range lines {
		if line == "" {
			continue
		}
		if raw {
			fmt.Fprintln(out, line)
			continue
		}
		formatEntry(out, line)
	}
	return nil
}

func followLog(path string, raw bool, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()

	// Seek to end.
	f.Seek(0, 2) //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for {
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if raw {
				fmt.Fprintln(out, line)
			} else {
				formatEntry(out, line)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func formatEntry(out io.Writer, line string) {
	var entry traceEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		fmt.Fprintln(out, line)
		return
	}

	ts := entry.Timestamp.Format("15:04:05")
	errMarker := ""
	if entry.IsError {
		errMarker = " [ERROR]"
	}

	switch {
	case entry.Tool != "":
		fmt.Fprintf(out, "%s  %-12s  tool=%s%s  %s\n", ts, entry.Phase, entry.Tool, errMarker, entry.Detail)
	case entry.Phase != "":
		fmt.Fprintf(out, "%s  %-12s  iter=%d%s  %s\n", ts, entry.Phase, entry.Iteration, errMarker, entry.Detail)
	default:
		fmt.Fprintf(out, "%s  %s%s  %s\n", ts, entry.Type, errMarker, entry.Detail)
	}
}
