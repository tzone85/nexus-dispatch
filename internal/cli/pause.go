package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause <req-id>",
		Short: "Pause a running requirement pipeline",
		Long:  "Pauses a requirement by emitting a REQ_PAUSED event. Active agents continue their current work, but no new waves will be dispatched until the requirement is resumed.",
		Args:  cobra.ExactArgs(1),
		RunE:  runPause,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runPause(cmd *cobra.Command, args []string) error {
	reqID := args[0]

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	// Verify the requirement exists
	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement not found: %w", err)
	}

	// Validate requirement is in a pausable state
	if err := validatePausable(req); err != nil {
		return err
	}

	// Emit REQ_PAUSED event
	evt := state.NewEvent(state.EventReqPaused, "", "", map[string]any{
		"id": reqID,
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("append pause event: %w", err)
	}
	if err := s.Proj.Project(evt); err != nil {
		return fmt.Errorf("project pause event: %w", err)
	}

	fmt.Fprintf(out, "Paused requirement: %s (%s)\n", req.Title, reqID)
	fmt.Fprintf(out, "Active agents will finish their current work, but no new waves will be dispatched.\n")
	fmt.Fprintf(out, "Run 'nxd resume %s' to resume.\n", reqID)

	return nil
}

// validatePausable checks that a requirement is in a state that can be paused.
func validatePausable(req state.Requirement) error {
	switch req.Status {
	case "paused":
		return fmt.Errorf("requirement %s is already paused", req.ID)
	case "completed":
		return fmt.Errorf("requirement %s is already completed", req.ID)
	case "pending":
		return fmt.Errorf("requirement %s has not been planned yet", req.ID)
	default:
		return nil
	}
}
