package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newReviewStoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review <story-id>",
		Short: "Review a story's changes before merge",
		Args:  cobra.ExactArgs(1),
		RunE:  runReviewStory,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runReviewStory(cmd *cobra.Command, args []string) error {
	storyID := args[0]
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()
	out := cmd.OutOrStdout()

	story, err := s.Proj.GetStory(storyID)
	if err != nil {
		return fmt.Errorf("story not found: %w", err)
	}

	fmt.Fprintf(out, "Story: %s\nID: %s\nStatus: %s\nComplexity: %d\nBranch: %s\n\n",
		story.Title, story.ID, story.Status, story.Complexity, story.Branch)

	if story.Branch != "" {
		repoDir, _ := os.Getwd()
		baseBranch := s.Config.Merge.BaseBranch

		// Diff stats
		statCmd := exec.Command("git", "diff", baseBranch+"..."+story.Branch, "--stat")
		statCmd.Dir = repoDir
		if statOut, err := statCmd.CombinedOutput(); err == nil && len(statOut) > 0 {
			fmt.Fprintf(out, "Changes:\n%s\n", string(statOut))
		}

		// Full diff
		diffCmd := exec.Command("git", "diff", baseBranch+"..."+story.Branch)
		diffCmd.Dir = repoDir
		if diffOut, err := diffCmd.CombinedOutput(); err == nil && len(diffOut) > 0 {
			fmt.Fprintf(out, "Diff:\n%s\n", string(diffOut))
		}
	}

	if story.Status == "merge_ready" {
		fmt.Fprintf(out, "Actions:\n")
		fmt.Fprintf(out, "  nxd merge %s    # merge this story\n", storyID)
	}
	return nil
}
