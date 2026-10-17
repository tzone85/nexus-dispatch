package cli

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/state"
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
	// Resolve the repo and base branch before printing anything, so a
	// requirement that cannot be read fails cleanly rather than after a
	// half-printed report.
	repoDir, mergeCfg, notes, err := storyRepo(s, story)
	if err != nil {
		return err
	}
	printLines(out, notes)
	branch := state.StoryBranch(story)
	// nxd status prints no branch at all for a row with no evidence it
	// started. Saying the name and that the story never started agrees with
	// it, and still tells the operator what review would act on.
	shown := branch
	if !state.StoryStarted(story) {
		shown = branch + " (not started)"
	}

	fmt.Fprintf(out, "Story: %s\nID: %s\nStatus: %s\nComplexity: %d\nBranch: %s\n\n",
		story.Title, story.ID, story.Status, story.Complexity, shown)

	// Surface the story's intent so a human reviewing the change can read the
	// description and acceptance criteria as a clean bulleted list rather than
	// a run-on technical blob.
	if desc := strings.TrimSpace(story.Description); desc != "" {
		fmt.Fprintf(out, "Description:\n%s\n\n", desc)
	}
	if ac := criteria.FormatMarkdown(story.AcceptanceCriteria); ac != "" {
		fmt.Fprintf(out, "Acceptance Criteria:\n%s\n\n", ac)
	}

	// Diff in the story's requirement repo against the resolved base branch
	// (the same resolution nxd merge uses); a git failure is printed, never
	// swallowed, so a wrong directory is visible instead of an empty
	// "Changes" section. A story that has not started has no branch yet;
	// a merged story's branch is removed right after the merge; a repo that
	// cannot be checked is neither — say which, instead of git noise or a
	// false "none yet".
	exists, err := storyBranchExists(repoDir, branch)
	switch {
	case err != nil:
		fmt.Fprintf(out, "Changes: unavailable (%s)\n\n", oneLine(err.Error()))
	case !exists && state.StoryStarted(story):
		fmt.Fprintf(out, "Changes: branch %s no longer exists (removed after merge or cleanup)\n\n", branch)
	case !exists:
		fmt.Fprintf(out, "Changes: none yet (branch %s does not exist)\n\n", branch)
	default:
		printStoryDiff(out, repoDir, mergeCfg.BaseBranch, branch)
	}

	if story.Status == "merge_ready" {
		fmt.Fprintf(out, "Actions:\n")
		fmt.Fprintf(out, "  nxd merge %s    # merge this story\n", storyID)
	}
	return nil
}

// printStoryDiff prints the diff stat and the full diff of branch against
// base in repoDir; each git failure is printed as "unavailable" with git's
// message.
func printStoryDiff(out io.Writer, repoDir, base, branch string) {
	for _, section := range []struct {
		title string
		args  []string
	}{
		{"Changes", []string{"diff", base + "..." + branch, "--stat"}},
		{"Diff", []string{"diff", base + "..." + branch}},
	} {
		cmd := exec.Command("git", section.args...)
		cmd.Dir = repoDir
		gitOut, err := cmd.CombinedOutput()
		switch {
		case err != nil:
			fmt.Fprintf(out, "%s: unavailable (%v: %s)\n", section.title, err, oneLine(string(gitOut)))
		case len(gitOut) > 0:
			fmt.Fprintf(out, "%s:\n%s\n", section.title, string(gitOut))
		}
	}
}
