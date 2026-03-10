package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

const defaultEventLimit = 50

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "List events from the event store",
		Long:  "Lists events with optional filters for type, story, and limit. Displays newest first.",
		RunE:  runEvents,
	}
	cmd.Flags().String("type", "", "Filter by event type (e.g., REQ_SUBMITTED, STORY_CREATED)")
	cmd.Flags().String("story", "", "Filter by story ID")
	cmd.Flags().Int("limit", defaultEventLimit, "Maximum number of events to display")
	cmd.SilenceUsage = true
	return cmd
}

func runEvents(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	eventType, _ := cmd.Flags().GetString("type")
	storyID, _ := cmd.Flags().GetString("story")
	limit, _ := cmd.Flags().GetInt("limit")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	filter := state.EventFilter{
		Type:    state.EventType(eventType),
		StoryID: storyID,
	}

	events, err := s.Events.List(filter)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	if len(events) == 0 {
		fmt.Fprintf(out, "No events found.\n")
		return nil
	}

	// Reverse for newest-first display
	reversed := reverseEvents(events)

	// Apply limit
	if limit > 0 && len(reversed) > limit {
		reversed = reversed[:limit]
	}

	fmt.Fprintf(out, "Events (%d shown of %d total):\n\n", len(reversed), len(events))

	for _, evt := range reversed {
		fmt.Fprintf(out, "  [%s] %s\n", evt.Timestamp.Format("2006-01-02 15:04:05"), evt.Type)
		fmt.Fprintf(out, "    ID: %s", evt.ID)
		if evt.AgentID != "" {
			fmt.Fprintf(out, " | Agent: %s", evt.AgentID)
		}
		if evt.StoryID != "" {
			fmt.Fprintf(out, " | Story: %s", evt.StoryID)
		}
		fmt.Fprintf(out, "\n")

		if len(evt.Payload) > 0 {
			payloadStr := formatPayload(evt.Payload)
			if payloadStr != "" {
				fmt.Fprintf(out, "    Payload: %s\n", payloadStr)
			}
		}
		fmt.Fprintf(out, "\n")
	}

	return nil
}

// reverseEvents returns a new slice with events in reverse order.
func reverseEvents(events []state.Event) []state.Event {
	n := len(events)
	reversed := make([]state.Event, n)
	for i, evt := range events {
		reversed[n-1-i] = evt
	}
	return reversed
}

// formatPayload returns a compact JSON representation of the event payload.
func formatPayload(payload []byte) string {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return string(payload)
	}

	compact, err := json.Marshal(m)
	if err != nil {
		return string(payload)
	}

	// Truncate very long payloads for display
	s := string(compact)
	if len(s) > 200 {
		return s[:197] + "..."
	}
	return s
}
