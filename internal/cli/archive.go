package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive <req-id>",
		Short: "Archive a requirement and all its stories",
		Long:  "Marks a requirement as archived. Archived requirements are hidden from the dashboard and status by default. Use --all flag on status/dashboard to see them.",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchive,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runArchive(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	cfgPath, _ := cmd.Flags().GetString("config")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	// Verify requirement exists
	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement %s not found: %w", reqID, err)
	}

	// Archive the requirement
	if err := s.Proj.ArchiveRequirement(reqID); err != nil {
		return fmt.Errorf("archive requirement: %w", err)
	}

	// Archive all stories for this requirement
	if err := s.Proj.ArchiveStoriesByReq(reqID); err != nil {
		return fmt.Errorf("archive stories: %w", err)
	}

	// Clean up worktrees and branches for stories
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err == nil {
		repoDir, _ := os.Getwd()
		for _, story := range stories {
			cleanupStoryBranch(repoDir, story)
		}
	}

	// Emit REQ_COMPLETED event with archived status
	evt := state.NewEvent(state.EventReqCompleted, "cli", "", map[string]any{
		"id":     reqID,
		"status": "archived",
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("emit archive event: %w", err)
	}

	fmt.Fprintf(out, "Archived requirement %s (%s) and all its stories.\n", reqID, req.Title)
	return nil
}

// cleanupStoryBranch removes the worktree and branch for a story, if they exist.
func cleanupStoryBranch(repoDir string, story state.Story) {
	if story.Branch == "" {
		return
	}

	// Try to remove worktree (best-effort)
	worktreePath := filepath.Join(repoDir, "repos", story.Branch)
	rmCmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	rmCmd.Dir = repoDir
	rmCmd.Run() //nolint:errcheck // best-effort cleanup

	// Try to remove branch (best-effort)
	brCmd := exec.Command("git", "branch", "-D", story.Branch)
	brCmd.Dir = repoDir
	brCmd.Run() //nolint:errcheck // best-effort cleanup
}
