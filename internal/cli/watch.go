package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream pipeline events in real time",
		Long:  "Polls the event store and prints new events as they arrive. Ctrl+C to stop.",
		RunE:  runWatch,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runWatch(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Watching for events... (Ctrl+C to stop)")
	fmt.Fprintln(out)

	lastSeen := 0
	ctx := cmd.Context()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		events, err := s.Events.List(state.EventFilter{})
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if len(events) > lastSeen {
			for _, evt := range events[lastSeen:] {
				ts := evt.Timestamp.Format("15:04:05")
				line := fmt.Sprintf("[%s] %s", ts, evt.Type)
				if evt.StoryID != "" {
					line += " " + evt.StoryID
				}
				if evt.AgentID != "" {
					line += " agent=" + evt.AgentID
				}

				payload := state.DecodePayload(evt.Payload)
				if title, ok := payload["title"].(string); ok {
					line += fmt.Sprintf(" %q", title)
				}
				if status, ok := payload["status"].(string); ok {
					line += " status=" + status
				}

				fmt.Fprintln(out, line)
			}
			lastSeen = len(events)
		}

		time.Sleep(500 * time.Millisecond)
	}
}
