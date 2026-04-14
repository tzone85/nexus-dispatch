package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/codegraph"
	"github.com/tzone85/nexus-dispatch/internal/config"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/memory"
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
	escalation       *EscalationMachine
	manager          *Manager

	// mempalace provides semantic memory for mining diffs, review
	// feedback, and QA failures so future agent runs benefit from
	// accumulated project knowledge.
	mempalace *memory.MemPalace

	// artifactStore persists per-story artifacts (diffs, review results,
	// QA results) for post-mortem inspection.
	artifactStore *artifact.Store

	// codeGraph enables blast-radius analysis before code review.
	codeGraph *codegraph.Runner

	// planner enables tier-3 (tech lead) re-planning. When set, the
	// monitor can decompose failing stories into smaller replacements.
	planner *Planner

	// dispatcher + executor allow the monitor to automatically spawn the
	// next wave of stories after merges complete, removing the need for
	// the user to manually run "nxd resume" between waves.
	dispatcher *Dispatcher
	executor   *Executor

	// dryRun causes the post-execution pipeline to simulate a successful
	// agent diff instead of checking the real worktree.
	dryRun bool

	// mergeMu serializes the rebase-push-merge cycle so that each story
	// rebases onto the latest main before merging, preventing conflicts
	// when parallel agents touch the same files.
	mergeMu sync.Mutex

	// dagMu serializes DAG mutations (e.g. story splits) so that
	// concurrent pipelines don't corrupt the graph.
	dagMu sync.Mutex
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
		escalation: NewEscalationMachine(es, cfg.Routing),
	}
}

// SetMemPalace enables semantic memory mining during the post-execution
// pipeline. When set, the monitor mines story diffs, review feedback, and
// QA failures into MemPalace for future agent context.
func (m *Monitor) SetMemPalace(mp *memory.MemPalace) {
	m.mempalace = mp
}

// SetArtifactStore enables per-story artifact persistence (diffs, reviews, QA).
func (m *Monitor) SetArtifactStore(store *artifact.Store) {
	m.artifactStore = store
}

// SetCodeGraph enables blast-radius analysis before code review.
func (m *Monitor) SetCodeGraph(cg *codegraph.Runner) {
	m.codeGraph = cg
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

// SetManager enables tier-2 (manager) escalation handling. When set, the
// monitor intercepts tier-2 stories before dispatch and routes them through
// the Manager for LLM-powered failure diagnosis and corrective actions.
func (m *Monitor) SetManager(mgr *Manager) {
	m.manager = mgr
}

// SetPlanner enables tier-3 (tech lead) re-planning. When set, the monitor
// can decompose failing stories into smaller replacement stories via the
// Planner's RePlan method.
func (m *Monitor) SetPlanner(p *Planner) {
	m.planner = p
}

// SetDryRun enables dry-run mode. In this mode, the post-execution pipeline
// writes a simulated change to the worktree so the pipeline can exercise
// the full review->QA->merge flow without real agent output.
func (m *Monitor) SetDryRun(enabled bool) {
	m.dryRun = enabled
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
		// Native runtimes (e.g. Gemma) run in-process goroutines, not tmux
		// sessions. Check the event store for a STORY_COMPLETED event instead.
		if m.registry.IsNative(ag.RuntimeName) {
			m.pollNativeAgent(ctx, wg, active, sessionName, ag, repoDir)
			continue
		}

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

// pollNativeAgent checks whether a native runtime agent (e.g. Gemma) has
// finished by looking for a STORY_COMPLETED event. Native agents run as
// in-process goroutines and emit completion events directly to the store.
func (m *Monitor) pollNativeAgent(ctx context.Context, wg *sync.WaitGroup, active map[string]ActiveAgent, sessionName string, ag ActiveAgent, repoDir string) {
	events, err := m.eventStore.List(state.EventFilter{
		Type:    state.EventStoryCompleted,
		StoryID: ag.Assignment.StoryID,
	})
	if err != nil || len(events) == 0 {
		return // still running
	}

	log.Printf("[monitor] native agent %s finished for %s", ag.Assignment.AgentID, ag.Assignment.StoryID)

	wg.Add(1)
	go func() {
		defer wg.Done()
		m.postExecutionPipeline(ctx, ag, repoDir)
	}()

	delete(active, sessionName)
	log.Printf("[monitor] %d agents remaining", len(active))
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

	// In dry-run mode, simulate a successful agent by writing a placeholder
	// file and committing it.
	if m.dryRun {
		simulateDryRunChanges(ag.WorktreePath, storyID)
	}

	// Check if agent produced any changes.
	// Distinguish between git infrastructure errors (which count toward
	// the retry limit) and genuinely empty diffs so that broken worktrees
	// don't loop forever.
	diff, err := gitDiff(ag.WorktreePath)
	if err != nil {
		log.Printf("[pipeline] git diff error for %s: %v", storyID, err)
		m.resetStoryToDraft(storyID, "monitor", fmt.Sprintf("git diff error: %v", err))
		return
	}
	if diff == "" {
		log.Printf("[pipeline] no changes produced for %s, resetting to draft for re-dispatch", storyID)
		m.resetStoryToDraft(storyID, "monitor", "agent produced no code changes")
		return
	}

	// Persist the diff as an artifact for post-mortem inspection.
	if m.artifactStore != nil {
		m.artifactStore.WriteRaw(storyID, artifact.TypeGitDiff, diff)
	}

	// Look up story details used by review, MemPalace mining, and QA feedback.
	storyTitle := storyID
	reqID := ""
	storyAC := ""
	if story, err := m.projStore.GetStory(storyID); err == nil {
		storyTitle = story.Title
		storyAC = story.AcceptanceCriteria
		reqID = story.ReqID
	}

	// Mine the story diff to MemPalace for future agent context.
	if m.mempalace != nil && m.mempalace.IsAvailable() {
		statDiff := captureStoryDiff(repoDir, branch)
		if statDiff != "" {
			repoName := filepath.Base(repoDir)
			summary := fmt.Sprintf("Story %s (%s) completed. Changes:\n%s", storyID, storyTitle, truncateDiff(statDiff, 2000))
			if err := m.mempalace.Mine(repoName, reqID, summary); err != nil {
				log.Printf("[pipeline] mempalace mine diff for %s: %v", storyID, err)
			}
		}
	}

	// 1. Code Review
	if m.reviewer != nil {
		// Run blast-radius analysis if codegraph is available.
		blastRadius := ""
		if m.codeGraph != nil && m.codeGraph.Available() {
			impact, cgErr := m.codeGraph.DetectChanges(ctx, ag.WorktreePath, "HEAD~1")
			if cgErr != nil {
				log.Printf("[pipeline] codegraph detect-changes warning for %s: %v", storyID, cgErr)
			} else if !impact.Empty() {
				blastRadius = impact.FormatMarkdown()
				log.Printf("[pipeline] codegraph: risk=%.2f, %d changed functions, %d test gaps for %s",
					impact.RiskScore, len(impact.ChangedFunctions), len(impact.TestGaps), storyID)
			}
		}

		result, err := m.reviewer.Review(ctx, storyID, storyTitle, storyAC, diff, blastRadius)
		if err != nil {
			// Fatal API errors (auth failures, billing exhaustion,
			// permission denied) will never succeed on retry -- pause
			// the entire requirement to stop the infinite loop.
			if llm.IsFatalAPIError(err) {
				log.Printf("[pipeline] FATAL: non-retryable API error -- pausing requirement for %s: %v", storyID, err)
				m.pauseRequirement(storyID, fmt.Sprintf("fatal API error: %v", err))
				return
			}
			log.Printf("[pipeline] review error for %s: %v", storyID, err)
			m.resetStoryToDraft(storyID, "reviewer", fmt.Sprintf("review error: %v", err))
			return
		}

		// Mine review feedback to MemPalace.
		if m.mempalace != nil && m.mempalace.IsAvailable() {
			repoName := filepath.Base(repoDir)
			verdict := "approved"
			if !result.Passed {
				verdict = "rejected"
			}
			mineSummary := fmt.Sprintf("Review of %s: %s. %s", storyID, verdict, result.Summary)
			if err := m.mempalace.Mine(repoName, reqID, mineSummary); err != nil {
				log.Printf("[pipeline] mempalace mine review for %s: %v", storyID, err)
			}
		}

		// Persist review result as artifact.
		if m.artifactStore != nil {
			m.artifactStore.Write(storyID, artifact.TypeReviewResult, map[string]any{
				"passed":  result.Passed,
				"summary": result.Summary,
			})
		}

		if !result.Passed {
			m.resetStoryToDraft(storyID, "reviewer", fmt.Sprintf("review rejected: %s", result.Summary))
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

		// Persist QA result as artifact.
		if m.artifactStore != nil {
			m.artifactStore.Write(storyID, artifact.TypeQAResult, map[string]any{
				"passed": result.Passed,
				"checks": result.Checks,
			})
		}

		if !result.Passed {
			log.Printf("[pipeline] QA failed for %s", storyID)

			// Collect QA failure output from failed checks.
			var qaOutput string
			for _, check := range result.Checks {
				if !check.Passed {
					qaOutput += fmt.Sprintf("[%s] %s\n", check.Name, check.Output)
				}
			}

			// Generate a targeted fix hint from the failure output.
			hint := AnalyzeFailure(qaOutput, "")

			// Store as review feedback so the agent sees it on retry.
			retryFeedback := fmt.Sprintf(
				"QA FAILURE — fix this error:\n\n%s\n\nHint: %s\n\nMake the minimal change to fix this. Do not rewrite files.",
				qaOutput, hint,
			)
			feedbackEvt := state.NewEvent(state.EventStoryReviewFailed, "monitor", storyID, map[string]any{
				"feedback": retryFeedback,
				"source":   "qa_failure",
			})
			if err := m.eventStore.Append(feedbackEvt); err != nil {
				log.Printf("[pipeline] failed to append QA feedback event for %s: %v", storyID, err)
			}
			if err := m.projStore.Project(feedbackEvt); err != nil {
				log.Printf("[pipeline] failed to project QA feedback event for %s: %v", storyID, err)
			}

			// Mine QA failure to MemPalace for future learning.
			if m.mempalace != nil && m.mempalace.IsAvailable() {
				repoName := filepath.Base(repoDir)
				if err := m.mempalace.Mine(repoName, reqID, fmt.Sprintf("QA failure on %s: %s", storyID, qaOutput)); err != nil {
					log.Printf("[pipeline] mempalace mine QA failure for %s: %v", storyID, err)
				}
			}

			return
		}
		log.Printf("[pipeline] QA passed for %s", storyID)
	}

	// 3. Merge (serialized: rebase onto latest main, then push + merge)
	if m.config.Merge.ReviewBeforeMerge {
		evt := state.NewEvent(state.EventStoryMergeReady, "", storyID, nil)
		if err := m.eventStore.Append(evt); err != nil {
			log.Printf("[pipeline] failed to append merge_ready event for %s: %v", storyID, err)
		}
		if err := m.projStore.Project(evt); err != nil {
			log.Printf("[pipeline] failed to project merge_ready event for %s: %v", storyID, err)
		}
		log.Printf("[pipeline] story %s ready for merge review (review_before_merge enabled)", storyID)
		return
	}
	if m.merger != nil {
		m.mergeMu.Lock()
		defer m.mergeMu.Unlock()
		result, err := m.rebaseAndMerge(ctx, storyID, branch, repoDir, ag.WorktreePath)

		if err != nil {
			// Fatal API errors during conflict resolution (credits exhausted,
			// auth failure) must pause the requirement immediately.
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

// resetStoryToDraft uses the EscalationMachine to decide whether the story
// should be retried at the current tier, escalated to the next tier, or
// paused (all tiers exhausted). It emits the appropriate events so the
// dispatcher picks the story back up with the correct routing.
func (m *Monitor) resetStoryToDraft(storyID, fromAgent, reason string) {
	shouldEsc, nextTier, err := m.escalation.ShouldEscalate(storyID)
	if err != nil {
		log.Printf("[pipeline] escalation check error for %s: %v", storyID, err)
	}

	if shouldEsc {
		currentTier, _ := m.escalation.CurrentTier(storyID)
		if nextTier >= 4 {
			m.pauseRequirement(storyID, fmt.Sprintf(
				"story exhausted all escalation tiers (%d): %s", currentTier, reason,
			))
			return
		}
		log.Printf("[pipeline] escalating %s from tier %d to tier %d: %s", storyID, currentTier, nextTier, reason)
		escEvt := state.NewEvent(state.EventStoryEscalated, fromAgent, storyID, map[string]any{
			"from_tier": currentTier,
			"to_tier":   nextTier,
			"reason":    reason,
		})
		m.eventStore.Append(escEvt)
		m.projStore.Project(escEvt)
		// Also reset to draft so the dispatcher picks it up at the new tier.
		resetEvt := state.NewEvent(state.EventStoryReviewFailed, fromAgent, storyID, map[string]any{
			"reason": fmt.Sprintf("escalated to tier %d: %s", nextTier, reason),
		})
		m.eventStore.Append(resetEvt)
		m.projStore.Project(resetEvt)
		return
	}

	// Normal reset within current tier.
	retryCount, _ := m.escalation.RetryCountAtCurrentTier(storyID)
	currentTier, _ := m.escalation.CurrentTier(storyID)
	maxRetries := m.escalation.MaxRetriesForTier(currentTier)
	log.Printf("[pipeline] reset %s to draft (attempt %d/%d at tier %d): %s",
		storyID, retryCount+1, maxRetries, currentTier, reason)

	evt := state.NewEvent(state.EventStoryReviewFailed, fromAgent, storyID, map[string]any{
		"reason": reason,
	})
	m.eventStore.Append(evt)
	m.projStore.Project(evt)
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
		if s.Status == "merged" || s.Status == "pr_submitted" || s.Status == "split" {
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

	// Pre-dispatch interception: handle tier 2+ stories inline before
	// they reach the dispatcher. Tier 2 goes to the Manager for LLM
	// diagnosis; tier 3 goes to the tech-lead re-plan path.
	if m.manager != nil {
		readyIDs := rc.DAG.ReadyNodes(completed)
		storyLookup := make(map[string]PlannedStory, len(rc.PlannedStories))
		for _, ps := range rc.PlannedStories {
			storyLookup[ps.ID] = ps
		}

		handledThisWave := make(map[string]bool)

		for _, id := range readyIDs {
			if completed[id] {
				continue
			}
			tier, err := m.escalation.CurrentTier(id)
			if err != nil {
				log.Printf("[auto-resume] tier lookup error for %s: %v", id, err)
				continue
			}
			if tier < 2 {
				continue
			}

			story, ok := storyLookup[id]
			if !ok {
				log.Printf("[auto-resume] story %s not found in planned stories", id)
				continue
			}

			handledThisWave[id] = true

			switch tier {
			case 2:
				log.Printf("[auto-resume] intercepting tier-2 story %s for manager diagnosis", id)
				m.handleManagerEscalation(ctx, story, repoDir, rc)
			default: // tier 3+
				log.Printf("[auto-resume] intercepting tier-%d story %s for tech-lead escalation", tier, id)
				m.handleTechLeadEscalation(ctx, story, repoDir, rc)
			}
		}

		// Mark handled stories as completed for this wave only.
		for id := range handledThisWave {
			completed[id] = true
		}
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

// handleManagerEscalation runs the Manager LLM to diagnose a tier-2 story
// and executes the recommended corrective action (retry, rewrite, split,
// or escalate to tech lead).
func (m *Monitor) handleManagerEscalation(ctx context.Context, story PlannedStory, repoDir string, rc *RunContext) {
	storyID := story.ID
	stateDir := execExpandHome(m.config.Workspace.StateDir)
	worktreePath := filepath.Join(stateDir, "worktrees", storyID)
	logDir := filepath.Join(stateDir, "logs")

	dc, err := m.manager.BuildDiagnosticContext(storyID, worktreePath, logDir)
	if err != nil {
		log.Printf("[manager] context build error for %s: %v", storyID, err)
		m.resetStoryToDraft(storyID, "manager", fmt.Sprintf("context build error: %v", err))
		return
	}

	action, err := m.manager.Diagnose(ctx, dc)
	if err != nil {
		log.Printf("[manager] diagnosis failed for %s: %v", storyID, err)
		if llm.IsFatalAPIError(err) {
			m.pauseRequirement(storyID, fmt.Sprintf("fatal API error in manager: %v", err))
			return
		}
		m.resetStoryToDraft(storyID, "manager", fmt.Sprintf("diagnosis error: %v", err))
		return
	}

	log.Printf("[manager] %s: diagnosis=%q action=%s", storyID, action.Diagnosis, action.Action)

	// Persist the diagnosis for post-mortem review.
	logPath := filepath.Join(logDir, storyID+"-manager.log")
	os.WriteFile(logPath, []byte(fmt.Sprintf("Diagnosis: %s\nCategory: %s\nAction: %s\n",
		action.Diagnosis, action.Category, action.Action)), 0o644)

	switch action.Action {
	case "retry":
		m.executeRetryAction(storyID, action, worktreePath)
	case "rewrite":
		m.executeRewriteAction(storyID, action)
	case "split":
		m.executeSplitAction(ctx, storyID, action, rc, story)
	case "escalate_to_techlead":
		m.escalateToTier(storyID, 3, "manager escalated: "+action.Diagnosis)
	default:
		m.resetStoryToDraft(storyID, "manager", "unknown action: "+action.Action)
	}
}

// executeRetryAction resets a story to a lower tier for re-dispatch,
// optionally removing the worktree for a clean start.
func (m *Monitor) executeRetryAction(storyID string, action ManagerAction, worktreePath string) {
	if action.RetryConfig != nil && action.RetryConfig.WorktreeReset {
		os.RemoveAll(worktreePath)
	}

	resetTier := 0
	if action.RetryConfig != nil {
		resetTier = action.RetryConfig.ResetTier
	}

	evt := state.NewEvent(state.EventStoryEscalated, "manager", storyID, map[string]any{
		"from_tier": 2,
		"to_tier":   resetTier,
		"reason":    "manager retry: " + action.Diagnosis,
	})
	m.eventStore.Append(evt)
	m.projStore.Project(evt)

	resetEvt := state.NewEvent(state.EventStoryReviewFailed, "manager", storyID, map[string]any{
		"reason": "manager retry with fixes",
	})
	m.eventStore.Append(resetEvt)
	m.projStore.Project(resetEvt)
}

// executeRewriteAction emits a STORY_REWRITTEN event to update the story
// definition with the Manager's revised title, description, acceptance
// criteria, and/or complexity.
func (m *Monitor) executeRewriteAction(storyID string, action ManagerAction) {
	if action.RewriteConfig == nil {
		m.resetStoryToDraft(storyID, "manager", "rewrite action with no config")
		return
	}

	changes := map[string]any{}
	if action.RewriteConfig.Title != "" {
		changes["title"] = action.RewriteConfig.Title
	}
	if action.RewriteConfig.Description != "" {
		changes["description"] = action.RewriteConfig.Description
	}
	if action.RewriteConfig.AcceptanceCriteria != "" {
		changes["acceptance_criteria"] = action.RewriteConfig.AcceptanceCriteria
	}
	if action.RewriteConfig.Complexity > 0 {
		changes["complexity"] = action.RewriteConfig.Complexity
	}

	evt := state.NewEvent(state.EventStoryRewritten, "manager", storyID, map[string]any{
		"changes": changes,
		"reason":  action.Diagnosis,
	})
	m.eventStore.Append(evt)
	m.projStore.Project(evt)
}

// executeSplitAction validates and applies a split, creating child stories
// in the event store and mutating the DAG.
func (m *Monitor) executeSplitAction(ctx context.Context, storyID string, action ManagerAction, rc *RunContext, story PlannedStory) {
	if action.SplitConfig == nil || len(action.SplitConfig.Children) == 0 {
		m.resetStoryToDraft(storyID, "manager", "split with no children")
		return
	}

	storyData, err := m.projStore.GetStory(storyID)
	if err != nil {
		m.resetStoryToDraft(storyID, "manager", fmt.Sprintf("cannot look up story for split: %v", err))
		return
	}

	children := make([]SplitChild, 0, len(action.SplitConfig.Children))
	for _, c := range action.SplitConfig.Children {
		children = append(children, SplitChild{
			ID:                 storyID + "-" + c.Suffix,
			Suffix:             c.Suffix,
			Title:              c.Title,
			Description:        c.Description,
			AcceptanceCriteria: c.AcceptanceCriteria,
			Complexity:         c.Complexity,
			OwnedFiles:         c.OwnedFiles,
		})
	}

	if err := m.escalation.ValidateSplit(storyData.SplitDepth, children, m.config.Planning.MaxStoryComplexity); err != nil {
		log.Printf("[manager] split validation failed for %s: %v", storyID, err)
		m.resetStoryToDraft(storyID, "manager", fmt.Sprintf("invalid split: %v", err))
		return
	}

	m.dagMu.Lock()
	defer m.dagMu.Unlock()

	// Create child stories in the event store.
	for _, child := range children {
		childEvt := state.NewEvent(state.EventStoryCreated, "manager", child.ID, map[string]any{
			"id":                  child.ID,
			"req_id":              rc.ReqID,
			"title":               child.Title,
			"description":         child.Description,
			"acceptance_criteria": child.AcceptanceCriteria,
			"complexity":          child.Complexity,
			"owned_files":         child.OwnedFiles,
			"split_depth":         storyData.SplitDepth + 1,
		})
		m.eventStore.Append(childEvt)
		m.projStore.Project(childEvt)
	}

	// Emit STORY_SPLIT for the parent.
	childIDs := make([]string, len(children))
	for i, c := range children {
		childIDs[i] = c.ID
	}
	splitEvt := state.NewEvent(state.EventStorySplit, "manager", storyID, map[string]any{
		"child_story_ids": childIDs,
		"reason":          action.Diagnosis,
	})
	m.eventStore.Append(splitEvt)
	m.projStore.Project(splitEvt)

	// Mutate the DAG to replace the parent with children.
	m.escalation.ApplySplit(
		rc.DAG, rc, storyID, children,
		action.SplitConfig.DependencyEdges,
		story.DependsOn,
		FindDependents(rc.PlannedStories, storyID),
	)
}

// escalateToTier emits a STORY_ESCALATED event moving the story to the
// specified tier.
func (m *Monitor) escalateToTier(storyID string, tier int, reason string) {
	currentTier, _ := m.escalation.CurrentTier(storyID)
	evt := state.NewEvent(state.EventStoryEscalated, "monitor", storyID, map[string]any{
		"from_tier": currentTier,
		"to_tier":   tier,
		"reason":    reason,
	})
	m.eventStore.Append(evt)
	m.projStore.Project(evt)
}

// handleTechLeadEscalation handles tier-3 stories by calling the Planner's
// RePlan method to decompose the failing story into smaller replacements,
// then emitting STORY_SPLIT and mutating the DAG via ApplySplit.
func (m *Monitor) handleTechLeadEscalation(ctx context.Context, story PlannedStory, repoDir string, rc *RunContext) {
	storyID := story.ID
	stateDir := execExpandHome(m.config.Workspace.StateDir)
	logDir := filepath.Join(stateDir, "logs")

	// Build failure context from events and logs.
	events, _ := m.eventStore.List(state.EventFilter{StoryID: storyID})
	var failureContext strings.Builder
	for _, evt := range events {
		fmt.Fprintf(&failureContext, "%s %s (agent: %s)\n", evt.Type, evt.Timestamp.Format("15:04:05"), evt.AgentID)
	}
	logPath := filepath.Join(logDir, storyID+".log")
	if data, err := os.ReadFile(logPath); err == nil {
		failureContext.WriteString("\nAgent log:\n")
		failureContext.Write(data)
	}

	// Check if planner is available.
	if m.planner == nil {
		log.Printf("[tech-lead] no planner available for %s, pausing", storyID)
		m.pauseRequirement(storyID, "tech lead escalation: no planner configured")
		return
	}

	// Call RePlan to get replacement stories.
	replacements, err := m.planner.RePlan(ctx, storyID, rc.ReqID, failureContext.String())
	if err != nil {
		log.Printf("[tech-lead] re-plan failed for %s: %v", storyID, err)
		m.pauseRequirement(storyID, fmt.Sprintf("tech lead re-plan failed: %v", err))
		return
	}

	if len(replacements) == 0 {
		log.Printf("[tech-lead] re-plan produced no stories for %s", storyID)
		m.pauseRequirement(storyID, "tech lead re-plan produced no replacement stories")
		return
	}

	// Build SplitChild list from replacements.
	storyData, _ := m.projStore.GetStory(storyID)
	children := make([]SplitChild, 0, len(replacements))
	for _, r := range replacements {
		children = append(children, SplitChild{
			ID:                 r.ID,
			Title:              r.Title,
			Description:        r.Description,
			AcceptanceCriteria: string(r.AcceptanceCriteria),
			Complexity:         r.Complexity,
			OwnedFiles:         r.OwnedFiles,
		})
	}

	// Validate split constraints before mutating.
	if err := m.escalation.ValidateSplit(storyData.SplitDepth, children, m.config.Planning.MaxStoryComplexity); err != nil {
		log.Printf("[tech-lead] split validation failed for %s: %v", storyID, err)
		m.pauseRequirement(storyID, fmt.Sprintf("tech lead split invalid: %v", err))
		return
	}

	// Emit STORY_SPLIT + mutate DAG (same pattern as executeSplitAction).
	m.dagMu.Lock()
	defer m.dagMu.Unlock()

	// Create child stories in the event store (with split_depth).
	for _, child := range children {
		childEvt := state.NewEvent(state.EventStoryCreated, "tech_lead", child.ID, map[string]any{
			"id":                  child.ID,
			"req_id":              rc.ReqID,
			"title":               child.Title,
			"description":         child.Description,
			"acceptance_criteria": child.AcceptanceCriteria,
			"complexity":          child.Complexity,
			"owned_files":         child.OwnedFiles,
			"split_depth":         storyData.SplitDepth + 1,
		})
		m.eventStore.Append(childEvt)
		m.projStore.Project(childEvt)
	}

	childIDs := make([]string, len(children))
	for i, c := range children {
		childIDs[i] = c.ID
	}
	splitEvt := state.NewEvent(state.EventStorySplit, "tech_lead", storyID, map[string]any{
		"child_story_ids": childIDs,
		"reason":          "tech lead re-plan",
	})
	m.eventStore.Append(splitEvt)
	m.projStore.Project(splitEvt)

	// Build sequential dependency edges for re-planned stories.
	var depEdges [][]string
	for i := 1; i < len(children); i++ {
		depEdges = append(depEdges, []string{children[i].ID, children[i-1].ID})
	}

	m.escalation.ApplySplit(rc.DAG, rc, storyID, children, depEdges,
		story.DependsOn, FindDependents(rc.PlannedStories, storyID))

	log.Printf("[tech-lead] re-planned %s into %d replacement stories", storyID, len(children))
}

// FindDependents returns the IDs of stories that depend on the given storyID.
func FindDependents(stories []PlannedStory, storyID string) []string {
	var deps []string
	for _, s := range stories {
		for _, d := range s.DependsOn {
			if d == storyID {
				deps = append(deps, s.ID)
				break
			}
		}
	}
	return deps
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

// simulateDryRunChanges writes a placeholder file and commits it so the
// post-execution pipeline has a non-empty diff to work with in dry-run mode.
func simulateDryRunChanges(worktreePath, storyID string) {
	simFile := filepath.Join(worktreePath, "dry-run-simulation.txt")
	content := fmt.Sprintf("[DRY RUN] Simulated changes for story %s\n", storyID)
	if err := os.WriteFile(simFile, []byte(content), 0o644); err != nil {
		log.Printf("[dry-run] failed to write simulation file: %v", err)
		return
	}
	addCmd := exec.Command("git", "add", "dry-run-simulation.txt")
	addCmd.Dir = worktreePath
	if err := addCmd.Run(); err != nil {
		log.Printf("[dry-run] git add failed: %v", err)
		return
	}
	commitCmd := exec.Command("git", "commit", "-m", fmt.Sprintf("[dry-run] simulated changes for %s", storyID))
	commitCmd.Dir = worktreePath
	commitCmd.Run()
	log.Printf("[dry-run] simulated changes committed for %s", storyID)
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
// It tries multiple merge-base candidates so it works with local-only
// repos that have no "origin/main".
func gitDiff(worktreePath string) (string, error) {
	// Try merge-base candidates in order of preference.
	candidates := []string{"origin/main", "main"}
	var mbOut []byte
	var mbErr error
	for _, ref := range candidates {
		mbCmd := exec.Command("git", "merge-base", "HEAD", ref)
		mbCmd.Dir = worktreePath
		mbOut, mbErr = mbCmd.Output()
		if mbErr == nil {
			break
		}
	}
	if mbErr != nil {
		// No merge-base found -- fall back to the root commit of the
		// current branch so we diff all changes since the initial commit.
		rootCmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
		rootCmd.Dir = worktreePath
		rootOut, rootErr := rootCmd.Output()
		if rootErr != nil {
			return "", fmt.Errorf("git diff: cannot find merge-base or root commit: %w", rootErr)
		}
		mbOut = rootOut
	}

	mergeBase := strings.TrimSpace(string(mbOut))
	cmd := exec.Command("git", "diff", mergeBase, "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}

	// Filter out diffs that only touch .gitignore (written by
	// ensureGitignorePatterns before this check). A diff limited to
	// .gitignore means the agent produced no real code changes.
	if isGitignoreOnlyDiff(worktreePath, mergeBase) {
		return "", nil
	}

	return string(out), nil
}

// nxdArtifactPatterns are files created by NXD infrastructure, not by the
// agent's actual work.
var nxdArtifactPatterns = []string{
	".gitignore",
	"CLAUDE.md",
	".nxd-prompts/",
	".serena/",
	"dry-run-simulation.txt",
}

func isArtifactFile(path string) bool {
	for _, pattern := range nxdArtifactPatterns {
		if path == pattern || strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

// isGitignoreOnlyDiff returns true when the only files changed between
// mergeBase and HEAD are NXD infrastructure artifacts (not real code).
func isGitignoreOnlyDiff(worktreePath, mergeBase string) bool {
	cmd := exec.Command("git", "diff", "--name-only", mergeBase, "HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	files := strings.TrimSpace(string(out))
	if files == "" {
		return false
	}
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !isArtifactFile(f) {
			return false
		}
	}
	return true
}

// captureStoryDiff returns a compact --stat summary of changes between main
// and the given branch. Returns an empty string on any error so callers can
// skip mining without disrupting the pipeline.
func captureStoryDiff(repoDir, branch string) string {
	cmd := exec.Command("git", "diff", "main..."+branch, "--stat")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// truncateDiff returns s unchanged when it fits within max bytes, otherwise
// truncates and appends an indicator.
func truncateDiff(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}
