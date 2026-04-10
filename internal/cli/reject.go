package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newRejectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject <req-id>",
		Short: "Reject a plan pending review",
		Long:  "Rejects a requirement that is in pending_review status by emitting a REQ_REJECTED event, setting the requirement status to rejected.",
		Args:  cobra.ExactArgs(1),
		RunE:  runReject,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runReject(cmd *cobra.Command, args []string) error {
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

	// Validate requirement is pending review
	if req.Status != "pending_review" {
		return fmt.Errorf("requirement %s is in status %q, expected \"pending_review\"", reqID, req.Status)
	}

	// Emit REQ_REJECTED event
	evt := state.NewEvent(state.EventReqRejected, "", "", map[string]any{
		"id": reqID,
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("append reject event: %w", err)
	}
	if err := s.Proj.Project(evt); err != nil {
		return fmt.Errorf("project reject event: %w", err)
	}

	fmt.Fprintf(out, "Rejected requirement: %s (%s)\n", req.Title, reqID)
	fmt.Fprintf(out, "Plan has been rejected. Submit a new requirement with 'nxd req'.\n")

	return nil
}
