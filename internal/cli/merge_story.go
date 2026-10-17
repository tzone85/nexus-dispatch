package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
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
	defer func() { _ = lock.Release() }()

	// The story's requirement repo and the resolved base branch, as resume
	// uses them — not the cwd and not a raw (possibly empty) config value.
	repoDir, mergeCfg, notes, err := storyRepo(s, story)
	if err != nil {
		return err
	}
	printLines(out, notes)

	merger, err := newStoryMerger(s, mergeCfg, repoDir)
	if err != nil {
		return err
	}
	result, err := merger.Merge(storyID, story.Title, repoDir, state.StoryBranch(story))
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

// newStoryMerger builds the merger for the configured mode (same pattern as
// resume.go): a local git merge, or gh-backed PRs when gh is available.
func newStoryMerger(s stores, mergeCfg config.MergeConfig, repoDir string) (*engine.Merger, error) {
	if mergeCfg.Mode == "local" {
		return engine.NewLocalMerger(mergeCfg, nxdgit.NewLocalMerger(repoDir), s.Events, s.Proj), nil
	}
	if !nxdgit.GHAvailable() {
		return nil, fmt.Errorf("merge mode is %q but gh CLI is not available", mergeCfg.Mode)
	}
	return engine.NewMerger(mergeCfg, &ghOpsAdapter{}, s.Events, s.Proj), nil
}
