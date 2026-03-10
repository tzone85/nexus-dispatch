package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Run garbage collection on branches and worktrees",
		Long:  "Removes merged branches that have exceeded the retention period. Use --dry-run to preview without deleting.",
		RunE:  runGC,
	}
	cmd.Flags().Bool("dry-run", false, "Preview what would be cleaned up without deleting")
	cmd.SilenceUsage = true
	return cmd
}

func runGC(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	// Find merged stories to build branch info
	mergedStories, err := s.Proj.ListStories(state.StoryFilter{Status: "merged"})
	if err != nil {
		return fmt.Errorf("list merged stories: %w", err)
	}

	if len(mergedStories) == 0 {
		fmt.Fprintf(out, "No merged stories found. Nothing to clean up.\n")
		return nil
	}

	branches := make([]engine.BranchInfo, 0, len(mergedStories))
	for _, story := range mergedStories {
		if story.Branch == "" {
			continue
		}
		branches = append(branches, engine.BranchInfo{
			Name:     story.Branch,
			StoryID:  story.ID,
			MergedAt: story.CreatedAt,
		})
	}

	if dryRun {
		fmt.Fprintf(out, "Dry run: would check %d branches for cleanup\n", len(branches))
		fmt.Fprintf(out, "Branch retention: %d days\n\n", s.Config.Cleanup.BranchRetentionDays)
		for _, b := range branches {
			fmt.Fprintf(out, "  %s (story: %s, merged: %s)\n",
				b.Name, b.StoryID, b.MergedAt.Format("2006-01-02"))
		}
		return nil
	}

	gitOps := &cliGitCleanupOps{}
	reaper := engine.NewReaper(s.Config.Cleanup, gitOps, s.Events)

	repoDir := "."
	deleted, err := reaper.GarbageCollect(repoDir, branches)
	if err != nil {
		return fmt.Errorf("garbage collect: %w", err)
	}

	if deleted == 0 {
		fmt.Fprintf(out, "No branches eligible for cleanup.\n")
	} else {
		fmt.Fprintf(out, "Cleaned up %d branches.\n", deleted)
	}

	return nil
}

// cliGitCleanupOps implements engine.GitCleanupOps using real git commands.
type cliGitCleanupOps struct{}

func (g *cliGitCleanupOps) DeleteWorktree(repoDir, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = repoDir
	return cmd.Run()
}

func (g *cliGitCleanupOps) DeleteBranch(repoDir, branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoDir
	return cmd.Run()
}

func (g *cliGitCleanupOps) BranchExists(repoDir, branch string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}
