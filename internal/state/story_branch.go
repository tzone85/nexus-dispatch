package state

// CanonicalStoryBranch is the branch name the dispatcher creates for a story
// (Dispatcher.DispatchWave uses it): "nxd/<story-id>". Every other place
// that needs the name derives it from here, so the prefix cannot drift.
func CanonicalStoryBranch(storyID string) string {
	return "nxd/" + storyID
}

// StoryStarted reports whether the projection proves the story ever started:
// a branch persisted from STORY_STARTED, or a recorded merge. There is no
// status list to keep in sync with the pipeline — one got this wrong in both
// directions: "split" was missing, so a split parent that never started read
// as started, and a story reset to "draft" after a failed review reads as
// draft while its branch is live. Archiving does not clear the branch, so a
// re-run on an archived story that did start still reports "already removed"
// rather than "nothing to remove".
func StoryStarted(s Story) bool {
	return s.Branch != "" || !s.MergedAt.IsZero()
}

// StoryBranch is the branch a story's work lives on: the projected branch
// (persisted from STORY_STARTED), or — for a row projected without one, e.g.
// by an older nxd resume that kept running through the upgrade after the
// once-only backfill had already run — the canonical name. Every command
// that runs git against a story's branch (gc, archive, merge, review) and
// the monitor's dangling-branch cleanup use this, so an empty projected
// branch never points cleanup at a branch that does not exist nor skips
// one that does. A story with no ID has no branch.
//
// Displaying it is the exception: the projected branch is also the evidence
// that a story ever started (StoryStarted), so nxd status prints no branch
// for a row that has neither a branch nor a merge time, and nxd review marks
// the name it would act on "(not started)". Both are honest about a row this
// fallback covers; only the commands that RUN git take the fallback silently.
func StoryBranch(s Story) string {
	if s.Branch != "" {
		return s.Branch
	}
	if s.ID == "" {
		return ""
	}
	return CanonicalStoryBranch(s.ID)
}
