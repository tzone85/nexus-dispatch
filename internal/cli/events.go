package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

const defaultEventLimit = 50

// eventJSON is the --json shape of an event: the envelope plus the payload
// decoded into an object instead of the base64 blob the log stores.
type eventJSON struct {
	ID        string          `json:"id"`
	Type      state.EventType `json:"type"`
	Timestamp string          `json:"timestamp"`
	AgentID   string          `json:"agent_id,omitempty"`
	StoryID   string          `json:"story_id,omitempty"`
	Payload   map[string]any  `json:"payload,omitempty"`
}

func eventsForJSON(events []state.Event) []eventJSON {
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		var payload map[string]any
		if len(e.Payload) > 0 {
			payload = state.DecodePayload(e.Payload)
		}
		out = append(out, eventJSON{
			ID: e.ID, Type: e.Type, Timestamp: e.Timestamp.UTC().Format(time.RFC3339Nano),
			AgentID: e.AgentID, StoryID: e.StoryID, Payload: payload,
		})
	}
	return out
}

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
	cmd.Flags().Bool("json", false, "machine-readable JSON output (newest first, payload decoded)")
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

	asJSON, _ := cmd.Flags().GetBool("json")
	if len(events) == 0 && !asJSON {
		fmt.Fprintf(out, "No events found.\n")
		return nil
	}

	// Reverse for newest-first display
	reversed := reverseEvents(events)

	// Apply limit
	if limit > 0 && len(reversed) > limit {
		reversed = reversed[:limit]
	}

	if asJSON {
		return writeJSON(out, eventsForJSON(reversed))
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
