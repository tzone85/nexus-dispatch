package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive <req-id>",
		Short: "Archive a requirement and all its stories",
		Long: "Marks a requirement as archived. Archived requirements are hidden from the dashboard and status by default. Use --all flag on status/dashboard to see them.\n\n" +
			"Also deletes the git worktree and branch of each MERGED story of the requirement (best-effort, in the requirement's repo). " +
			"Stories that are not merged keep their worktree and branch unless --force is given.\n\n" +
			"Takes the pipeline lock (with or without --force) and refuses while nxd resume is running: it removes worktrees a live run may be working in.",
		Args: cobra.ExactArgs(1),
		RunE: runArchive,
	}
	cmd.Flags().Bool("force", false, "also delete the worktrees and branches of stories that are not merged (unmerged work is lost)")
	cmd.SilenceUsage = true
	return cmd
}

func runArchive(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	cfgPath, _ := cmd.Flags().GetString("config")
	force, _ := cmd.Flags().GetBool("force")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement %s not found: %w", reqID, err)
	}
	// archive removes worktrees and branches and appends events, whether or
	// not --force is given — the merged-only default is the same operation gc
	// refuses to run unlocked, and the archive event lands while nxd resume may
	// be projecting. So the pipeline lock is taken before anything is archived,
	// and a refused run leaves nothing half done.
	lock, err := engine.AcquireLock(expandHome(s.Config.Workspace.StateDir))
	if err != nil {
		return fmt.Errorf("archive needs the pipeline lock (is nxd resume running?): %w", err)
	}
	defer func() { _ = lock.Release() }()
	repoDir, err := resolveRepoDir(req)
	if err != nil {
		return err
	}
	printLines(cmd.OutOrStdout(), cwdFallbackNotes(req, repoDir))
	// Snapshot the stories BEFORE archiving: ArchiveStoriesByReq rewrites
	// every status to "archived", so the merged check must read these rows.
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return fmt.Errorf("list stories for %s: %w", reqID, err)
	}
	if err := archiveRows(s, reqID); err != nil {
		return err
	}
	cleanupArchivedStories(cmd.OutOrStdout(), repoDir, stories, force)
	if err := recordArchived(s, reqID); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Archived requirement %s (%s) and all its stories.\n", reqID, req.Title)
	return nil
}

// archiveRows marks the requirement and all its stories archived in the
// projection (direct writes; recordArchived accounts for them in the log).
func archiveRows(s stores, reqID string) error {
	if err := s.Proj.ArchiveRequirement(reqID); err != nil {
		return fmt.Errorf("archive requirement: %w", err)
	}
	if err := s.Proj.ArchiveStoriesByReq(reqID); err != nil {
		return fmt.Errorf("archive stories: %w", err)
	}
	return nil
}

// cleanupArchivedStories removes the worktree and branch of each story in
// repoDir. Only merged stories are cleaned by default: an unmerged story's
// worktree may hold uncommitted work, and deleting it is irreversible. A
// recorded merge time also counts as merged, so re-running archive on an
// already archived requirement still cleans up. Every story gets one line
// saying what happened (nothing to remove, kept, already removed, could not
// remove, removed); nothing is claimed removed, kept or already gone that
// never existed.
func cleanupArchivedStories(out io.Writer, repoDir string, stories []state.Story, force bool) {
	for _, story := range stories {
		branch := state.StoryBranch(story)
		merged := story.Status == "merged" || !story.MergedAt.IsZero()
		if !merged && !force {
			exists, err := storyBranchExists(repoDir, branch)
			switch {
			case err != nil:
				// Nothing was verified, and --force would not help: say so
				// instead of claiming the worktree was kept.
				fmt.Fprintf(out, "  could not check %s (story %s): %s\n", branch, story.ID, strings.ReplaceAll(err.Error(), "\n", "; "))
			case !exists:
				fmt.Fprintf(out, "  nothing to remove for story %s (%s)\n", story.ID, neverStartedNote(story, branch))
			default:
				fmt.Fprintf(out, "  kept %s (story %s, status %s) — pass --force to delete its worktree and branch\n", branch, story.ID, story.Status)
			}
			continue
		}
		switch err := cleanupStoryBranch(repoDir, story); {
		case errors.Is(err, errAlreadyClean) && !storyStarted(story):
			fmt.Fprintf(out, "  nothing to remove for story %s (%s)\n", story.ID, neverStartedNote(story, branch))
		case errors.Is(err, errAlreadyClean):
			// The monitor removes a story's worktree and branch right after
			// its merge, so this is the normal case for merged stories.
			fmt.Fprintf(out, "  already removed %s (story %s)\n", branch, story.ID)
		case err != nil:
			fmt.Fprintf(out, "  could not remove %s (story %s): %s\n", branch, story.ID, strings.ReplaceAll(err.Error(), "\n", "; "))
		default:
			fmt.Fprintf(out, "  removed worktree and branch %s (story %s)\n", branch, story.ID)
		}
	}
}

// recordArchived appends REQ_COMPLETED{status:"archived"} and advances the
// projection watermark past it. Archive reconciles the projection by direct
// write (archiveRows) rather than by projecting the event, so without the
// ack loadStores would see the projection one event behind the log and
// rebuild on the very next command, replaying REQ_COMPLETED to "completed"
// and undoing the archive.
func recordArchived(s stores, reqID string) error {
	evt := state.NewEvent(state.EventReqCompleted, "cli", "", map[string]any{
		"id":     reqID,
		"status": "archived",
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("emit archive event: %w", err)
	}
	if err := s.Proj.AckDirectWrite(1); err != nil {
		return fmt.Errorf("advance projection watermark: %w", err)
	}
	return nil
}

// storyStarted reports whether a story ever had a branch. STORY_STARTED
// projects the branch, so a non-empty branch (or a recorded merge) is the
// evidence — there is no status list to keep in sync with the pipeline. A
// status list got this wrong in both directions: "split" was missing, so a
// split parent that never started read as started, and a story reset to
// "draft" after a failed review reads as draft while its branch is live.
// Archiving does not clear the branch, so a re-run on an archived story that
// did start still reports "already removed" rather than "nothing to remove".
func storyStarted(story state.Story) bool {
	return story.Branch != "" || !story.MergedAt.IsZero()
}

// neverStartedNote names the branch and, for a story that never started, says
// so. A re-run on an already archived story (its branch removed by the first
// run) must not claim it never started.
func neverStartedNote(story state.Story, branch string) string {
	if story.Status == "archived" {
		return branch
	}
	return branch + " never started"
}

// storyBranchExists reports whether the story's branch exists in repoDir,
// checked out somewhere or not.
func storyBranchExists(repoDir, branch string) (bool, error) {
	wt, err := nxdgit.WorktreeForBranch(repoDir, branch)
	if err != nil {
		return false, err
	}
	return wt != "" || nxdgit.BranchExists(repoDir, branch), nil
}

// errAlreadyClean reports that a story's worktree and branch were already
// gone — the normal state for a merged story, whose worktree and branch the
// monitor removes right after the merge.
var errAlreadyClean = errors.New("worktree and branch already removed")

// cleanupStoryBranch removes the worktree and branch for a story
// (state.StoryBranch), if they exist, and reports what could not be removed
// so the caller never claims a destructive action that did not happen. The
// worktree is located by asking git which checkout has the story's branch
// (nxdgit.WorktreeForBranch); nothing to remove is errAlreadyClean, not a
// failure.
func cleanupStoryBranch(repoDir string, story state.Story) error {
	branch := state.StoryBranch(story)
	worktreePath, err := nxdgit.WorktreeForBranch(repoDir, branch)
	if err != nil {
		return fmt.Errorf("locate worktree for %s in %s: %w", branch, repoDir, err)
	}
	if worktreePath == "" && !nxdgit.BranchExists(repoDir, branch) {
		return errAlreadyClean
	}
	var errs []error
	if worktreePath != "" {
		if err := nxdgit.DeleteWorktree(repoDir, worktreePath); err != nil {
			errs = append(errs, fmt.Errorf("remove worktree %s: %w", worktreePath, err))
		}
	}
	if err := nxdgit.DeleteBranch(repoDir, branch); err != nil {
		errs = append(errs, fmt.Errorf("delete branch %s: %w", branch, err))
	}
	return errors.Join(errs...)
}
