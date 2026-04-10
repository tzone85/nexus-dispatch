package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
)

func newMergeStoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge <story-id>",
		Short: "Manually merge a story that is ready for merge",
		Args:  cobra.ExactArgs(1),
		RunE:  runMergeStory,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runMergeStory(cmd *cobra.Command, args []string) error {
	storyID := args[0]
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()
	out := cmd.OutOrStdout()

	// Verify story exists and is merge_ready
	story, err := s.Proj.GetStory(storyID)
	if err != nil {
		return fmt.Errorf("story not found: %w", err)
	}
	if story.Status != "merge_ready" {
		return fmt.Errorf("story %s has status %q, expected \"merge_ready\"", storyID, story.Status)
	}

	// Acquire pipeline lock
	stateDir := expandHome(s.Config.Workspace.StateDir)
	lock, err := engine.AcquireLock(stateDir)
	if err != nil {
		return fmt.Errorf("acquire pipeline lock: %w", err)
	}
	defer lock.Release()

	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Create merger (same pattern as resume.go)
	var merger *engine.Merger
	if s.Config.Merge.Mode == "local" {
		localOps := nxdgit.NewLocalMerger(repoDir)
		merger = engine.NewLocalMerger(s.Config.Merge, localOps, s.Events, s.Proj)
	} else if nxdgit.GHAvailable() {
		merger = engine.NewMerger(s.Config.Merge, &ghOpsAdapter{}, s.Events, s.Proj)
	} else {
		return fmt.Errorf("merge mode is %q but gh CLI is not available", s.Config.Merge.Mode)
	}

	result, err := merger.Merge(storyID, story.Title, repoDir, story.Branch)
	if err != nil {
		return fmt.Errorf("merge failed: %w", err)
	}

	fmt.Fprintf(out, "Merged story %s\n", storyID)
	if result.PRURL != "" {
		fmt.Fprintf(out, "  PR: %s (#%d)\n", result.PRURL, result.PRNumber)
	}
	fmt.Fprintf(out, "  Merged: %v\n", result.Merged)
	return nil
}
