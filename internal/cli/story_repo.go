package cli

import (
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// storyRepo resolves where a story's git operations run and which branch
// they merge into: the story's requirement repo (resolveRepoDir — resume
// itself runs in the cwd, which only coincides with it when resume is run
// from the repo the requirement was submitted in) and merge.base_branch,
// resolved exactly as resume resolves it (resolveMergeBase). The returned
// config is a copy; s.Config is never mutated. notes is the cwd-fallback
// line (cwdFallbackNotes) for the caller to print, so a merge into the
// current directory is never silent.
func storyRepo(s stores, story state.Story) (repoDir string, mergeCfg config.MergeConfig, notes []string, err error) {
	req, err := s.Proj.GetRequirement(story.ReqID)
	if err != nil {
		return "", config.MergeConfig{}, nil, fmt.Errorf("requirement %s for story %s: %w", story.ReqID, story.ID, err)
	}
	repoDir, err = resolveRepoDir(req)
	if err != nil {
		return "", config.MergeConfig{}, nil, err
	}
	return repoDir, resolveMergeBase(s.Config.Merge, repoDir), cwdFallbackNotes(req, repoDir), nil
}

// resolveMergeBase fills an empty merge.base_branch from the repo's default
// branch (origin/HEAD, then main, then master; see git.DetectDefaultBranch).
// It is the one rule for merge, review, resume and the completion gate, so
// local mode never reaches `git checkout ""` and github mode never opens a
// PR against an empty base.
func resolveMergeBase(cfg config.MergeConfig, repoDir string) config.MergeConfig {
	if cfg.BaseBranch == "" {
		cfg.BaseBranch = nxdgit.DetectDefaultBranch(repoDir)
	}
	return cfg
}
