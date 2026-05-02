package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
	"github.com/tzone85/nexus-dispatch/internal/codegraph"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/plugin"
	"github.com/tzone85/nexus-dispatch/internal/routing"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume [req-id]",
		Short: "Resume a paused requirement pipeline",
		Long:  "Loads existing state for a requirement, dispatches the next wave of ready stories, spawns agents in tmux sessions, and monitors progress through review, QA, and merge.\n\nIf req-id is omitted and only one active (non-archived, non-completed) requirement exists, it is selected automatically.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runResume,
	}
	cmd.Flags().Bool("godmode", false, "skip permission prompts on LLM calls (fully autonomous)")
	cmd.Flags().Bool("force", false, "Force override of lock file if another instance appears stuck")
	cmd.Flags().Bool("dry-run", false, "Simulate LLM responses for pipeline testing (no API calls)")
	cmd.SilenceUsage = true
	return cmd
}

func runResume(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// Auto-select the requirement if only one active (non-archived, non-completed) exists.
	var reqID string
	if len(args) > 0 {
		reqID = args[0]
	} else {
		reqs, listErr := s.Proj.ListRequirementsFiltered(state.ReqFilter{ExcludeArchived: true})
		if listErr != nil {
			return fmt.Errorf("list requirements: %w", listErr)
		}
		var active []state.Requirement
		for _, r := range reqs {
			if r.Status != "completed" && r.Status != "archived" {
				active = append(active, r)
			}
		}
		switch len(active) {
		case 0:
			return fmt.Errorf("no active requirements found — run 'nxd req' first")
		case 1:
			reqID = active[0].ID
			fmt.Fprintf(cmd.OutOrStdout(), "Auto-selected requirement: %s\n", active[0].Title)
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "Multiple active requirements:\n")
			for _, r := range active {
				fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s (%s)\n", r.ID[:8], r.Title, r.Status)
			}
			return fmt.Errorf("specify which requirement to resume: nxd resume <req-id>")
		}
	}

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

	// Initialize Bayesian router for adaptive routing.
	bayesianRouter := routing.NewBayesianRouter()
	priorsPath := filepath.Join(expandHome(s.Config.Workspace.StateDir), "bayesian_priors.json")
	if err := bayesianRouter.Load(priorsPath); err != nil {
		// No prior data yet — start with defaults. This is the normal path
		// on first run or after clearing state.
		bayesianRouter.InitDefaults()
	}

	// Dispatch next wave
	dispatcher := engine.NewDispatcher(s.Config, s.Events, s.Proj)
	dispatcher.SetBayesianRouter(bayesianRouter)
	waveNumber := maxWave + 1
	dispatchStart := time.Now()
	assignments, err := dispatcher.DispatchWave(dag, completed, reqID, plannedStories, waveNumber)
	if err != nil {
		engine.EmitStageCompleted(s.Events, s.Proj, "dispatcher", "", "dispatch", "failure", dispatchStart)
		return fmt.Errorf("dispatch wave: %w", err)
	}
	engine.EmitStageCompleted(s.Events, s.Proj, "dispatcher", "", "dispatch", "success", dispatchStart)
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

	// Build the metrics recorder once and share it between the native
	// runtime (executor stage) and the post-execution pipeline (reviewer /
	// merger stages) so all three categories land in metrics.jsonl with the
	// correct stage label for the reporter.
	stateDirForMetrics := expandHome(s.Config.Workspace.StateDir)
	metricsRecorder := metrics.NewRecorder(filepath.Join(stateDirForMetrics, "metrics.jsonl"))

	// Provide LLM client for native runtimes (Gemma)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	var nativeClient llm.Client
	if dryRun {
		nativeClient = llm.NewDryRunClient(100 * time.Millisecond)
		fmt.Fprintf(out, "[DRY RUN] Using simulated LLM responses\n")
	} else {
		nativeClient, _ = buildLLMClient(s.Config.Models.Junior.Provider)
	}
	if nativeClient != nil {
		// Wrap with metrics so every native LLM call gets recorded with the
		// "executor" stage. Per-story / per-tier / per-role labels are added
		// inside spawnNative once those values are known.
		nativeClient = metrics.LabelStage(
			metrics.NewMetricsClient(nativeClient, metricsRecorder, reqID, "execute", ""),
			"executor",
		)
	}

	// Initialize artifact store for per-story persistence.
	stateDir0 := expandHome(s.Config.Workspace.StateDir)
	artifactDir := filepath.Join(stateDir0, "artifacts")
	artStore, _ := artifact.NewStore(artifactDir)

	// Initialize scratchboard for cross-agent knowledge sharing.
	sbPath := filepath.Join(stateDir0, "scratchboards", reqID+".jsonl")
	sb, _ := scratchboard.New(sbPath)

	// Initialize periodic controller for stuck agent detection.
	controller := engine.NewController(s.Config.Controller, nil, s.Events, s.Proj)

	// Apply optional executor wiring as functional options. Each helper is
	// nil-safe inside the option, so passing a nil store / scratchboard /
	// client just skips wiring without an explicit guard at the call site.
	executor.Configure(
		engine.WithExecLLMClient(nativeClient),
		engine.WithExecArtifactStore(artStore),
		engine.WithExecScratchboard(sb),
		engine.WithExecProjectDir(stateDir0),
		engine.WithExecController(controller),
		// Operator directive injection: native runtime checks for pending
		// USER_DIRECTIVE events at iteration start. CLI: `nxd direct <id>`.
		engine.WithExecDirectiveStore(engine.NewDirectiveStore(s.Events)),
	)

	// Create cancellable context for spawn (parented to ctrl-c). The monitor
	// reuses this same context further down so cancellation propagates to
	// in-flight native goroutines.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	results := executor.SpawnAll(ctx, repoDir, assignments, storyMap)

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
	var llmClient llm.Client
	var llmErr error
	if dryRun {
		llmClient = llm.NewDryRunClient(100 * time.Millisecond)
	} else {
		llmClient, llmErr = buildLLMClient(s.Config.Models.Senior.Provider, godmode)
	}
	if llmErr != nil {
		log.Printf("Warning: LLM client unavailable, skipping code review: %v", llmErr)
	} else {
		// Reuse the metrics recorder created earlier so executor + reviewer
		// + merger entries all land in the same metrics.jsonl with distinct
		// stage labels.
		llmClient = metrics.NewMetricsClient(llmClient, metricsRecorder, reqID, "pipeline", "")

		seniorModel := s.Config.Models.Senior
		// Stamp stage="reviewer" so the metrics reporter can isolate review
		// cost from executor / merger cost.
		reviewerClient := metrics.LabelStage(llmClient, "reviewer")
		reviewer = engine.NewReviewer(reviewerClient, seniorModel.Provider, seniorModel.Model, seniorModel.MaxTokens, s.Events, s.Proj)
	}

	qaRunner := engine.NewQA(engine.QAConfig{
		SuccessCriteria: engine.ConfigCriteriaToRuntime(s.Config.QA.SuccessCriteria),
	}, &engine.ExecRunner{}, s.Events, s.Proj)

	// LB11: honor config.Merge.Mode. Default mode (no GitHub remote, or
	// mode: local in YAML) uses NewLocalMerger which performs offline git
	// merges. Only fall back to GitHub when mode: github is set explicitly.
	var merger *engine.Merger
	switch s.Config.Merge.Mode {
	case engine.MergeModeLocal, "":
		merger = engine.NewLocalMerger(s.Config.Merge, nxdgit.NewLocalMerger(repoDir), s.Events, s.Proj)
	case engine.MergeModeGitHub:
		if nxdgit.GHAvailable() {
			merger = engine.NewMerger(s.Config.Merge, &ghOpsAdapter{}, s.Events, s.Proj)
		} else {
			log.Printf("[merge] mode=github but `gh` CLI not available; falling back to local merge")
			merger = engine.NewLocalMerger(s.Config.Merge, nxdgit.NewLocalMerger(repoDir), s.Events, s.Proj)
		}
	default:
		log.Printf("[merge] unknown merge.mode=%q; defaulting to local", s.Config.Merge.Mode)
		merger = engine.NewLocalMerger(s.Config.Merge, nxdgit.NewLocalMerger(repoDir), s.Events, s.Proj)
	}

	watchdog := engine.NewWatchdog(engine.WatchdogConfig{
		StuckThresholdS: s.Config.Monitor.StuckThresholdS,
	}, s.Events)

	// ctx + cancel created earlier (just before SpawnAll) so cancellation
	// propagates to native runtime goroutines as well as the monitor.

	monitor := engine.NewMonitor(reg, watchdog, reviewer, qaRunner, merger, s.Config, s.Events, s.Proj)

	// Optional codegraph runner for blast-radius analysis. Only wire if the
	// binary is available on PATH; the option is nil-safe but we want to log
	// the activation so operators can see which features the run used.
	var cg *codegraph.Runner
	if r := codegraph.NewRunner(); r.Available() {
		cg = r
		log.Printf("[resume] codegraph enabled: %s", cg.BinPath)
	}

	// Optional LLM-powered conflict resolver during rebase. Stamp stage so
	// its calls land in metrics.jsonl under the "merger" stage.
	var conflictResolver *engine.ConflictResolver
	if llmClient != nil {
		seniorModel := s.Config.Models.Senior
		mergerClient := metrics.LabelStage(llmClient, "merger")
		conflictResolver = engine.NewConflictResolver(mergerClient, seniorModel.Model, seniorModel.MaxTokens, s.Events)
	}

	monitor.Configure(
		engine.WithMonMemPalace(mp),
		engine.WithMonBayesianRouter(bayesianRouter),
		engine.WithMonArtifactStore(artStore),
		engine.WithMonCodeGraph(cg),
		engine.WithMonConflictResolver(conflictResolver),
		engine.WithMonDryRun(dryRun),
		// Auto-resume: when a wave completes, dispatch the next ready wave
		// without waiting for the user to re-run "nxd resume".
		engine.WithMonAutoResume(dispatcher, executor),
	)

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

	// Save Bayesian priors with decay applied (outcomes from this run
	// are fresh, older observations decay toward neutral).
	bayesianRouter.ApplyDecay()
	if err := bayesianRouter.Save(priorsPath); err != nil {
		log.Printf("[bayesian] failed to save priors: %v", err)
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
