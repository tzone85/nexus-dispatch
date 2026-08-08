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

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
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
	if !sanitize.ValidIdentifier(storyID) {
		return fmt.Errorf("invalid story id %q", storyID)
	}
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
	return followLogPoll(path, raw, out, 500*time.Millisecond, nil, nil)
}

// followLogPoll tails path (tail -f style), emitting only lines appended after
// the call begins. It stops when stop is closed; a nil stop channel follows
// forever (the production behaviour — until the process is killed). If ready is
// non-nil it is closed once the initial seek-to-end completes, so callers can
// order subsequent appends deterministically (used by tests).
//
// It uses a persisted bufio.Reader rather than a bufio.Scanner: a Scanner
// latches done permanently once its reader returns io.EOF, so a single Scanner
// reused across poll iterations (as this function previously did) would seek to
// end, hit EOF immediately, and then never observe any appended line. A
// bufio.Reader re-reads from the file's current offset on each ReadString, so it
// resumes cleanly. Partial (not yet newline-terminated) trailing writes are
// rewound so they are re-read whole once the writer completes the line.
func followLogPoll(path string, raw bool, out io.Writer, interval time.Duration, stop <-chan struct{}, ready chan<- struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()

	// Follow shows only content appended after we start.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek trace: %w", err)
	}
	if ready != nil {
		close(ready)
	}

	reader := bufio.NewReader(f)
	for {
		for {
			line, rerr := reader.ReadString('\n')
			if strings.HasSuffix(line, "\n") {
				if text := strings.TrimRight(line, "\n"); text != "" {
					if raw {
						fmt.Fprintln(out, text)
					} else {
						formatEntry(out, text)
					}
				}
				continue
			}
			// No complete line available. Rewind past any partial bytes so the
			// next poll re-reads the record once its newline is written.
			if len(line) > 0 {
				if _, serr := f.Seek(int64(-len(line)), io.SeekCurrent); serr != nil {
					return fmt.Errorf("seek trace: %w", serr)
				}
				reader.Reset(f)
			}
			if rerr != nil && rerr != io.EOF {
				return fmt.Errorf("read trace: %w", rerr)
			}
			break
		}
		select {
		case <-stop:
			return nil
		case <-time.After(interval):
		}
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
