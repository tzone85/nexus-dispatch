package engine

import (
	"log"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// danglingBranchesToClean returns the branch names to remove once a requirement
// completes: the branches of stories that did NOT merge. Merged stories already
// had their branch deleted at merge time; split stories are logical parents with
// no branch of their own; the base branch is never touched. Pure function so the
// selection logic is unit-testable without touching git.
func danglingBranchesToClean(stories []state.Story, baseBranch string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(stories))
	for _, s := range stories {
		if s.Status == "merged" || s.Status == "split" {
			continue
		}
		branch := s.Branch
		if branch == "" {
			// Mirror the dispatcher's canonical branch naming
			// (see Dispatcher.DispatchWave: "nxd/<storyID>"). Using any other
			// prefix here points cleanup at a branch that never existed, so the
			// real dangling branch is silently left behind.
			branch = "nxd/" + s.ID
		}
		if branch == "" || branch == baseBranch || seen[branch] {
			continue
		}
		seen[branch] = true
		out = append(out, branch)
	}
	return out
}

// cleanupDanglingBranches deletes the local + remote branches of a completed
// requirement's non-merged stories. Deleting the remote branch auto-closes any
// open PR, so the workspace is left with no dangling branches or PRs.
// Best-effort: every failure is logged but never fatal (a branch may legitimately
// not exist locally or remotely).
func (m *Monitor) cleanupDanglingBranches(reqID, repoDir string) {
	if !m.config.Cleanup.DeleteDanglingBranches {
		return
	}
	stories, err := m.projStore.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		log.Printf("[cleanup] list stories for %s: %v", reqID, err)
		return
	}
	branches := danglingBranchesToClean(stories, m.config.Merge.BaseBranch)
	if len(branches) == 0 {
		return
	}
	cleaned := 0
	for _, b := range branches {
		if nxdgit.BranchExists(repoDir, b) {
			if delErr := nxdgit.DeleteBranch(repoDir, b); delErr != nil {
				log.Printf("[cleanup] local branch %s not removed: %v", b, delErr)
			}
		}
		if remoteErr := nxdgit.DeleteRemoteBranch(repoDir, b); remoteErr != nil {
			log.Printf("[cleanup] remote branch %s not removed (may not exist): %v", b, remoteErr)
		} else {
			cleaned++
		}
	}
	log.Printf("[cleanup] requirement %s: removed %d dangling remote branch(es) and closed their open PRs", reqID, cleaned)
}
