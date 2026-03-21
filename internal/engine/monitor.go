package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Monitor polls running agents and progresses completed stories through
// review, QA, and merge.
type Monitor struct {
	registry         *runtime.Registry
	watchdog         *Watchdog
	reviewer         *Reviewer
	qa               *QA
	merger           *Merger
	conflictResolver *ConflictResolver
	config           config.Config
	eventStore       state.EventStore
	projStore        state.ProjectionStore

	// dispatcher + executor allow the monitor to automatically spawn the
	// next wave of stories after merges complete, removing the need for
	// the user to manually run "nxd resume" between waves.
	dispatcher *Dispatcher
	executor   *Executor

	// mergeMu serializes the rebase-push-merge cycle so that each story
	// rebases onto the latest main before merging, preventing conflicts
	// when parallel agents touch the same files.
	mergeMu sync.Mutex
}

// NewMonitor creates a Monitor wired to all pipeline components.
func NewMonitor(
	reg *runtime.Registry,
	wd *Watchdog,
	rev *Reviewer,
	qa *QA,
	merger *Merger,
	cfg config.Config,
	es state.EventStore,
	ps state.ProjectionStore,
) *Monitor {
	return &Monitor{
		registry:   reg,
		watchdog:   wd,
		reviewer:   rev,
		qa:         qa,
		merger:     merger,
		config:     cfg,
		eventStore: es,
		projStore:  ps,
	}
}

// SetConflictResolver enables LLM-based automatic conflict resolution during
// rebase. Without this, rebase conflicts cause the story to be reset to draft.
func (m *Monitor) SetConflictResolver(cr *ConflictResolver) {
	m.conflictResolver = cr
}

// SetAutoResume enables automatic dispatch of the next wave when stories
// complete. Without this, the monitor exits after one wave and the user
// must manually run "nxd resume".
func (m *Monitor) SetAutoResume(d *Dispatcher, e *Executor) {
	m.dispatcher = d
	m.executor = e
}

// RunContext carries the state needed for auto-resume across waves.
type RunContext struct {
	ReqID          string
	PlannedStories []PlannedStory
	DAG            *graph.DAG
	WaveNumber     int
}

// Run polls active agents at the configured interval until all are done
// or the context is cancelled. When all agents finish naturally, Run waits
// for their post-execution pipelines (review, QA, merge) to complete.
// If auto-resume is enabled (SetAutoResume was called), Run then dispatches
// the next wave of ready stories and continues monitoring. This repeats
// until all stories are complete or context is cancelled.
func (m *Monitor) Run(ctx context.Context, agents []ActiveAgent, repoDir string) error {
	return m.RunWithContext(ctx, agents, repoDir, nil)
}

// RunWithContext is like Run but accepts a RunContext for auto-resume.
func (m *Monitor) RunWithContext(ctx context.Context, agents []ActiveAgent, repoDir string, rc *RunContext) error {
	pollInterval := time.Duration(m.config.Monitor.PollIntervalMs) * time.Millisecond
	if pollInterval == 0 {
		pollInterval = 10 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var pipelineWG sync.WaitGroup

	active := make(map[string]ActiveAgent, len(agents))
	for _, a := range agents {
		active[a.Assignment.SessionName] = a
	}

	log.Printf("[monitor] tracking %d agents, polling every %s", len(active), pollInterval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[monitor] context cancelled, %d agents still active", len(active))
			return nil // graceful detach, agents continue in tmux
		case <-ticker.C:
			if len(active) == 0 {
				log.Printf("[monitor] all agents finished, waiting for post-execution pipelines")
				pipelineWG.Wait()
				log.Printf("[monitor] all pipelines complete")

				// Auto-resume: dispatch next wave if possible.
				if rc != nil && m.dispatcher != nil && m.executor != nil {
					newAgents := m.dispatchNextWave(ctx, rc, repoDir)
					if len(newAgents) > 0 {
						for _, a := range newAgents {
							active[a.Assignment.SessionName] = a
						}
						log.Printf("[monitor] auto-resumed: tracking %d new agents", len(newAgents))
						continue
					}
				}

				return nil
			}

			m.pollOnce(ctx, &pipelineWG, active, repoDir)
		}
	}
}

// pollOnce performs a single pass over active agents, checking status and
// kicking off post-execution pipelines for any that have finished.
func (m *Monitor) pollOnce(ctx context.Context, wg *sync.WaitGroup, active map[string]ActiveAgent, repoDir string) {
	for sessionName, ag := range active {
		rt, err := m.registry.Get(ag.RuntimeName)
		if err != nil {
			continue
		}

		// Watchdog check (handles permission prompts, stuck detection)
		m.watchdog.Check(sessionName, rt)

		// Check if agent is done
		status, err := rt.DetectStatus(sessionName)
		if err != nil {
			log.Printf("[monitor] %s status check error: %v", ag.Assignment.StoryID, err)
			continue
		}

		log.Printf("[monitor] %s: %s", ag.Assignment.StoryID, status)

		if status != runtime.StatusDone && status != runtime.StatusTerminated {
			continue
		}

		log.Printf("[monitor] agent %s finished (status: %s)", ag.Assignment.AgentID, status)

		// Emit story completed
		completedEvt := state.NewEvent(
			state.EventStoryCompleted,
			ag.Assignment.AgentID,
			ag.Assignment.StoryID,
			map[string]any{
				"status": status.String(),
			},
		)
		if err := m.eventStore.Append(completedEvt); err != nil {
			log.Printf("[monitor] failed to append completed event for %s: %v", ag.Assignment.StoryID, err)
		}
		if err := m.projStore.Project(completedEvt); err != nil {
			log.Printf("[monitor] failed to project completed event for %s: %v", ag.Assignment.StoryID, err)
		}

		// Drive post-execution pipeline
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.postExecutionPipeline(ctx, ag, repoDir)
		}()

		// Remove from active tracking
		m.watchdog.ClearFingerprint(sessionName)
		delete(active, sessionName)

		log.Printf("[monitor] %d agents remaining", len(active))
	}
}

// postExecutionPipeline runs review, QA, and merge for a completed story.
func (m *Monitor) postExecutionPipeline(ctx context.Context, ag ActiveAgent, repoDir string) {
	storyID := ag.Assignment.StoryID
	branch := ag.Assignment.Branch

	log.Printf("[pipeline] starting post-execution for %s", storyID)

	// Auto-commit any uncommitted work left by the agent.
	// Agents frequently exit without committing their changes,
	// especially in -p (prompt) mode. This safety net ensures we capture
	// the work before checking the diff.
	autoCommit(ag.WorktreePath, storyID)

	// Check if agent produced any changes
	diff, err := gitDiff(ag.WorktreePath)
	if err != nil {
		log.Printf("[pipeline] git diff error for %s: %v", storyID, err)
	}
	if diff == "" {
		log.Printf("[pipeline] no changes produced for %s, resetting to draft for re-dispatch", storyID)
		// Reset story status so it can be re-dispatched in a future wave.
		resetEvt := state.NewEvent(state.EventStoryReviewFailed, "monitor", storyID, map[string]any{
			"reason": "agent produced no code changes",
		})
		m.eventStore.Append(resetEvt)
		m.projStore.Project(resetEvt)
		return
	}

	// 1. Code Review
	if m.reviewer != nil {
		// Look up story details for the reviewer
		storyTitle := storyID
		storyAC := ""
		if story, err := m.projStore.GetStory(storyID); err == nil {
			storyTitle = story.Title
			storyAC = story.AcceptanceCriteria
		}

		result, err := m.reviewer.Review(ctx, storyID, storyTitle, storyAC, diff)
		if err != nil {
			// Fatal API errors (auth failures, billing exhaustion,
			// permission denied) will never succeed on retry — pause
			// the entire requirement to stop the infinite loop.
			if llm.IsFatalAPIError(err) {
				log.Printf("[pipeline] FATAL: non-retryable API error — pausing requirement for %s: %v", storyID, err)
				m.pauseRequirement(storyID, fmt.Sprintf("fatal API error: %v", err))
				return
			}
			log.Printf("[pipeline] review error for %s: %v", storyID, err)
			m.resetStoryToDraft(storyID, "reviewer", fmt.Sprintf("review error: %v", err))
			return
		}
		if !result.Passed {
			m.handleReviewFailure(storyID, result)
			return
		}
		log.Printf("[pipeline] review passed for %s", storyID)
	}

	// 2. QA
	if m.qa != nil {
		result, err := m.qa.Run(ctx, storyID, ag.WorktreePath)
		if err != nil {
			log.Printf("[pipeline] QA error for %s: %v", storyID, err)
			m.resetStoryToDraft(storyID, "qa", fmt.Sprintf("QA error: %v", err))
			return
		}
		if !result.Passed {
			log.Printf("[pipeline] QA failed for %s", storyID)
			failEvt := state.NewEvent(state.EventStoryReviewFailed, "qa", storyID, map[string]any{
				"reason": "QA checks failed",
			})
			m.eventStore.Append(failEvt)
			m.projStore.Project(failEvt)
			return
		}
		log.Printf("[pipeline] QA passed for %s", storyID)
	}

	// 3. Merge (serialized: rebase onto latest main, then push + merge)
	if m.merger != nil {
		m.mergeMu.Lock()
		result, err := m.rebaseAndMerge(ctx, storyID, branch, repoDir, ag.WorktreePath)
		m.mergeMu.Unlock()

		if err != nil {
			if llm.IsFatalAPIError(err) {
				log.Printf("[pipeline] FATAL: non-retryable API error during merge for %s: %v", storyID, err)
				m.pauseRequirement(storyID, fmt.Sprintf("fatal API error during merge: %v", err))
				return
			}
			log.Printf("[pipeline] merge error for %s: %v", storyID, err)
			m.resetStoryToDraft(storyID, "merger", fmt.Sprintf("merge/rebase error: %v", err))
			return
		}
		log.Printf("[pipeline] %s -> PR #%d (%s) merged=%v",
			storyID, result.PRNumber, result.PRURL, result.Merged)

		// Clean up worktree and branches after successful merge.
		if result.Merged {
			if err := nxdgit.RemoveWorktree(repoDir, ag.WorktreePath, branch); err != nil {
				log.Printf("[pipeline] worktree cleanup for %s: %v", storyID, err)
			}
			if err := nxdgit.DeleteRemoteBranch(repoDir, branch); err != nil {
				log.Printf("[pipeline] remote branch cleanup for %s: %v", storyID, err)
			}
		}
	}

	// 4. Check if requirement is paused before next wave dispatch
	if m.isRequirementPaused(storyID) {
		log.Printf("[pipeline] requirement for %s is paused, skipping next wave dispatch", storyID)
		return
	}

	log.Printf("[pipeline] post-execution complete for %s, next wave can be dispatched", storyID)
}

// rebaseAndMerge fetches the latest base branch, rebases the worktree onto
// it, then delegates to the merger for push + PR + auto-merge. This must be
// called while holding mergeMu so that each story sees the result of any
// prior merge before rebasing.
//
// If a ConflictResolver is configured, rebase conflicts are automatically
// resolved via LLM instead of failing immediately.
func (m *Monitor) rebaseAndMerge(ctx context.Context, storyID, branch, repoDir, worktreePath string) (MergeResult, error) {
	baseBranch := m.config.Merge.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	log.Printf("[pipeline] fetching %s and rebasing %s for %s", baseBranch, branch, storyID)

	if err := nxdgit.FetchBranch(repoDir, baseBranch); err != nil {
		return MergeResult{}, fmt.Errorf("fetch %s: %w", baseBranch, err)
	}

	upstream := "origin/" + baseBranch

	if m.conflictResolver != nil {
		// Use LLM-powered conflict resolution during rebase.
		if err := m.conflictResolver.RebaseWithResolution(ctx, storyID, worktreePath, upstream); err != nil {
			return MergeResult{}, fmt.Errorf("rebase onto %s: %w", baseBranch, err)
		}
	} else {
		// Fall back to the original abort-on-conflict behavior.
		if err := nxdgit.RebaseOnto(worktreePath, upstream); err != nil {
			return MergeResult{}, fmt.Errorf("rebase onto %s: %w", baseBranch, err)
		}
	}

	log.Printf("[pipeline] rebase succeeded for %s, proceeding to merge", storyID)

	return m.merger.Merge(storyID, storyID, repoDir, branch)
}

// isRequirementPaused looks up the requirement for a story and returns true
// if it is in the "paused" state.
func (m *Monitor) isRequirementPaused(storyID string) bool {
	story, err := m.projStore.GetStory(storyID)
	if err != nil {
		log.Printf("[monitor] failed to get story %s for pause check: %v", storyID, err)
		return false
	}

	req, err := m.projStore.GetRequirement(story.ReqID)
	if err != nil {
		log.Printf("[monitor] failed to get requirement %s for pause check: %v", story.ReqID, err)
		return false
	}

	return req.Status == "paused"
}

// resetStoryToDraft emits a STORY_REVIEW_FAILED event to move a story back
// to "draft" so it can be re-dispatched. This handles error paths (review
// errors, QA errors, rebase conflicts) that would otherwise leave the story
// stuck in an intermediate status.
//
// Before resetting, it checks how many times this story has already been
// reset. If the count exceeds the configured max_retries_before_escalation,
// the entire requirement is paused instead of resetting — preventing infinite
// retry loops that drain credits.
func (m *Monitor) resetStoryToDraft(storyID, fromAgent, reason string) {
	// Count existing STORY_REVIEW_FAILED events for this story to detect
	// infinite retry loops (e.g. persistent merge conflicts, repeated QA
	// failures, credit exhaustion).
	maxRetries := m.config.Routing.MaxRetriesBeforeEscalation
	if maxRetries <= 0 {
		maxRetries = 3
	}

	resetCount, err := m.eventStore.Count(state.EventFilter{
		Type:    state.EventStoryReviewFailed,
		StoryID: storyID,
	})
	if err != nil {
		log.Printf("[pipeline] failed to count reset events for %s: %v", storyID, err)
		// On error, proceed with reset rather than silently pausing.
	} else if resetCount >= maxRetries {
		log.Printf("[pipeline] story %s exceeded max retries (%d), pausing requirement", storyID, maxRetries)
		m.pauseRequirement(storyID, fmt.Sprintf(
			"story exceeded max retries (%d/%d): %s", resetCount, maxRetries, reason,
		))
		return
	}

	evt := state.NewEvent(state.EventStoryReviewFailed, fromAgent, storyID, map[string]any{
		"reason": reason,
	})
	if err := m.eventStore.Append(evt); err != nil {
		log.Printf("[pipeline] failed to append reset event for %s: %v", storyID, err)
	}
	if err := m.projStore.Project(evt); err != nil {
		log.Printf("[pipeline] failed to project reset event for %s: %v", storyID, err)
	}
	log.Printf("[pipeline] reset %s to draft (attempt %d/%d): %s", storyID, resetCount+1, maxRetries, reason)
}

// pauseRequirement pauses the entire requirement that owns the given story.
// This is used when a fatal, non-retryable error (e.g. billing exhaustion)
// makes further progress impossible. The user must resolve the issue and
// run "nxd resume" to continue.
func (m *Monitor) pauseRequirement(storyID, reason string) {
	story, err := m.projStore.GetStory(storyID)
	if err != nil {
		log.Printf("[pipeline] cannot pause: failed to look up story %s: %v", storyID, err)
		return
	}

	pauseEvt := state.NewEvent(state.EventReqPaused, "monitor", "", map[string]any{
		"id":     story.ReqID,
		"reason": reason,
	})
	if err := m.eventStore.Append(pauseEvt); err != nil {
		log.Printf("[pipeline] failed to append pause event for req %s: %v", story.ReqID, err)
	}
	if err := m.projStore.Project(pauseEvt); err != nil {
		log.Printf("[pipeline] failed to project pause event for req %s: %v", story.ReqID, err)
	}
	log.Printf("[pipeline] requirement %s paused: %s", story.ReqID, reason)
	log.Printf("[pipeline] resolve the issue and run 'nxd resume %s' to continue", story.ReqID)
}

// handleReviewFailure implements retry-with-feedback and senior escalation
// for code review rejections. On the first failure, the story is reset to
// draft so the same agent can retry with feedback attached. On the second
// failure, the story is escalated to a senior agent. On the third failure
// (senior also fails), the requirement is paused for human intervention.
//
// Note: by the time this method is called, the Reviewer has already emitted
// one STORY_REVIEW_FAILED event (agent_id="reviewer") for this rejection.
func (m *Monitor) handleReviewFailure(storyID string, result ReviewResult) {
	// Count review-specific failures (emitted by the Reviewer, agent_id="reviewer").
	reviewFailCount, err := m.eventStore.Count(state.EventFilter{
		Type:    state.EventStoryReviewFailed,
		AgentID: "reviewer",
		StoryID: storyID,
	})
	if err != nil {
		log.Printf("[pipeline] failed to count review failures for %s: %v", storyID, err)
		reviewFailCount = 1 // assume first failure on error
	}

	// Marshal review comments for the event payload.
	commentsJSON := marshalReviewComments(result.Comments)

	switch {
	case reviewFailCount <= 1:
		// First failure: reset to draft with feedback so the dispatcher
		// re-dispatches the same agent with review comments attached.
		log.Printf("[pipeline] review rejected %s (attempt 1), will retry with feedback", storyID)
		evt := state.NewEvent(state.EventStoryReviewFailed, "monitor", storyID, map[string]any{
			"reason":   "review rejected",
			"feedback": result.Summary,
			"comments": commentsJSON,
		})
		m.eventStore.Append(evt)
		m.projStore.Project(evt)

	case reviewFailCount == 2:
		// Second failure: retry also failed — escalate to senior agent.
		log.Printf("[pipeline] review rejected %s (attempt 2), escalating to senior", storyID)

		// Emit escalation event.
		escEvt := state.NewEvent(state.EventEscalationCreated, "monitor", storyID, map[string]any{
			"reason":   "review failed twice",
			"feedback": result.Summary,
			"comments": commentsJSON,
		})
		m.eventStore.Append(escEvt)
		m.projStore.Project(escEvt)

		// Reset to draft so the dispatcher picks it up and routes to senior.
		resetEvt := state.NewEvent(state.EventStoryReviewFailed, "monitor", storyID, map[string]any{
			"reason":   "review rejected, escalating to senior",
			"feedback": result.Summary,
			"comments": commentsJSON,
		})
		m.eventStore.Append(resetEvt)
		m.projStore.Project(resetEvt)

	default:
		// Third+ failure (senior also failed): pause the requirement.
		log.Printf("[pipeline] review rejected %s (attempt 3+), pausing requirement", storyID)
		m.pauseRequirement(storyID, fmt.Sprintf(
			"review failed %d times (including senior escalation): %s",
			reviewFailCount, result.Summary,
		))
	}
}

// marshalReviewComments serializes review comments to a JSON string for
// storage in event payloads.
func marshalReviewComments(comments []ReviewComment) string {
	if len(comments) == 0 {
		return "[]"
	}
	data, err := json.Marshal(comments)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// dispatchNextWave determines which stories are now ready (dependencies met)
// and dispatches a new wave of agents. Returns the newly spawned ActiveAgents.
func (m *Monitor) dispatchNextWave(ctx context.Context, rc *RunContext, repoDir string) []ActiveAgent {
	// Bail out immediately if the requirement has been paused (e.g. by
	// billing exhaustion in a prior pipeline). Without this check, the
	// monitor would re-dispatch the same story in an infinite loop.
	if req, err := m.projStore.GetRequirement(rc.ReqID); err == nil && req.Status == "paused" {
		log.Printf("[auto-resume] requirement %s is paused, stopping auto-resume", rc.ReqID)
		return nil
	}

	// Build completed set from the projection store.
	stories, err := m.projStore.ListStories(state.StoryFilter{ReqID: rc.ReqID})
	if err != nil {
		log.Printf("[auto-resume] failed to list stories: %v", err)
		return nil
	}

	completed := make(map[string]bool)
	allDone := true
	for _, s := range stories {
		if s.Status == "merged" || s.Status == "pr_submitted" {
			completed[s.ID] = true
		} else {
			allDone = false
		}
	}

	if allDone {
		log.Printf("[auto-resume] all %d stories complete for requirement %s", len(stories), rc.ReqID)
		// Mark requirement complete.
		compEvt := state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": rc.ReqID})
		m.eventStore.Append(compEvt)
		m.projStore.Project(compEvt)
		return nil
	}

	rc.WaveNumber++
	assignments, err := m.dispatcher.DispatchWave(rc.DAG, completed, rc.ReqID, rc.PlannedStories, rc.WaveNumber)
	if err != nil {
		log.Printf("[auto-resume] dispatch error: %v", err)
		return nil
	}
	if len(assignments) == 0 {
		log.Printf("[auto-resume] no stories ready for next wave (dependencies not met)")
		return nil
	}

	log.Printf("[auto-resume] dispatching %d stories in next wave", len(assignments))

	storyMap := make(map[string]PlannedStory, len(rc.PlannedStories))
	for _, ps := range rc.PlannedStories {
		storyMap[ps.ID] = ps
	}

	results := m.executor.SpawnAll(repoDir, assignments, storyMap)

	var active []ActiveAgent
	for _, r := range results {
		if r.Error != nil {
			log.Printf("[auto-resume] spawn error for %s: %v", r.Assignment.StoryID, r.Error)
			continue
		}
		log.Printf("[auto-resume] spawned %s -> %s (session: %s)",
			r.Assignment.StoryID, r.RuntimeName, r.Assignment.SessionName)
		active = append(active, ActiveAgent{
			Assignment:   r.Assignment,
			WorktreePath: r.WorktreePath,
			RuntimeName:  r.RuntimeName,
		})
	}

	return active
}

// autoCommit stages and commits any uncommitted changes in the worktree.
// This is a safety net for agents that produce code but exit without
// committing. NXD artifacts (.nxd-prompts, CLAUDE.md, .serena) are excluded.
func autoCommit(worktreePath, storyID string) {
	// Check for uncommitted changes (staged or unstaged).
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = worktreePath
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(statusOut))) == 0 {
		return // nothing to commit
	}

	log.Printf("[pipeline] auto-committing uncommitted work for %s", storyID)

	// Ensure NXD artifacts are in .gitignore so they are never committed.
	ensureGitignorePatterns(worktreePath)

	// Stage all non-ignored changes.
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = worktreePath
	if out, err := addCmd.CombinedOutput(); err != nil {
		log.Printf("[pipeline] git add failed for %s: %v (%s)", storyID, err, strings.TrimSpace(string(out)))
		return
	}

	// Commit with a descriptive message.
	commitCmd := exec.Command("git", "commit", "-m",
		fmt.Sprintf("feat(%s): auto-commit agent work\n\nNXD auto-committed changes that the agent left uncommitted.", storyID))
	commitCmd.Dir = worktreePath
	if out, err := commitCmd.CombinedOutput(); err != nil {
		log.Printf("[pipeline] auto-commit failed for %s: %v (%s)", storyID, err, strings.TrimSpace(string(out)))
		return
	}

	log.Printf("[pipeline] auto-commit succeeded for %s", storyID)
}

// ensureGitignorePatterns appends NXD artifact patterns to .gitignore if
// they are not already present, preventing CLAUDE.md, .nxd-prompts/,
// .serena/, and other tool artifacts from being committed by agents.
func ensureGitignorePatterns(worktreePath string) {
	nxdPatterns := []string{
		"CLAUDE.md",
		".nxd-prompts/",
		".serena/",
	}

	giPath := worktreePath + "/.gitignore"
	existing, _ := os.ReadFile(giPath)
	content := string(existing)

	var toAdd []string
	for _, pat := range nxdPatterns {
		if !strings.Contains(content, pat) {
			toAdd = append(toAdd, pat)
		}
	}
	if len(toAdd) == 0 {
		return
	}

	appendix := "\n# NXD agent artifacts (auto-added)\n" + strings.Join(toAdd, "\n") + "\n"
	os.WriteFile(giPath, append(existing, []byte(appendix)...), 0o644)
}

// gitDiff returns the git diff for committed changes in a worktree.
func gitDiff(worktreePath string) (string, error) {
	cmd := exec.Command("git", "diff", "HEAD~1")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If HEAD~1 fails (single commit), diff against empty tree
		emptyTree := "4b825dc642cb6eb9a060e54bf899d69f7cb46a0"
		cmd2 := exec.Command("git", "diff", emptyTree, "HEAD")
		cmd2.Dir = worktreePath
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("git diff fallback: %w", err2)
		}
		return string(out2), nil
	}
	return string(out), nil
}
