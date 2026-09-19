package engine

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// RecoveryAction describes a corrective action taken during crash recovery.
type RecoveryAction struct {
	StoryID     string
	Type        string
	Description string
}

// RunRecovery inspects projection state, the filesystem, and tmux sessions
// for inconsistencies that indicate a prior crash, then fixes them.  It
// returns a slice of every corrective action taken (empty when healthy).
//
// baseBranch is the branch a stuck merge would have landed on — the caller's
// resolved merge base (resolveMergeBase), so a master-only repository
// recovers too; empty falls back to "main".
func RunRecovery(repoDir, baseBranch string, es state.EventStore, ps *state.SQLiteStore) []RecoveryAction {
	if baseBranch == "" {
		baseBranch = "main"
	}
	var actions []RecoveryAction
	actions = append(actions, recoverOrphanedWorktrees(repoDir, ps, es)...)
	actions = append(actions, recoverStuckMerges(repoDir, baseBranch, ps, es)...)
	actions = append(actions, recoverStaleSessions(ps)...)
	return actions
}

// recoverOrphanedWorktrees finds stories marked in_progress or review whose
// worktree directory is missing or invalid, then resets them to draft so the
// pipeline can re-dispatch them.
func recoverOrphanedWorktrees(repoDir string, ps *state.SQLiteStore, es state.EventStore) []RecoveryAction {
	var actions []RecoveryAction

	for _, status := range []string{"in_progress", "review"} {
		stories, err := ps.ListStories(state.StoryFilter{Status: status})
		if err != nil {
			log.Printf("recovery: list %s stories: %v", status, err)
			continue
		}

		for _, story := range stories {
			wtPath := findWorktreePath(repoDir, story.ID)
			if isValidWorktree(wtPath) {
				continue
			}

			evt := state.NewEvent(state.EventStoryRecovery, "", story.ID, map[string]any{
				"new_status": "draft",
				"reason":     fmt.Sprintf("worktree missing for %s story", status),
			})
			if err := es.Append(evt); err != nil {
				log.Printf("recovery: append event for %s: %v", story.ID, err)
				continue
			}
			if err := ps.Project(evt); err != nil {
				log.Printf("recovery: project event for %s: %v", story.ID, err)
				continue
			}

			actions = append(actions, RecoveryAction{
				StoryID:     story.ID,
				Type:        "orphaned_worktree",
				Description: fmt.Sprintf("reset %s -> draft (worktree missing)", status),
			})
		}
	}
	return actions
}

// recoverStuckMerges finds stories in pr_submitted whose branch has already
// been merged into the base branch, then emits STORY_MERGED so the pipeline
// can advance.
func recoverStuckMerges(repoDir, baseBranch string, ps *state.SQLiteStore, es state.EventStore) []RecoveryAction {
	var actions []RecoveryAction

	stories, err := ps.ListStories(state.StoryFilter{Status: "pr_submitted"})
	if err != nil {
		log.Printf("recovery: list pr_submitted stories: %v", err)
		return nil
	}

	for _, story := range stories {
		branch := state.StoryBranch(story)
		if !isBranchMerged(repoDir, baseBranch, branch) {
			continue
		}

		evt := state.NewEvent(state.EventStoryMerged, "", story.ID, map[string]any{
			"source": "recovery",
		})
		if err := es.Append(evt); err != nil {
			log.Printf("recovery: append merged event for %s: %v", story.ID, err)
			continue
		}
		if err := ps.Project(evt); err != nil {
			log.Printf("recovery: project merged event for %s: %v", story.ID, err)
			continue
		}

		actions = append(actions, RecoveryAction{
			StoryID:     story.ID,
			Type:        "stuck_merge",
			Description: "marked as merged (branch already in main)",
		})
	}
	return actions
}

// recoverStaleSessions lists tmux sessions matching the nxd-* prefix and
// kills any whose associated story is already merged.  tmux not being
// installed or having no server running is handled gracefully.
//
// The mapping from session name to story ID is resolved via the agents
// table, which records session_name and current_story_id for each agent.
func recoverStaleSessions(ps *state.SQLiteStore) []RecoveryAction {
	var actions []RecoveryAction

	sessions, err := listNxdTmuxSessions()
	if err != nil {
		// tmux not installed or server not running — nothing to clean up.
		return nil
	}

	if len(sessions) == 0 {
		return nil
	}

	// Build a session -> storyID lookup from the agents table.
	sessionToStory := buildSessionStoryMap(ps)

	for _, sess := range sessions {
		storyID, ok := sessionToStory[sess]
		if !ok || storyID == "" {
			continue
		}

		story, err := ps.GetStory(storyID)
		if err != nil {
			continue
		}
		if story.Status != "merged" {
			continue
		}

		// Best-effort kill; ignore errors.
		_ = exec.Command("tmux", "kill-session", "-t", sess).Run()

		actions = append(actions, RecoveryAction{
			StoryID:     storyID,
			Type:        "stale_session",
			Description: fmt.Sprintf("killed tmux session %s (story already merged)", sess),
		})
	}
	return actions
}

// buildSessionStoryMap queries all agents and returns a map from
// session_name to current_story_id.
func buildSessionStoryMap(ps *state.SQLiteStore) map[string]string {
	m := make(map[string]string)

	agents, err := ps.ListAgents(state.AgentFilter{})
	if err != nil {
		return m
	}
	for _, a := range agents {
		if a.SessionName != "" && a.CurrentStoryID != "" {
			m[a.SessionName] = a.CurrentStoryID
		}
	}
	return m
}

// --------------- helpers ---------------

// findWorktreePath returns the filesystem path for the worktree associated
// with the given story ID by parsing `git worktree list --porcelain`.
func findWorktreePath(repoDir, storyID string) string {
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return ""
	}

	// Parse porcelain output: lines starting with "worktree " give paths.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		wtPath := strings.TrimPrefix(line, "worktree ")
		if strings.HasSuffix(wtPath, "/"+storyID) || strings.HasSuffix(wtPath, string(filepath.Separator)+storyID) {
			return wtPath
		}
	}
	return ""
}

// isValidWorktree reports whether the given path is a usable git worktree.
// A valid worktree has a .git file (not directory) that points to the main
// repo's worktree metadata.
func isValidWorktree(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	// Worktrees use a .git *file*, not a directory.
	return !info.IsDir()
}

// isBranchMerged reports whether the named branch has been merged into base.
// --format, not the display listing: that prefixes the current branch with
// "* " and — since git 2.23 — a branch checked out in a linked worktree with
// "+ ". A story stuck in pr_submitted still has its worktree (the monitor
// removes it after the merge), which is exactly the case this recovers.
func isBranchMerged(repoDir, base, branch string) bool {
	out, err := exec.Command("git", "-C", repoDir, "branch", "--merged", base, "--format=%(refname:short)").Output()
	if err != nil {
		// Most often base does not exist locally (github mode with only
		// origin/main). The stuck merge is then skipped, and an operator
		// looking for why must not have to guess.
		log.Printf("[recovery] cannot tell whether %s is merged into %s in %s: %v — leaving the story alone", branch, base, repoDir, err)
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == branch {
			return true
		}
	}
	return false
}

// listNxdTmuxSessions returns session names that start with "nxd-".
func listNxdTmuxSessions() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil, err
	}

	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "nxd-") {
			sessions = append(sessions, name)
		}
	}
	return sessions, nil
}
