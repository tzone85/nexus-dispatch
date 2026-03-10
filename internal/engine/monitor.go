package engine

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Monitor polls running agents and progresses completed stories through
// review, QA, and merge.
type Monitor struct {
	registry   *runtime.Registry
	watchdog   *Watchdog
	reviewer   *Reviewer
	qa         *QA
	merger     *Merger
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
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

// Run polls active agents at the configured interval until all are done
// or the context is cancelled.
func (m *Monitor) Run(ctx context.Context, agents []ActiveAgent, repoDir string) error {
	pollInterval := time.Duration(m.config.Monitor.PollIntervalMs) * time.Millisecond
	if pollInterval == 0 {
		pollInterval = 10 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

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
				log.Printf("[monitor] all agents finished")
				return nil
			}

			m.pollOnce(ctx, active, repoDir)
		}
	}
}

// pollOnce performs a single pass over active agents, checking status and
// kicking off post-execution pipelines for any that have finished.
func (m *Monitor) pollOnce(ctx context.Context, active map[string]ActiveAgent, repoDir string) {
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
			continue
		}

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
		go m.postExecutionPipeline(ctx, ag, repoDir)

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

	// 1. Code Review
	if m.reviewer != nil {
		diff, err := gitDiff(ag.WorktreePath)
		if err != nil {
			log.Printf("[pipeline] git diff error for %s: %v", storyID, err)
		}
		if diff != "" {
			result, err := m.reviewer.Review(ctx, storyID, storyID, "", diff)
			if err != nil {
				log.Printf("[pipeline] review error for %s: %v", storyID, err)
				return
			}
			if !result.Passed {
				log.Printf("[pipeline] review rejected %s: %s", storyID, result.Summary)
				return
			}
			log.Printf("[pipeline] review passed for %s", storyID)
		}
	}

	// 2. QA
	if m.qa != nil {
		result, err := m.qa.Run(ctx, storyID, ag.WorktreePath)
		if err != nil {
			log.Printf("[pipeline] QA error for %s: %v", storyID, err)
			return
		}
		if !result.Passed {
			log.Printf("[pipeline] QA failed for %s", storyID)
			return
		}
		log.Printf("[pipeline] QA passed for %s", storyID)
	}

	// 3. Merge
	if m.merger != nil {
		result, err := m.merger.Merge(storyID, storyID, repoDir, branch)
		if err != nil {
			log.Printf("[pipeline] merge error for %s: %v", storyID, err)
			return
		}
		log.Printf("[pipeline] %s -> PR #%d (%s) merged=%v",
			storyID, result.PRNumber, result.PRURL, result.Merged)
	}
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
