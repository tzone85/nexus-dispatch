package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Run garbage collection on branches and worktrees",
		Long: "Deletes the branches of merged stories that were merged more than cleanup.branch_retention_days ago, in each story's requirement repo; " +
			"a leftover worktree checked out on such a branch is removed first. The CLI reference (nxd gc) lists exactly what is and is not touched. " +
			"A real run takes the pipeline lock and refuses while nxd resume is active — it removes worktrees and branches a live run may be working in; " +
			"--dry-run reads only and does not need the lock. " +
			"Use --dry-run to preview without deleting.",
		RunE: runGC,
	}
	cmd.Flags().Bool("dry-run", false, "Preview what would be cleaned up without deleting")
	cmd.SilenceUsage = true
	return cmd
}

// gcNow is the clock the retention cutoff is measured from. A variable so a
// CLI test can pin it: the reaper's own clock is injected (SetNow), and gc is
// the one place that decides what "now" means for a real run.
var gcNow = time.Now

// repoBranches is one requirement repo and the merged-story branches gc
// checks in it. missing is set when the repo directory cannot be read: the
// listing says so, and a real run reports it as an error for that repo.
type repoBranches struct {
	dir      string
	missing  error
	branches []engine.BranchInfo
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

	mergedStories, err := s.Proj.ListStories(state.StoryFilter{Status: "merged"})
	if err != nil {
		return fmt.Errorf("list merged stories: %w", err)
	}
	if len(mergedStories) == 0 {
		fmt.Fprintf(out, "No merged stories found. Nothing to clean up.\n")
		return nil
	}

	groups, total, notes := groupBranchesByRepo(s.Proj, mergedStories)
	if dryRun {
		fmt.Fprintf(out, "Dry run: would check %d branches for cleanup\n", total)
		fmt.Fprintf(out, "Branch retention: %d days\n\n", s.Config.Cleanup.BranchRetentionDays)
		for _, g := range groups {
			for _, b := range g.branches {
				fmt.Fprintf(out, "  %s (story: %s, merged: %s, repo: %s)\n",
					b.Name, b.StoryID, b.MergedAt.Format("2006-01-02"), g.dir)
			}
		}
		printLines(out, notes)
		return nil
	}

	// gc removes worktrees and branches and appends events, so like
	// archive --force it takes the pipeline lock: a live nxd resume may be
	// working in those worktrees and projecting concurrently.
	lock, err := engine.AcquireLock(expandHome(s.Config.Workspace.StateDir))
	if err != nil {
		// Same shape as archive --force: the operator's first question is
		// which process holds it.
		return fmt.Errorf("gc needs the pipeline lock (is nxd resume running?): %w", err)
	}
	defer func() { _ = lock.Release() }()

	// The reaper projects the BRANCH_DELETED / GC_COMPLETED events it appends,
	// so the projection watermark stays level with the log; otherwise the
	// next command's RebuildFrom would replay every archive's
	// REQ_COMPLETED{status:"archived"} as plain "completed".
	reaper := engine.NewReaper(s.Config.Cleanup, &cliGitCleanupOps{out: out}, s.Events, s.Proj)
	reaper.SetNow(gcNow)
	deleted, err := collectRepos(reaper, groups)
	switch {
	case deleted > 0:
		fmt.Fprintf(out, "Cleaned up %d branches.\n", deleted)
	case err == nil: // a run whose deletions all failed must not read as "nothing to do"
		fmt.Fprintf(out, "No branches eligible for cleanup.\n")
	}
	printLines(out, notes)
	return err
}

// groupBranchesByRepo maps merged stories to the repo gc must run git in —
// the story's requirement repo (resolveRepoDir), never whatever directory
// the operator happens to be standing in. A story whose requirement cannot
// be read is skipped with a note. Groups keep first-seen order so the
// dry-run listing is stable; total counts the branches that will be checked.
func groupBranchesByRepo(proj requirementGetter, merged []state.Story) (groups []*repoBranches, total int, notes []string) {
	byDir := map[string]*repoBranches{}
	lookup := newRepoDirCache(proj)
	for _, story := range merged {
		branch := state.StoryBranch(story)
		// A NULL merged_at (pre-migration row) reads as the zero time, which is
		// always before the retention cutoff — skip rather than delete at once,
		// and say so: every other skip in this command is visible, and an
		// operator whose database predates the migration would otherwise read
		// "No branches eligible for cleanup" forever.
		if story.MergedAt.IsZero() {
			notes = append(notes, fmt.Sprintf("  skipped %s (story %s): no recorded merge time", branch, story.ID))
			continue
		}
		dir, dirNotes, err := lookup.dirFor(story.ReqID)
		notes = append(notes, dirNotes...)
		if err != nil {
			notes = append(notes, fmt.Sprintf("  skipped %s (story %s): %v", branch, story.ID, err))
			continue
		}
		g := byDir[dir]
		if g == nil {
			g = &repoBranches{dir: dir}
			if err := checkRepoDir(dir); err != nil {
				g.missing = err
				notes = append(notes, fmt.Sprintf("  repo for requirement %s cannot be checked: %v", story.ReqID, err))
			}
			byDir[dir] = g
			groups = append(groups, g)
		}
		g.branches = append(g.branches, engine.BranchInfo{Name: branch, StoryID: story.ID, MergedAt: story.MergedAt})
		total++
	}
	return groups, total, notes
}

// requirementGetter is the slice of the projection store the repo lookup
// needs, so a table test can drive it without SQLite.
type requirementGetter interface {
	GetRequirement(string) (state.Requirement, error)
}

// repoDirCache answers "which repo does this requirement live in?" once per
// requirement: the answer is the same for every story of it, and so are the
// notes (a cwd fallback, or the reason it could not be answered).
type repoDirCache struct {
	proj  requirementGetter
	dirs  map[string]string
	fails map[string]error
}

func newRepoDirCache(proj requirementGetter) *repoDirCache {
	return &repoDirCache{proj: proj, dirs: map[string]string{}, fails: map[string]error{}}
}

// dirFor returns the requirement's repo directory, the notes its resolution
// produced the first time (empty afterwards), and the reason there is none.
func (c *repoDirCache) dirFor(reqID string) (string, []string, error) {
	if dir, ok := c.dirs[reqID]; ok {
		return dir, nil, nil
	}
	if err, bad := c.fails[reqID]; bad {
		return "", nil, err
	}
	req, err := c.proj.GetRequirement(reqID)
	if err != nil {
		err = fmt.Errorf("requirement %s: %w", reqID, err)
		c.fails[reqID] = err
		return "", nil, err
	}
	dir, err := resolveRepoDir(req)
	if err != nil {
		c.fails[reqID] = err
		return "", nil, err
	}
	c.dirs[reqID] = dir
	return dir, cwdFallbackNotes(req, dir), nil
}

// collectRepos runs the reaper in every repo and keeps going when one fails:
// a repo that no longer exists is an error for that repo (never silently
// "nothing to delete"), and every failure is returned together at the end.
func collectRepos(reaper *engine.Reaper, groups []*repoBranches) (int, error) {
	deleted := 0
	var errs []error
	for _, g := range groups {
		if g.missing != nil {
			errs = append(errs, fmt.Errorf("garbage collect: %w", g.missing))
			continue
		}
		n, err := reaper.GarbageCollect(g.dir, g.branches)
		deleted += n
		if err != nil {
			errs = append(errs, fmt.Errorf("garbage collect in %s: %w", g.dir, err))
		}
	}
	return deleted, errors.Join(errs...)
}

// checkRepoDir reports a directory that gc cannot run git in: missing, or
// present but not a git repository (in which case every BranchExists would
// be false and gc would read as "nothing to do").
func checkRepoDir(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	return nxdgit.IsRepo(dir)
}

func printLines(out io.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}

// cliGitCleanupOps implements engine.GitCleanupOps using real git commands.
type cliGitCleanupOps struct {
	// out is the command's writer, so the one destructive step gc takes
	// outside the branch list — force-removing a leftover worktree — is
	// reported where the operator reads the rest of gc's output. The
	// GitCleanupOps interface returns only an error, and nil out (the unit
	// tests construct one) simply prints nothing.
	out io.Writer
}

func (g *cliGitCleanupOps) notef(format string, a ...any) {
	if g.out == nil {
		return
	}
	fmt.Fprintf(g.out, format, a...)
}

func (g *cliGitCleanupOps) DeleteWorktree(repoDir, worktreePath string) error {
	return nxdgit.DeleteWorktree(repoDir, worktreePath)
}

// DeleteBranch removes a leftover worktree first (git refuses to delete a
// branch that is checked out somewhere, e.g. when the monitor crashed before
// removing a story's worktree), so a leftover worktree never blocks the
// deletion.
func (g *cliGitCleanupOps) DeleteBranch(repoDir, branch string) error {
	wt, err := nxdgit.WorktreeForBranch(repoDir, branch)
	if err != nil {
		return fmt.Errorf("locate worktree for %s: %w", branch, err)
	}
	var wtErr error
	if wt != "" {
		// A worktree that cannot be removed (locked, the main working tree)
		// is why branch -D will fail; keep its error next to that one.
		wtErr = g.DeleteWorktree(repoDir, wt)
		if wtErr == nil {
			// worktree remove --force discards uncommitted edits in that
			// checkout. "Cleaned up N branches." does not say a directory
			// went with them, and archive prints a line per story; this is
			// the equivalent for the one destructive step gc takes.
			g.notef("Removed leftover worktree %s (checked out on %s)\n", wt, branch)
		}
	}
	if err := nxdgit.DeleteBranch(repoDir, branch); err != nil {
		return errors.Join(err, wtErr)
	}
	return nil
}

func (g *cliGitCleanupOps) BranchExists(repoDir, branch string) bool {
	return nxdgit.BranchExists(repoDir, branch)
}
