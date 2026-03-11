package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <req-id>",
		Short: "Resume a paused requirement pipeline",
		Long:  "Loads existing state for a requirement, dispatches the next wave of ready stories, spawns agents in tmux sessions, and monitors progress through review, QA, and merge.",
		Args:  cobra.ExactArgs(1),
		RunE:  runResume,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runResume(cmd *cobra.Command, args []string) error {
	reqID := args[0]

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	// Verify the requirement exists
	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement not found: %w", err)
	}

	// If paused, emit REQ_RESUMED event to transition back to planned
	if req.Status == "paused" {
		resumeEvt := state.NewEvent(state.EventReqResumed, "", "", map[string]any{
			"id": reqID,
		})
		if err := s.Events.Append(resumeEvt); err != nil {
			return fmt.Errorf("append resume event: %w", err)
		}
		if err := s.Proj.Project(resumeEvt); err != nil {
			return fmt.Errorf("project resume event: %w", err)
		}
		fmt.Fprintf(out, "Unpaused requirement: %s\n", req.Title)
	}

	fmt.Fprintf(out, "Resuming requirement: %s (%s)\n", req.Title, req.Status)

	// Load all stories for this requirement
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return fmt.Errorf("list stories: %w", err)
	}
	if len(stories) == 0 {
		fmt.Fprintf(out, "No stories found for this requirement.\n")
		return nil
	}

	// Rebuild the dependency graph from story_deps table
	dag, plannedStories, err := rebuildDAG(s.Proj, reqID, stories)
	if err != nil {
		return fmt.Errorf("rebuild dependency graph: %w", err)
	}

	// Determine completed stories
	completed := make(map[string]bool)
	for _, story := range stories {
		if story.Status == "merged" || story.Status == "pr_submitted" {
			completed[story.ID] = true
		}
	}
	fmt.Fprintf(out, "Stories: %d total, %d completed\n", len(stories), len(completed))

	if len(completed) == len(stories) {
		fmt.Fprintf(out, "All stories are complete.\n")
		return nil
	}

	// Dispatch next wave
	dispatcher := engine.NewDispatcher(s.Config, s.Events, s.Proj)
	assignments, err := dispatcher.DispatchWave(dag, completed, reqID, plannedStories)
	if err != nil {
		return fmt.Errorf("dispatch wave: %w", err)
	}
	if len(assignments) == 0 {
		fmt.Fprintf(out, "No stories ready for dispatch (dependencies not yet met).\n")
		return nil
	}

	fmt.Fprintf(out, "\nWave: dispatching %d stories\n\n", len(assignments))

	// Build story map for executor
	storyMap := make(map[string]engine.PlannedStory, len(plannedStories))
	for _, ps := range plannedStories {
		storyMap[ps.ID] = ps
	}

	// Set up runtime registry
	reg, err := runtime.NewRegistry(s.Config.Runtimes)
	if err != nil {
		return fmt.Errorf("init runtime registry: %w", err)
	}

	// Detect repo path
	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Verify the repo has at least one commit (worktrees require a base commit)
	if !nxdgit.HasCommits(repoDir) {
		return fmt.Errorf("repository has no commits — run 'git add . && git commit -m \"initial commit\"' first")
	}

	// Spawn agents via executor
	executor := engine.NewExecutor(reg, s.Config, s.Events, s.Proj)
	results := executor.SpawnAll(repoDir, assignments, storyMap)

	activeAgents := make([]engine.ActiveAgent, 0, len(results))
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(out, "  [FAIL] %s: %v\n", r.Assignment.StoryID, r.Error)
			continue
		}
		fmt.Fprintf(out, "  [%s] %s -> %s (session: %s, branch: %s)\n",
			r.Assignment.Role, r.Assignment.StoryID, r.RuntimeName,
			r.Assignment.SessionName, r.Assignment.Branch)
		activeAgents = append(activeAgents, engine.ActiveAgent{
			Assignment:   r.Assignment,
			WorktreePath: r.WorktreePath,
			RuntimeName:  r.RuntimeName,
		})
	}

	if len(activeAgents) == 0 {
		return fmt.Errorf("no agents spawned successfully")
	}

	fmt.Fprintf(out, "\n%d agents working. Monitoring progress...\n", len(activeAgents))
	fmt.Fprintf(out, "Use 'nxd dashboard' in another terminal to watch progress.\n")
	fmt.Fprintf(out, "Press Ctrl+C to detach (agents continue in tmux).\n\n")

	// Build pipeline components for post-execution
	var reviewer *engine.Reviewer
	llmClient, llmErr := buildLLMClient(s.Config.Models.Senior.Provider)
	if llmErr != nil {
		log.Printf("Warning: LLM client unavailable, skipping code review: %v", llmErr)
	} else {
		seniorModel := s.Config.Models.Senior
		reviewer = engine.NewReviewer(llmClient, seniorModel.Model, seniorModel.MaxTokens, s.Events, s.Proj)
	}

	qaRunner := engine.NewQA(engine.QAConfig{}, &engine.ExecRunner{}, s.Events, s.Proj)

	var merger *engine.Merger
	if nxdgit.GHAvailable() {
		merger = engine.NewMerger(s.Config.Merge, &ghOpsAdapter{}, s.Events, s.Proj)
	}

	watchdog := engine.NewWatchdog(engine.WatchdogConfig{
		StuckThresholdS: s.Config.Monitor.StuckThresholdS,
	}, s.Events)

	// Start monitoring loop (Ctrl+C detaches cleanly, agents keep running)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	monitor := engine.NewMonitor(reg, watchdog, reviewer, qaRunner, merger, s.Config, s.Events, s.Proj)
	return monitor.Run(ctx, activeAgents, repoDir)
}

// rebuildDAG reconstructs the dependency graph from the story_deps table
// and builds PlannedStory entries from existing stories.
func rebuildDAG(proj *state.SQLiteStore, reqID string, stories []state.Story) (*graph.DAG, []engine.PlannedStory, error) {
	dag := graph.New()

	planned := make([]engine.PlannedStory, 0, len(stories))
	for _, story := range stories {
		dag.AddNode(story.ID)
		planned = append(planned, engine.PlannedStory{
			ID:          story.ID,
			Title:       story.Title,
			Description: story.Description,
			Complexity:  story.Complexity,
		})
	}

	// Reconstruct edges from story_deps table
	deps, err := proj.ListStoryDeps(reqID)
	if err != nil {
		return nil, nil, fmt.Errorf("list story deps: %w", err)
	}
	for _, dep := range deps {
		dag.AddEdge(dep.StoryID, dep.DependsOnID)
	}

	return dag, planned, nil
}

// ghOpsAdapter wraps the git package functions to satisfy the engine.GitHubOps interface.
type ghOpsAdapter struct{}

func (g *ghOpsAdapter) PushBranch(repoDir, branch string) error {
	return nxdgit.PushBranch(repoDir, branch)
}

func (g *ghOpsAdapter) CreatePR(repoDir, title, body, baseBranch string) (engine.PRCreationResult, error) {
	pr, err := nxdgit.CreatePR(repoDir, title, body, baseBranch)
	if err != nil {
		return engine.PRCreationResult{}, err
	}
	return engine.PRCreationResult{Number: pr.Number, URL: pr.URL}, nil
}

func (g *ghOpsAdapter) MergePR(repoDir string, prNumber int) error {
	return nxdgit.MergePR(repoDir, prNumber)
}
