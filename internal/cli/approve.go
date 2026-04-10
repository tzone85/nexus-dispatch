package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <req-id>",
		Short: "Approve a plan pending review",
		Long:  "Approves a requirement that is in pending_review status by emitting a REQ_PLANNED event, transitioning the requirement to planned status so execution can proceed.",
		Args:  cobra.ExactArgs(1),
		RunE:  runApprove,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runApprove(cmd *cobra.Command, args []string) error {
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

	// Emit REQ_PLANNED event to transition to planned
	evt := state.NewEvent(state.EventReqPlanned, "", "", map[string]any{
		"id": reqID,
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("append approve event: %w", err)
	}
	if err := s.Proj.Project(evt); err != nil {
		return fmt.Errorf("project approve event: %w", err)
	}

	fmt.Fprintf(out, "Approved requirement: %s (%s)\n", req.Title, reqID)
	fmt.Fprintf(out, "Run 'nxd resume %s' to begin execution.\n", reqID)

	return nil
}
