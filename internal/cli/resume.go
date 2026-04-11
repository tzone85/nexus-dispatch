package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/plugin"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <req-id>",
		Short: "Resume a paused requirement pipeline",
		Long:  "Loads existing state for a requirement, dispatches the next wave of ready stories, spawns agents in tmux sessions, and monitors progress through review, QA, and merge.",
		Args:  cobra.ExactArgs(1),
		RunE:  runResume,
	}
	cmd.Flags().Bool("godmode", false, "skip permission prompts on LLM calls (fully autonomous)")
	cmd.Flags().Bool("force", false, "Force override of lock file if another instance appears stuck")
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

	// Load plugins.
	pluginDir := expandHome("~/.nxd/plugins")
	pm, pluginErr := plugin.LoadPlugins(s.Config.Plugins, pluginDir)
	if pluginErr != nil {
		fmt.Fprintf(out, "Warning: plugin loading failed: %v\n", pluginErr)
		pm = plugin.EmptyManager()
	}

	// Apply plugin prompts and playbooks.
	var pbEntries []agent.PluginPlaybookEntry
	for _, pb := range pm.Playbooks {
		pbEntries = append(pbEntries, agent.PluginPlaybookEntry{
			Content:    pb.Content,
			InjectWhen: pb.InjectWhen,
			Roles:      pb.Roles,
		})
	}
	agent.SetPluginState(pbEntries, pm.Prompts)

	// Make plugin providers available to buildLLMClient.
	activePluginProviders = pm.Providers

	// Acquire pipeline lock to prevent concurrent runs.
	stateDir := expandHome(s.Config.Workspace.StateDir)
	forceFlag, _ := cmd.Flags().GetBool("force")
	if forceFlag {
		// Force removes any existing lock file before acquiring.
		os.Remove(filepath.Join(stateDir, "nxd.lock"))
	}
	lock, err := engine.AcquireLock(stateDir)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Detect repo path early for recovery (also used later for execution).
	repoDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// Run crash recovery before dispatching.
	recoveryActions := engine.RunRecovery(repoDir, s.Events, s.Proj)
	if len(recoveryActions) > 0 {
		fmt.Fprintf(out, "Recovery: fixed %d issues\n", len(recoveryActions))
		for _, a := range recoveryActions {
			fmt.Fprintf(out, "  - %s: %s\n", a.StoryID, a.Description)
		}
		fmt.Fprintln(out)
	}

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

	// Determine completed stories and max wave number.
	completed := make(map[string]bool)
	maxWave := 0
	for _, story := range stories {
		if story.Status == "merged" || story.Status == "pr_submitted" {
			completed[story.ID] = true
		}
		if story.Wave > maxWave {
			maxWave = story.Wave
		}
	}
	fmt.Fprintf(out, "Stories: %d total, %d completed\n", len(stories), len(completed))

	if len(completed) == len(stories) {
		fmt.Fprintf(out, "All stories are complete.\n")
		return nil
	}

	// Run consistency check for crash recovery.
	_ = filepath.Join(stateDir, "checkpoint.json") // reserved for future checkpoint persistence
	recoveryIssues := runConsistencyCheck(stories, stateDir)
	if len(recoveryIssues) > 0 {
		fmt.Fprintf(out, "\nRecovery: found %d inconsistent stories\n", len(recoveryIssues))
		for _, issue := range recoveryIssues {
			fmt.Fprintf(out, "  [RECOVERY] %s: %s\n", issue.StoryID, issue.Detail)
			if issue.Action == engine.ActionResetToDraft {
				evt := state.NewEvent(state.EventStoryReset, "recovery", issue.StoryID, map[string]any{
					"reason": issue.Detail,
				})
				s.Events.Append(evt)
				s.Proj.Project(evt)
			}
		}
		recoveryEvt := state.NewEvent(state.EventRecoveryCompleted, "system", "", map[string]any{
			"issues_found": len(recoveryIssues),
		})
		s.Events.Append(recoveryEvt)
		s.Proj.Project(recoveryEvt)
	}

	// Dispatch next wave
	dispatcher := engine.NewDispatcher(s.Config, s.Events, s.Proj)
	waveNumber := maxWave + 1
	assignments, err := dispatcher.DispatchWave(dag, completed, reqID, plannedStories, waveNumber)
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

	// Verify the repo has at least one commit (worktrees require a base commit)
	if !nxdgit.HasCommits(repoDir) {
		return fmt.Errorf("repository has no commits — run 'git add . && git commit -m \"initial commit\"' first")
	}

	// Initialize MemPalace for semantic memory (degrades gracefully when unavailable).
	mp := memory.NewMemPalace()

	// Spawn agents via executor
	executor := engine.NewExecutor(reg, s.Config, s.Events, s.Proj, mp)

	// Provide LLM client for native runtimes (Gemma)
	nativeClient, nativeErr := buildLLMClient(s.Config.Models.Junior.Provider)
	if nativeErr == nil {
		executor.SetLLMClient(nativeClient)
	}

	// Initialize artifact store for per-story persistence.
	stateDir0 := expandHome(s.Config.Workspace.StateDir)
	artifactDir := filepath.Join(stateDir0, "artifacts")
	artStore, artErr := artifact.NewStore(artifactDir)
	if artErr == nil {
		executor.SetArtifactStore(artStore)
	}

	// Initialize scratchboard for cross-agent knowledge sharing.
	sbPath := filepath.Join(stateDir0, "scratchboards", reqID+".jsonl")
	sb, sbErr := scratchboard.New(sbPath)
	if sbErr == nil {
		executor.SetScratchboard(sb)
	}

	// Initialize periodic controller for stuck agent detection.
	controller := engine.NewController(s.Config.Controller, nil, s.Events, s.Proj)
	executor.SetController(controller)

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
	godmode, _ := cmd.Flags().GetBool("godmode")
	if !godmode {
		godmode = s.Config.Planning.Godmode
	}

	var reviewer *engine.Reviewer
	llmClient, llmErr := buildLLMClient(s.Config.Models.Senior.Provider, godmode)
	if llmErr != nil {
		log.Printf("Warning: LLM client unavailable, skipping code review: %v", llmErr)
	} else {
		// Wrap LLM client with metrics tracking
		stateDir := expandHome(s.Config.Workspace.StateDir)
		recorder := metrics.NewRecorder(filepath.Join(stateDir, "metrics.jsonl"))
		llmClient = metrics.NewMetricsClient(llmClient, recorder, reqID, "pipeline", "")

		seniorModel := s.Config.Models.Senior
		reviewer = engine.NewReviewer(llmClient, seniorModel.Provider, seniorModel.Model, seniorModel.MaxTokens, s.Events, s.Proj)
	}

	// Convert config success criteria to engine criteria.
	var successCriteria []criteria.Criterion
	for _, sc := range s.Config.QA.SuccessCriteria {
		successCriteria = append(successCriteria, criteria.Criterion{
			Type:     criteria.Type(sc.Kind),
			Target:   sc.Path,
			Expected: sc.Value,
		})
	}

	qaRunner := engine.NewQA(engine.QAConfig{
		SuccessCriteria: successCriteria,
	}, &engine.ExecRunner{}, s.Events, s.Proj)

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
	monitor.SetMemPalace(mp)
	if artStore != nil {
		monitor.SetArtifactStore(artStore)
	}

	// Enable LLM-powered conflict resolution during rebase.
	if llmClient != nil {
		seniorModel := s.Config.Models.Senior
		conflictResolver := engine.NewConflictResolver(llmClient, seniorModel.Model, seniorModel.MaxTokens, s.Events)
		monitor.SetConflictResolver(conflictResolver)
	}

	// Enable auto-resume: when a wave completes, the monitor automatically
	// dispatches the next wave of ready stories instead of exiting.
	monitor.SetAutoResume(dispatcher, executor)

	rc := &engine.RunContext{
		ReqID:          reqID,
		PlannedStories: plannedStories,
		DAG:            dag,
		WaveNumber:     maxWave + 1,
	}

	// Start periodic controller in background (if enabled).
	go controller.RunLoop(ctx)

	if err := monitor.RunWithContext(ctx, activeAgents, repoDir, rc); err != nil {
		return err
	}

	// Print completion summary if the requirement finished.
	req, reqErr := s.Proj.GetRequirement(reqID)
	if reqErr == nil && req.Status == "completed" {
		summary, sumErr := engine.GenerateSummary(s.Events, s.Proj, reqID)
		if sumErr == nil {
			fmt.Fprint(out, summary)
		}
	}

	return nil
}

// rebuildDAG reconstructs the dependency graph from the story_deps table
// and builds PlannedStory entries from existing stories.
func rebuildDAG(proj *state.SQLiteStore, reqID string, stories []state.Story) (*graph.DAG, []engine.PlannedStory, error) {
	dag := graph.New()

	planned := make([]engine.PlannedStory, 0, len(stories))
	for _, story := range stories {
		dag.AddNode(story.ID)
		planned = append(planned, engine.PlannedStory{
			ID:                 story.ID,
			Title:              story.Title,
			Description:        story.Description,
			AcceptanceCriteria: engine.FlexibleString(story.AcceptanceCriteria),
			Complexity:         story.Complexity,
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

func (g *ghOpsAdapter) CreatePR(repoDir, title, body, baseBranch, headBranch string) (engine.PRCreationResult, error) {
	pr, err := nxdgit.CreatePR(repoDir, title, body, baseBranch, headBranch)
	if err != nil {
		return engine.PRCreationResult{}, err
	}
	return engine.PRCreationResult{Number: pr.Number, URL: pr.URL}, nil
}

func (g *ghOpsAdapter) MergePR(repoDir string, prNumber int) error {
	return nxdgit.MergePR(repoDir, prNumber)
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func runConsistencyCheck(stories []state.Story, stateDir string) []engine.RecoveryIssue {
	worktreeBase := filepath.Join(stateDir, "worktrees")

	var cp *engine.Checkpoint
	checkpointPath := filepath.Join(stateDir, "checkpoint.json")
	if read, err := engine.ReadCheckpoint(checkpointPath); err == nil {
		cp = &read
	}

	var recoveryStories []engine.RecoveryStory
	for _, story := range stories {
		if story.Status != "in_progress" && story.Status != "merging" {
			continue
		}
		rs := engine.RecoveryStory{
			ID:          story.ID,
			Status:      story.Status,
			HasWorktree: dirExists(filepath.Join(worktreeBase, story.ID)),
		}
		if story.AgentID != "" {
			sessionName := fmt.Sprintf("nxd-%s", story.ID)
			rs.HasTmux = tmux.SessionExists(sessionName)
		}
		recoveryStories = append(recoveryStories, rs)
	}

	return engine.CheckConsistency(recoveryStories, cp)
}
